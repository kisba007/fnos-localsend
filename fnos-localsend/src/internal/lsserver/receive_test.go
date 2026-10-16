package lsserver

import (
	"context"

	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexindigo/localsend-nas/internal/identity"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/receive"
	"github.com/alexindigo/localsend-nas/internal/settings"
	"github.com/alexindigo/localsend-nas/internal/shares"
)

// receiveRig spins a full TLS receive server (real identity, real client
// cert verification path) plus a sender side using our own protocol client
// with its own identity — the same flow official senders use.
type receiveRig struct {
	ts      *httptest.Server
	rcv     *receive.Manager
	client  *localsend.Client
	sender  localsend.Info
	host    string
	port    int
	destDir string
}

func newReceiveRig(t *testing.T, st *settings.Store) *receiveRig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srvID, err := identity.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	store, err := shares.NewStore(map[string]string{"inbox": destDir})
	if err != nil {
		t.Fatal(err)
	}
	rcv := receive.New(store, st, nil, log)
	srvInfo := localsend.Info{Alias: "Nas test", Fingerprint: srvID.Fingerprint, Port: 53317, Protocol: "https"}
	s := New(0, srvID, srvInfo, &fakeRegistry{}, rcv)
	ts := httptest.NewUnstartedServer(s.http.Handler)
	ts.TLS = srvID.TLSConfig()
	ts.StartTLS()
	t.Cleanup(ts.Close)

	senderID, err := identity.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := strings.TrimPrefix(ts.URL, "https://")
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)

	return &receiveRig{
		ts:      ts,
		rcv:     rcv,
		client:  localsend.NewClient(senderID.Cert),
		sender:  localsend.Info{Alias: "phone", Fingerprint: senderID.Fingerprint, Port: 53317, Protocol: "https", Version: "2.2"},
		host:    host,
		port:    port,
		destDir: destDir,
	}
}

func (r *receiveRig) prepare(t *testing.T, files map[string]localsend.FileDTO, fpOverride string) (<-chan prepareResult, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	out := make(chan prepareResult, 1)
	info := r.sender
	if fpOverride != "" {
		info.Fingerprint = fpOverride
	}
	go func() {
		resp, status, err := r.client.Prepare(ctx, r.host, r.port, localsend.PrepareRequest{Info: info, Files: files})
		out <- prepareResult{resp, status, err}
	}()
	return out, cancel
}

type prepareResult struct {
	resp   *localsend.PrepareResponse
	status int
	err    error
}

func (r *receiveRig) awaitPending(t *testing.T) *receive.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := r.rcv.Pending(); len(p) == 1 {
			return p[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no pending session appeared")
	return nil
}

func helloFiles() map[string]localsend.FileDTO {
	return map[string]localsend.FileDTO{
		"f0": {ID: "f0", FileName: "hello.txt", Size: 5, FileType: "text/plain"},
	}
}

func TestReceiveEndToEnd(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)

	prepCh, cancel := rig.prepare(t, helloFiles(), "")
	defer cancel()
	sess := rig.awaitPending(t)
	if err := rig.rcv.Decide(sess.ID, true, "inbox"); err != nil {
		t.Fatal(err)
	}
	prep := <-prepCh
	if prep.err != nil || prep.status != 200 {
		t.Fatalf("prepare: status=%d err=%v", prep.status, prep.err)
	}
	token := prep.resp.Files["f0"]
	if token == "" || prep.resp.SessionID != sess.ID {
		t.Fatalf("bad prepare response: %+v", prep.resp)
	}
	if err := rig.client.Upload(context.Background(), rig.host, rig.port, sess.ID, "f0", token, 5, strings.NewReader("hello"), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(rig.destDir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content %q", got)
	}
	if sess.State != receive.StateDone {
		t.Fatalf("session state %q", sess.State)
	}
}

func TestReceiveDecline(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)
	prepCh, cancel := rig.prepare(t, helloFiles(), "")
	defer cancel()
	sess := rig.awaitPending(t)
	if err := rig.rcv.Decide(sess.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	prep := <-prepCh
	if prep.status != 403 {
		t.Fatalf("status %d, want 403", prep.status)
	}
}

func TestReceiveRejectsTraversalName(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)
	files := map[string]localsend.FileDTO{
		"f0": {ID: "f0", FileName: "../escape.txt", Size: 5, FileType: "text/plain"},
	}
	prepCh, cancel := rig.prepare(t, files, "")
	defer cancel()
	prep := <-prepCh
	if prep.status != 400 {
		t.Fatalf("status %d, want 400", prep.status)
	}
}

func TestReceiveFingerprintMismatch(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)
	prepCh, cancel := rig.prepare(t, helloFiles(), "DEADBEEF")
	defer cancel()
	prep := <-prepCh
	if prep.status != 403 {
		t.Fatalf("status %d, want 403", prep.status)
	}
}

func TestReceiveBadToken(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)
	prepCh, cancel := rig.prepare(t, helloFiles(), "")
	defer cancel()
	sess := rig.awaitPending(t)
	rig.rcv.Decide(sess.ID, true, "inbox")
	prep := <-prepCh
	if prep.status != 200 {
		t.Fatalf("prepare status %d", prep.status)
	}
	err := rig.client.Upload(context.Background(), rig.host, rig.port, sess.ID, "f0", "wrong-token", 5, strings.NewReader("hello"), nil)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("upload with bad token: %v", err)
	}
	rig.rcv.CancelSession(sess.ID)
}

func TestReceiveDedupe(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	rig := newReceiveRig(t, st)
	for i, want := range []string{"hello.txt", "hello (1).txt"} {
		prepCh, cancel := rig.prepare(t, helloFiles(), "")
		sess := rig.awaitPending(t)
		rig.rcv.Decide(sess.ID, true, "inbox")
		prep := <-prepCh
		if prep.status != 200 {
			t.Fatalf("round %d prepare status %d", i, prep.status)
		}
		if err := rig.client.Upload(context.Background(), rig.host, rig.port, sess.ID, "f0", prep.resp.Files["f0"], 5, strings.NewReader("hello"), nil); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := os.Stat(filepath.Join(rig.destDir, want)); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
}

func TestReceiveDropboxAutoAccept(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	if _, err := st.Update(settings.Settings{AcceptTimeoutSec: 5, DropboxShare: "inbox"}); err != nil {
		t.Fatal(err)
	}
	rig := newReceiveRig(t, st)
	prepCh, cancel := rig.prepare(t, helloFiles(), "")
	defer cancel()
	_ = rig.awaitPending(t)
	prep := <-prepCh // no Decide — the countdown must auto-accept into the dropbox share
	if prep.status != 200 {
		t.Fatalf("status %d, want 200 (dropbox auto-accept); err=%v", prep.status, prep.err)
	}
	if err := rig.client.Upload(context.Background(), rig.host, rig.port, prep.resp.SessionID, "f0", prep.resp.Files["f0"], 5, strings.NewReader("hello"), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(rig.destDir, "hello.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("dropbox file: %v %q", err, got)
	}
}

func TestReceiveTimeoutDeclines(t *testing.T) {
	st, _ := settings.Load(t.TempDir())
	if _, err := st.Update(settings.Settings{AcceptTimeoutSec: 5}); err != nil {
		t.Fatal(err)
	}
	rig := newReceiveRig(t, st)
	prepCh, cancel := rig.prepare(t, helloFiles(), "")
	defer cancel()
	_ = rig.awaitPending(t)
	prep := <-prepCh
	if prep.status != 403 {
		t.Fatalf("status %d, want 403 (timeout decline)", prep.status)
	}
}
