package shares

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeElement(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b", "a\nb"} {
		if _, err := SanitizeElement(bad); err == nil {
			t.Fatalf("SanitizeElement(%q) accepted", bad)
		}
	}
	for _, good := range []string{"a.txt", "my file (2).mkv", "日本語.txt"} {
		if _, err := SanitizeElement(good); err != nil {
			t.Fatalf("SanitizeElement(%q) rejected: %v", good, err)
		}
	}
}

func TestResolveForWrite(t *testing.T) {
	st, _ := fixture(t)

	// Legit new file under an existing subdir.
	p, err := st.ResolveForWrite("test", "sub/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "new.txt" {
		t.Fatalf("bad path %q", p)
	}

	// Parent escapes root through a symlink → rejected.
	if _, err := st.ResolveForWrite("test", "evil/new.txt"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("err=%v, want ErrOutsideRoot", err)
	}

	// Bad final element.
	if _, err := st.ResolveForWrite("test", "sub/.."); err == nil {
		t.Fatal("accepted '..' as filename")
	}

	// Unknown share.
	if _, err := st.ResolveForWrite("nope", "x.txt"); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("err=%v, want ErrUnknownShare", err)
	}
}

func TestEnsureDir(t *testing.T) {
	st, _ := fixture(t)

	abs, err := st.EnsureDir("test", "newdir/nested")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		t.Fatalf("dir not created at %q: %v", abs, err)
	}

	// Through an escaping symlink → rejected.
	if _, err := st.EnsureDir("test", "evil/deeper"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("err=%v, want ErrOutsideRoot", err)
	}

	// Existing dir is fine; existing file as element is not.
	if _, err := st.EnsureDir("test", "sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureDir("test", "a.txt/under"); err == nil {
		t.Fatal("file-as-dir accepted")
	}
}
