package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsOnFirstStart(t *testing.T) {
	st, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.AcceptTimeoutSec != 30 || got.DropboxShare != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestUpdatePersistsAndClamps(t *testing.T) {
	dir := t.TempDir()
	st, _ := Load(dir)
	got, err := st.Update(Settings{AcceptTimeoutSec: 9999, DropboxShare: "incoming"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptTimeoutSec != 300 {
		t.Fatalf("not clamped: %+v", got)
	}
	st2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Get(); got.AcceptTimeoutSec != 300 || got.DropboxShare != "incoming" {
		t.Fatalf("not persisted: %+v", got)
	}
	fi, _ := os.Stat(filepath.Join(dir, fileName))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %o, want 0600", fi.Mode().Perm())
	}
}

func TestCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, fileName), []byte("{nope"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error on corrupt settings.json")
	}
}
