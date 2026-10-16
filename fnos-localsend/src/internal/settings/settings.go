// Package settings persists runtime-adjustable preferences in
// <data-dir>/settings.json, shared by all browsers via the HTTP API.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	fileName = "settings.json"

	DefaultAcceptTimeoutSec = 30
	minAcceptTimeoutSec     = 5
	maxAcceptTimeoutSec     = 300
)

// Settings are the runtime-adjustable knobs (settings menu in the UI).
type Settings struct {
	// AcceptTimeoutSec is the receive-dialog countdown length.
	AcceptTimeoutSec int `json:"acceptTimeoutSec"`
	// DropboxShare names the share that receives auto-accepted files when
	// the countdown expires. Empty = dropbox off = timeout rejects.
	DropboxShare string `json:"dropboxShare"`
	// ShowNasNoise reveals NAS/OS metadata entries (@eaDir, .DS_Store, …).
	// Inverted so the zero value means hidden — also on upgraded installs
	// whose settings.json predates the field.
	ShowNasNoise bool `json:"showNasNoise"`
	// ReceiveDir is the absolute directory where received files are saved.
	// Empty = fall back to the share picked in the receive dialog. When set,
	// it is also registered as the "inbox" share so saved files are
	// browsable in the Files tab.
	ReceiveDir string `json:"receiveDir"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// Load reads <dir>/settings.json, falling back to defaults on first start.
func Load(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, fileName), s: Settings{AcceptTimeoutSec: DefaultAcceptTimeoutSec}}
	data, err := os.ReadFile(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", st.path, err)
	}
	st.s = clamp(s)
	return st, nil
}

func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.s
}

// Update validates (clamps), persists, and returns the effective settings.
func (s *Store) Update(in Settings) (Settings, error) {
	in = clamp(in)
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	s.s = in
	s.mu.Unlock()
	return in, nil
}

func clamp(s Settings) Settings {
	if s.AcceptTimeoutSec < minAcceptTimeoutSec {
		s.AcceptTimeoutSec = minAcceptTimeoutSec
	}
	if s.AcceptTimeoutSec > maxAcceptTimeoutSec {
		s.AcceptTimeoutSec = maxAcceptTimeoutSec
	}
	return s
}
