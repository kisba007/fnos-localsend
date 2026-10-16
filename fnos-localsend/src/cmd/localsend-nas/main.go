// localsend-nas: send-only LocalSend node with an embedded web UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexindigo/localsend-nas/internal/config"
	"github.com/alexindigo/localsend-nas/internal/discovery"
	"github.com/alexindigo/localsend-nas/internal/httpapi"
	"github.com/alexindigo/localsend-nas/internal/identity"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/lsserver"
	"github.com/alexindigo/localsend-nas/internal/receive"
	"github.com/alexindigo/localsend-nas/internal/settings"
	"github.com/alexindigo/localsend-nas/internal/shares"
	"github.com/alexindigo/localsend-nas/internal/transfer"
)

// version is stamped by release builds via -X main.version=...
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(2)
	}

	id, err := identity.Load(cfg.DataDir)
	if err != nil {
		log.Error("identity", "error", err)
		os.Exit(1)
	}

	roots := map[string]string{}
	for _, sh := range cfg.Shares {
		roots[sh.Name] = sh.Path
	}
	// The receive directory doubles as the "inbox" share (browse + save).
	if cfg.ReceiveDir != "" {
		if _, exists := roots["inbox"]; !exists {
			roots["inbox"] = cfg.ReceiveDir
		}
	}
	store, err := shares.NewStore(roots)
	if err != nil {
		log.Error("shares", "error", err)
		os.Exit(1)
	}

	info := localsend.Info{
		Alias:       cfg.Alias,
		Version:     localsend.ProtocolVersion,
		DeviceModel: "localsend-nas",
		DeviceType:  "server",
		Fingerprint: id.Fingerprint,
		Port:        cfg.LSPort,
		Protocol:    "https",
		Download:    false,
	}

	settingsStore, err := settings.Load(cfg.DataDir)
	if err != nil {
		log.Error("settings", "error", err)
		os.Exit(1)
	}
	// First run: seed the receive directory from the flag (so received
	// files land in the configured location even before the UI is touched).
	if cfg.ReceiveDir != "" && settingsStore.Get().ReceiveDir == "" {
		cur := settingsStore.Get()
		cur.ReceiveDir = cfg.ReceiveDir
		if _, err := settingsStore.Update(cur); err != nil {
			log.Warn("settings: could not persist default receive dir", "error", err)
		}
	}
	// Listing filter: receive partials always hidden; NAS metadata hidden
	// unless enabled in settings.
	store.HideEntry = func(name string) bool {
		if strings.HasSuffix(name, ".lnas-part") {
			return true
		}
		return !settingsStore.Get().ShowNasNoise && shares.NASNoise(name)
	}

	client := localsend.NewClient(id.Cert)
	disc := discovery.New(info, client, cfg.DataDir, log)
	tm := transfer.New(store, disc, client, info, log)

	var receiver *receive.Manager
	if !cfg.ReadOnly {
		receiver = receive.New(store, settingsStore, tm, log)
		tm.SetReceiveCancel(receiver.CancelSession)
	}
	lssrv := lsserver.New(cfg.LSPort, id, info, disc, receiver)

	apiHandler := httpapi.New(cfg, store, disc, tm, settingsStore, receiver, info, version)
	tm.SetGlobalNotify(apiHandler.BroadcastJob)
	webSrv := &http.Server{Addr: cfg.Listen, Handler: apiHandler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	disc.Start(ctx)

	go func() {
		mode := "send+receive"
		if cfg.ReadOnly {
			mode = "send-only"
		}
		log.Info("localsend endpoint listening", "port", cfg.LSPort, "protocol", "https", "mode", mode)
		if err := lssrv.ListenAndServeTLS(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("localsend endpoint", "error", err)
			stop()
		}
	}()
	go func() {
		log.Info("web UI listening", "addr", cfg.Listen)
		if err := webSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("web UI", "error", err)
			stop()
		}
	}()

	// Surface unreadable shares immediately in the container log — the
	// classic NAS failure (uid/gid mismatch) must not wait for a browser.
	for _, name := range store.Names() {
		if _, err := store.List(name, ""); err != nil {
			log.Warn("share not readable by the container user — check PUID/PGID", "share", name, "error", err)
		}
	}

	names := store.Names()
	log.Info("localsend-nas started",
		"version", version,
		"alias", info.Alias,
		"fingerprint", id.Fingerprint,
		"shares", names,
	)

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	webSrv.Shutdown(shutdownCtx)
	lssrv.Shutdown(shutdownCtx)
}
