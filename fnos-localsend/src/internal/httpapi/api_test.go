package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexindigo/localsend-nas/internal/config"
	"github.com/alexindigo/localsend-nas/internal/discovery"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/settings"
	"github.com/alexindigo/localsend-nas/internal/shares"
	"github.com/alexindigo/localsend-nas/internal/transfer"
)

func testAPI(t *testing.T, lsPort int) (http.Handler, *shares.Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := shares.NewStore(map[string]string{"test": root})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{LSPort: lsPort}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	info := localsend.Info{Alias: "Nas test", Fingerprint: "ABC123", Port: lsPort}
	disc := discovery.New(info, nil, t.TempDir(), log)
	tm := transfer.New(store, disc, nil, info, log)
	st, err := settings.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, store, disc, tm, st, nil, info, "v-test"), store, root
}

func TestHealth(t *testing.T) {
	h, _, _ := testAPI(t, 53317)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["version"] != "v-test" || body["protocol"] != "2.2" || body["fingerprint"] != "ABC123" {
		t.Fatalf("unexpected health body: %v", body)
	}
}

func TestSelftest503OnBrokenShare(t *testing.T) {
	// Port 1 is guaranteed closed → lsPort check must fail with 503.
	h, _, _ := testAPI(t, 1)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/selftest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.StatusCode)
	}
	var body struct {
		OK     bool `json:"ok"`
		Checks map[string]struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatal("ok=true with a dead lsPort")
	}
	if body.Checks["lsPort"].OK || body.Checks["lsPort"].Error == "" {
		t.Fatalf("lsPort check should fail with an error: %+v", body.Checks)
	}
	if !body.Checks["shares"].OK {
		t.Fatalf("shares check should pass: %+v", body.Checks)
	}
}

func TestSelftestShareRootVanished(t *testing.T) {
	h, _, root := testAPI(t, 1)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/selftest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Checks map[string]struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"checks"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Checks["shares"].OK {
		t.Fatal("shares check should fail after root removal")
	}
}
