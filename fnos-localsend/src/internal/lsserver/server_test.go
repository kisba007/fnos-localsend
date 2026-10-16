package lsserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexindigo/localsend-nas/internal/identity"
	"github.com/alexindigo/localsend-nas/internal/localsend"
)

type fakeRegistry struct{ got localsend.Info }

func (f *fakeRegistry) Upsert(info localsend.Info, ip string) { f.got = info }

func testServer(t *testing.T, reg Registry) *httptest.Server {
	t.Helper()
	id, err := identity.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info := localsend.Info{Alias: "Nas test", Fingerprint: id.Fingerprint, Port: 53317, Protocol: "https"}
	s := New(0, id, info, reg, nil)
	ts := httptest.NewUnstartedServer(s.http.Handler)
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

func TestInfo(t *testing.T) {
	ts := testServer(t, &fakeRegistry{})
	resp, err := http.Get(ts.URL + "/api/localsend/v2/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var info localsend.Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Alias != "Nas test" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestRegisterUpserts(t *testing.T) {
	reg := &fakeRegistry{}
	ts := testServer(t, reg)
	body := `{"alias":"Phone","fingerprint":"abc123","port":53317,"protocol":"https"}`
	resp, err := http.Post(ts.URL+"/api/localsend/v2/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if reg.got.Fingerprint != "abc123" {
		t.Fatalf("peer not upserted: %+v", reg.got)
	}
}

func TestPrepareUploadDeclined(t *testing.T) {
	ts := testServer(t, &fakeRegistry{})
	resp, err := http.Post(ts.URL+"/api/localsend/v2/prepare-upload", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "send-only") {
		t.Fatalf("body %q lacks send-only message", b)
	}
}

func TestCancelNoContent(t *testing.T) {
	ts := testServer(t, &fakeRegistry{})
	resp, err := http.Post(ts.URL+"/api/localsend/v2/cancel?sessionId=x", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want 204", resp.StatusCode)
	}
}
