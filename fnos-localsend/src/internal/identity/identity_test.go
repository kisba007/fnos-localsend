package identity

import (
	"os"
	"path/filepath"
	"testing"
)

// Fingerprint must stay stable across restarts so receivers keep
// "remembering" the node (protocol v2 §2).
func TestLoadPersistsFingerprint(t *testing.T) {
	dir := t.TempDir()
	id1, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(id1.Fingerprint) != 64 {
		t.Fatalf("fingerprint is not a SHA-256 hex: %q", id1.Fingerprint)
	}
	id2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1.Fingerprint != id2.Fingerprint {
		t.Fatalf("fingerprint changed across reload: %q -> %q", id1.Fingerprint, id2.Fingerprint)
	}
	for _, f := range []string{certFile, keyFile} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s has perm %o, want 0600", f, perm)
		}
	}
}
