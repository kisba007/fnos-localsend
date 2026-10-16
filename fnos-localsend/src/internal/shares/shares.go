// Package shares provides read-only, path-confined access to configured
// host directories ("share roots"). All lookups clean the requested
// relative path, resolve symlinks, and verify the result still lives
// inside the (symlink-evaluated) root.
package shares

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnknownShare = errors.New("unknown share")
	ErrOutsideRoot  = errors.New("path escapes share root")
	ErrNotFound     = errors.New("no such file or directory")
	ErrIsDirectory  = errors.New("is a directory")
)

// Entry describes one file or directory within a share.
type Entry struct {
	Name    string    `json:"name"`
	Rel     string    `json:"rel"` // POSIX-style path relative to the share root
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Store holds share roots by name.
type Store struct {
	mu    sync.RWMutex
	roots map[string]string // name → absolute, symlink-evaluated path

	// HideEntry, when set, excludes matching basenames from List output
	// (e.g. NAS metadata files, in-flight receive partials).
	HideEntry func(name string) bool
}

// NASNoise reports whether name is a well-known NAS/OS metadata entry that
// clutters listings (@eaDir and #recycle on Synology, .DS_Store/Thumbs.db
// on macOS/Windows, …).
func NASNoise(name string) bool {
	switch name {
	case "@eaDir", "#recycle", "$RECYCLE.BIN", ".DS_Store", "Thumbs.db", "desktop.ini", ".SynologyWorkingDirectory":
		return true
	}
	return strings.HasPrefix(name, ".Trash-")
}

// NewStore validates and canonicalizes the roots map (name → path).
func NewStore(roots map[string]string) (*Store, error) {
	s := &Store{roots: map[string]string{}}
	for name, path := range roots {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("share %q: %w", name, err)
		}
		eval, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("share %q: %w", name, err)
		}
		fi, err := os.Stat(eval)
		if err != nil {
			return nil, fmt.Errorf("share %q: %w", name, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("share %q: %s is not a directory", name, eval)
		}
		s.roots[name] = eval
	}
	return s, nil
}

// Names returns share names in sorted order.
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.roots))
	for name := range s.roots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Path returns the canonicalized host path of a share root.
func (s *Store) Path(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.roots[name]
	return p, ok
}

// AddShare registers (or re-points) a share root at runtime, creating the
// directory if needed. Used to keep the "inbox" share in sync with the
// user-configurable receive directory.
func (s *Store) AddShare(name, path string) error {
	if name == "" || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("invalid share name %q", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("share %q: %w", name, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("share %q: create directory: %w", name, err)
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("share %q: %w", name, err)
	}
	fi, err := os.Stat(eval)
	if err != nil {
		return fmt.Errorf("share %q: %w", name, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("share %q: %s is not a directory", name, eval)
	}
	s.mu.Lock()
	s.roots[name] = eval
	s.mu.Unlock()
	return nil
}

// resolve maps (share, rel) to an absolute host path, rejecting anything
// that escapes the share root — via ".." elements or symlink hops.
func (s *Store) resolve(name, rel string) (string, error) {
	s.mu.RLock()
	root, ok := s.roots[name]
	s.mu.RUnlock()
	if !ok {
		return "", ErrUnknownShare
	}
	// Anchoring the clean at "/" neutralizes ".." and makes absolute
	// rel paths harmless: they stay rooted at the share.
	clean := strings.TrimPrefix(filepath.Clean("/"+rel), "/")
	joined := filepath.Join(root, clean)
	eval, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	if eval != root && !strings.HasPrefix(eval, root+string(os.PathSeparator)) {
		return "", ErrOutsideRoot
	}
	return eval, nil
}

func entry(name, rel string, fi fs.FileInfo) Entry {
	return Entry{
		Name:    name,
		Rel:     filepath.ToSlash(rel),
		IsDir:   fi.IsDir(),
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
	}
}

// List returns the entries of directory rel within share name,
// sorted directories-first then by name. Symlinks are listed as plain
// entries (lstat metadata); they are resolved only on Open.
func (s *Store) List(name, rel string) ([]Entry, error) {
	abs, err := s.resolve(name, rel)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s", errors.New("not a directory"), rel)
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		if s.HideEntry != nil && s.HideEntry(de.Name()) {
			continue
		}
		fi, err := de.Info() // lstat semantics: symlinks describe the link itself
		if err != nil {
			continue
		}
		entries = append(entries, entry(de.Name(), filepath.Join(rel, de.Name()), fi))
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// Open opens the file at (share, rel) for reading. Symlinks are resolved
// through the same confinement guard; directories are rejected.
func (s *Store) Open(name, rel string) (io.ReadSeekCloser, Entry, error) {
	abs, err := s.resolve(name, rel)
	if err != nil {
		return nil, Entry{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, Entry{}, err
	}
	if fi.IsDir() {
		return nil, Entry{}, ErrIsDirectory
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, Entry{}, err
	}
	return f, entry(fi.Name(), rel, fi), nil
}

// --- write path (used by the receive pipeline only) ---

// SanitizeElement validates a single filename element received from a
// sender: rejects empty, ".", "..", separators and control characters.
func SanitizeElement(name string) (string, error) {
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid name %q", name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("invalid name %q: contains path separator", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid name %q: control character", name)
		}
	}
	return name, nil
}

// ResolveForWrite maps (share, rel) to an absolute path at which a new
// file may be created: the parent directory must exist and is confined by
// the same guard as reads; the final element is sanitized and need not
// exist yet.
func (s *Store) ResolveForWrite(name, rel string) (string, error) {
	dir, base := filepath.Split(rel)
	if _, err := SanitizeElement(base); err != nil {
		return "", err
	}
	parent, err := s.resolve(name, dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, base), nil
}

// EnsureDir makes (share, relDir) exist, creating each missing element
// inside the confinement guard; existing elements must be directories and
// pass the symlink check. Returns the confined absolute path.
func (s *Store) EnsureDir(name, relDir string) (string, error) {
	root, ok := s.roots[name]
	if !ok {
		return "", ErrUnknownShare
	}
	cur := root
	rel := strings.TrimPrefix(filepath.Clean("/"+relDir), "/")
	if rel == "" || rel == "." {
		return cur, nil
	}
	for _, elem := range strings.Split(rel, "/") {
		if _, err := SanitizeElement(elem); err != nil {
			return "", err
		}
		candidate := filepath.Join(cur, elem)
		_, err := os.Lstat(candidate)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(candidate, 0o755); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		default:
			// Exists: must be a directory (resolving symlinks inside root).
			eval, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			if eval != root && !strings.HasPrefix(eval, root+string(os.PathSeparator)) {
				return "", ErrOutsideRoot
			}
			if efi, err := os.Stat(eval); err != nil || !efi.IsDir() {
				return "", fmt.Errorf("%s is not a directory", elem)
			}
		}
		cur = candidate
	}
	return cur, nil
}
