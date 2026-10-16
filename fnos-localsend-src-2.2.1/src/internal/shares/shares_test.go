package shares

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fixture builds:
//
//	root/
//	  a.txt            "hello"
//	  sub/nested.txt   "nested"
//	  inside -> sub    (symlink staying within the root)
//	  evil -> outside  (symlink escaping the root)
//	outside/
//	  secret.txt       "secret"
func fixture(t *testing.T) (*Store, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "a.txt"):          "hello",
		filepath.Join(root, "sub/nested.txt"): "nested",
		filepath.Join(outside, "secret.txt"):  "secret",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("sub", filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(map[string]string{"test": root})
	if err != nil {
		t.Fatal(err)
	}
	return st, outside
}

func TestListRoot(t *testing.T) {
	st, _ := fixture(t)
	entries, err := st.List("test", "")
	if err != nil {
		t.Fatal(err)
	}
	// Dirs first: sub, then files/links by name: a.txt, evil, inside.
	if len(entries) != 4 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	if !entries[0].IsDir || entries[0].Name != "sub" {
		t.Fatalf("dirs must sort first: %+v", entries[0])
	}
	if entries[1].Name != "a.txt" {
		t.Fatalf("unexpected order: %+v", entries)
	}
}

func TestOpenLegitNested(t *testing.T) {
	st, _ := fixture(t)
	rc, e, err := st.Open("test", "sub/nested.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "nested" || e.Size != 6 {
		t.Fatalf("got %q (%+v)", b, e)
	}
}

func TestTraversalRejected(t *testing.T) {
	st, outside := fixture(t)
	// Every attack must fail closed: either confined-and-nonexistent
	// (ErrNotFound) or explicitly caught escaping (ErrOutsideRoot).
	// "Clean is confinement" alone is NOT the assertion — the payload
	// file must never be readable.
	attacks := []string{
		"../outside/secret.txt",
		"sub/../../outside/secret.txt",
		filepath.Join("..", filepath.Base(outside), "secret.txt"),
	}
	for _, rel := range attacks {
		if _, err := st.List("test", rel); err == nil {
			t.Fatalf("List(%q) succeeded, want rejection", rel)
		}
		rc, _, err := st.Open("test", rel)
		if err == nil {
			rc.Close()
			t.Fatalf("Open(%q) succeeded, want rejection", rel)
		}
	}
	// Pure ".." walks collapse onto the root itself (confinement-preserved),
	// so List succeeds but must only ever show the root's own contents.
	entries, err := st.List("test", "../../..")
	if err != nil {
		t.Fatalf("List(../.. walk collapsing to root) err=%v", err)
	}
	for _, e := range entries {
		if e.Name == filepath.Base(outside) {
			t.Fatalf("escape: listing contains host path entry %+v", e)
		}
	}
}

func TestAbsolutePathConfined(t *testing.T) {
	st, outside := fixture(t)
	// An absolute-looking rel is re-rooted into the share: it must never
	// reach the real host path.
	abs := filepath.Join(outside, "secret.txt")
	if _, _, err := st.Open("test", abs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open(%q) err=%v, want ErrNotFound", abs, err)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	st, _ := fixture(t)
	if _, _, err := st.Open("test", "evil/secret.txt"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Open(evil/secret.txt) err=%v, want ErrOutsideRoot", err)
	}
	if _, err := st.List("test", "evil"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("List(evil) err=%v, want ErrOutsideRoot", err)
	}
}

func TestSymlinkInsideAllowed(t *testing.T) {
	st, _ := fixture(t)
	rc, _, err := st.Open("test", "inside/nested.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "nested" {
		t.Fatalf("got %q", b)
	}
}

func TestOpenDirectoryRejected(t *testing.T) {
	st, _ := fixture(t)
	if _, _, err := st.Open("test", "sub"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("err=%v, want ErrIsDirectory", err)
	}
}

func TestHideEntryFilter(t *testing.T) {
	st, _ := fixture(t)
	os.WriteFile(filepath.Join(st.roots["test"], ".DS_Store"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(st.roots["test"], "@eaDir"), 0o755)
	st.HideEntry = func(name string) bool { return NASNoise(name) }
	entries, err := st.List("test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == ".DS_Store" || e.Name == "@eaDir" {
			t.Fatalf("noise entry not filtered: %+v", e)
		}
	}
	st.HideEntry = nil
	entries, _ = st.List("test", "")
	found := false
	for _, e := range entries {
		if e.Name == ".DS_Store" {
			found = true
		}
	}
	if !found {
		t.Fatal("filter disabled but .DS_Store still hidden")
	}
}

func TestUnknownShare(t *testing.T) {
	st, _ := fixture(t)
	if _, err := st.List("nope", ""); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("err=%v, want ErrUnknownShare", err)
	}
}
