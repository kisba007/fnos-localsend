package discovery

import (
	"io"
	"log/slog"
	"testing"

	"github.com/alexindigo/localsend-nas/internal/localsend"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"127.0.0.1", "127.0.0.1", DefaultPort, false},
		{"127.0.0.1:53318", "127.0.0.1", 53318, false},
		{"phone.lan", "phone.lan", DefaultPort, false},
		{"", "", 0, true},
		{"a:b", "", 0, true},
		{"127.0.0.1:99999", "", 0, true},
	}
	for _, c := range cases {
		host, port, err := parseAddress(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("parseAddress(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && (host != c.wantHost || port != c.wantPort) {
			t.Fatalf("parseAddress(%q) = %q:%d, want %q:%d", c.in, host, port, c.wantHost, c.wantPort)
		}
	}
}

func TestUpsertSelfFiltered(t *testing.T) {
	self := localsend.Info{Fingerprint: "self", Alias: "me"}
	d := New(self, nil, t.TempDir(), testLogger())
	d.Upsert(self, "10.0.0.1")
	if got := d.Snapshot(); len(got) != 0 {
		t.Fatalf("self announcement registered: %+v", got)
	}
}

func TestUpsertAndEvict(t *testing.T) {
	self := localsend.Info{Fingerprint: "self"}
	d := New(self, nil, t.TempDir(), testLogger())
	peer := localsend.Info{Fingerprint: "peer1", Alias: "Phone"}
	d.Upsert(peer, "10.0.0.2")
	if got := d.Snapshot(); len(got) != 1 || got[0].IP != "10.0.0.2" {
		t.Fatalf("peer not registered: %+v", got)
	}
	// Manual entries survive eviction; announced ones expire.
	d.mu.Lock()
	d.devices["peer1"].LastSeen = d.devices["peer1"].LastSeen.Add(-2 * registryTTL)
	d.devices["peer2"] = &Device{Info: localsend.Info{Fingerprint: "peer2", Alias: "Laptop"}, Manual: true}
	d.mu.Unlock()
	d.evict()
	got := d.Snapshot()
	if len(got) != 1 || got[0].Info.Fingerprint != "peer2" {
		t.Fatalf("wrong eviction result: %+v", got)
	}
	// Any known device can be removed (discovered ones may re-announce).
	if d.Remove("peer2") != true {
		t.Fatal("manual remove failed")
	}
	d.Upsert(peer, "10.0.0.2")
	if d.Remove("peer1") != true {
		t.Fatal("announced device remove failed")
	}
	if d.Remove("nonexistent") != false {
		t.Fatal("removing unknown device must report false")
	}
}
