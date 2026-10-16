package transfer

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexindigo/localsend-nas/internal/discovery"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/receive"
	"github.com/alexindigo/localsend-nas/internal/shares"
)

func testStore(t *testing.T) *shares.Store {
	t.Helper()
	root := t.TempDir()
	// root: a.txt ("hi"), dir/{b.txt,c.txt}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, "dir", f), []byte("1234"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := shares.NewStore(map[string]string{"test": root})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func testManager(t *testing.T) *Manager {
	return New(testStore(t), nil, nil, localsend.Info{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestExpandSingleFile(t *testing.T) {
	m := testManager(t)
	files, total, err := m.expand([]ItemRef{{Share: "test", Rel: "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "a.txt" || files[0].Size != 2 || total != 2 {
		t.Fatalf("unexpected: %+v total=%d", files, total)
	}
	if files[0].Share != "test" || files[0].Rel != "a.txt" {
		t.Fatalf("source not tracked: %+v", files[0])
	}
}

func TestExpandDirRecursively(t *testing.T) {
	m := testManager(t)
	files, total, err := m.expand([]ItemRef{{Share: "test", Rel: "dir"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || total != 8 {
		t.Fatalf("unexpected: %+v total=%d", files, total)
	}
	// Expanded files carry the top dir name as POSIX prefix.
	for _, f := range files {
		if f.Name != "dir/"+f.Rel[len("dir/"):] {
			t.Fatalf("bad receiver-visible name: %+v", f)
		}
	}
	names := []string{files[0].Name, files[1].Name}
	if names[0] != "dir/b.txt" || names[1] != "dir/c.txt" {
		t.Fatalf("unexpected names: %v", names)
	}
}

func TestExpandShareRootHasNoSyntheticDir(t *testing.T) {
	m := testManager(t)
	files, total, err := m.expand([]ItemRef{{Share: "test", Rel: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || total != 10 {
		t.Fatalf("unexpected: %+v total=%d", files, total)
	}
	// List sorts dirs-first, so order is dir/b.txt, dir/c.txt, a.txt.
	// The root-level file must not gain a synthetic directory prefix.
	if files[2].Name != "a.txt" || files[2].Rel != "a.txt" {
		t.Fatalf("root file got a prefix: %+v", files[2])
	}
}

func TestExpandRejectsEscape(t *testing.T) {
	m := testManager(t)
	if _, _, err := m.expand([]ItemRef{{Share: "test", Rel: "../../etc/passwd"}}); err == nil {
		t.Fatal("escape not rejected")
	}
	if _, _, err := m.expand([]ItemRef{{Share: "nope", Rel: "a.txt"}}); err == nil {
		t.Fatal("unknown share not rejected")
	}
}

func TestMimeType(t *testing.T) {
	if got := mimeType("movie.mp4"); got != "video/mp4" {
		t.Fatalf("mp4: %q", got)
	}
	if got := mimeType("archive.tar.gz"); got != "application/gzip" {
		t.Fatalf("tar.gz: %q", got)
	}
	if got := mimeType("noext"); got != "application/octet-stream" {
		t.Fatalf("noext: %q", got)
	}
}

func TestReceiveHooksIntegration(t *testing.T) {
	m := testManager(t)
	sess := &receive.Session{
		ID:        "s1",
		Sender:    localsend.Info{Alias: "phone", Fingerprint: "fp1"},
		State:     "pending",
		CreatedAt: time.Now(),
		Files: map[string]*receive.File{
			"f0": {DTO: localsend.FileDTO{ID: "f0", FileName: "a.txt", Size: 5}},
		},
	}
	m.ReceiveRegistered(sess)
	jobs := m.List()
	if len(jobs) != 1 || jobs[0].Direction != "receive" || jobs[0].State != StateAwaitingAccept || jobs[0].Total != 5 {
		t.Fatalf("bad job after register: %+v", jobs)
	}
	m.ReceiveState("s1", "accepted", "")
	m.ReceiveProgress("s1", "f0", 5)
	m.ReceiveFileDone("s1", "f0")
	m.ReceiveState("s1", "done", "")
	j := m.List()[0]
	if j.State != StateDone || j.Sent != 5 || !j.Files[0].Done {
		t.Fatalf("bad final job: %+v", j)
	}
	if ok, removed := m.Forget("s1"); !ok || !removed {
		t.Fatal("terminal receive job must be forgettable")
	}
}

var _ = discovery.Device{} // Resolver interface anchor
