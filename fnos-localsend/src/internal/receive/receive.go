// Package receive implements inbound LocalSend transfers: pending sessions
// with a user-decision countdown (dropbox auto-accept or decline on
// timeout), token-guarded streaming uploads confined to share roots, and
// cancel cleanup.
package receive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/settings"
	"github.com/alexindigo/localsend-nas/internal/shares"
)

// Session states.
const (
	StatePending   = "pending"  // waiting for user decision / countdown
	StateAccepted  = "accepted" // uploads in flight
	StateDone      = "done"
	StateDeclined  = "declined"
	StateCancelled = "cancelled"
	StateFailed    = "failed"
)

// File tracks one inbound file.
type File struct {
	DTO      localsend.FileDTO
	Received int64
	Done     bool

	partPath string // current partial on disk (deleted on cancel/fail)
}

// Session is one inbound transfer.
type Session struct {
	ID        string
	Sender    localsend.Info
	SenderIP  string
	Files     map[string]*File
	State     string
	DestShare string // chosen at accept time
	CreatedAt time.Time

	tokens   map[string]string // fileID → token
	decision chan decision
	cancel   context.CancelFunc
}

type decision struct {
	accept bool
	share  string
}

// Hooks let the transfer UI track receive sessions.
type Hooks interface {
	ReceiveRegistered(s *Session)
	ReceiveState(id, state, errMsg string)
	ReceiveProgress(id, fileID string, delta int64)
	ReceiveFileDone(id, fileID string)
}

// HooksFunc adapts the no-op default.
type nopHooks struct{}

func (nopHooks) ReceiveRegistered(*Session)            {}
func (nopHooks) ReceiveState(string, string, string)   {}
func (nopHooks) ReceiveProgress(string, string, int64) {}
func (nopHooks) ReceiveFileDone(string, string)        {}

type Manager struct {
	store    *shares.Store
	settings *settings.Store
	hooks    Hooks
	log      *slog.Logger

	mu         sync.Mutex
	sessions   map[string]*Session
	inProgress map[string]string // final dest path → sessionID (dedupe guard)
}

func New(store *shares.Store, st *settings.Store, hooks Hooks, log *slog.Logger) *Manager {
	if hooks == nil {
		hooks = nopHooks{}
	}
	return &Manager{
		store:      store,
		settings:   st,
		hooks:      hooks,
		log:        log,
		sessions:   map[string]*Session{},
		inProgress: map[string]string{},
	}
}

// Pending returns sessions awaiting a decision (for the UI).
func (m *Manager) Pending() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Session
	for _, s := range m.sessions {
		if s.State == StatePending {
			out = append(out, s)
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Prepare runs the decision flow for an inbound prepare-upload and returns
// the HTTP status and (on accept) the token map to answer with.
// It blocks until the user decides or the countdown expires.
func (m *Manager) Prepare(ctx context.Context, sender localsend.Info, senderIP, certFP string, files map[string]localsend.FileDTO) (int, *localsend.PrepareResponse, error) {
	// Protocol v2 security context: the claimed fingerprint must match the
	// presented client certificate (same rule official receivers apply).
	if certFP != "" && !strings.EqualFold(sender.Fingerprint, certFP) {
		return http.StatusForbidden, nil, fmt.Errorf("claimed fingerprint does not match the client certificate")
	}
	if len(files) == 0 {
		return http.StatusBadRequest, nil, fmt.Errorf("no files offered")
	}
	// Pre-validate filenames before bothering the user.
	sessFiles := map[string]*File{}
	for id, dto := range files {
		if dto.ID == "" {
			dto.ID = id
		}
		if _, err := sanitizeRelPath(dto.FileName); err != nil {
			return http.StatusBadRequest, nil, fmt.Errorf("file %q: %w", dto.FileName, err)
		}
		sessFiles[id] = &File{DTO: dto}
	}

	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		ID:        newID(),
		Sender:    sender,
		SenderIP:  senderIP,
		Files:     sessFiles,
		State:     StatePending,
		CreatedAt: time.Now(),
		decision:  make(chan decision, 1),
		cancel:    cancel,
	}
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	m.hooks.ReceiveRegistered(sess)
	m.log.Info("incoming transfer offer", "from", sender.Alias, "ip", senderIP, "files", len(files))

	// Countdown: dropbox auto-accept or decline.
	timeout := time.Duration(m.settings.Get().AcceptTimeoutSec) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var dec decision
	select {
	case dec = <-sess.decision:
	case <-timer.C:
		if share := m.dropboxShare(); share != "" {
			dec = decision{accept: true, share: share}
			m.log.Info("countdown expired: auto-accepting to dropbox", "share", share, "session", sess.ID)
		} else {
			dec = decision{}
			m.log.Info("countdown expired: declining", "session", sess.ID)
		}
	case <-sessCtx.Done(): // sender cancelled while pending
		m.setState(sess, StateCancelled, "")
		return http.StatusNoContent, nil, nil
	case <-ctx.Done(): // sender disconnected
		m.setState(sess, StateCancelled, "")
		return 0, nil, ctx.Err()
	}

	if !dec.accept {
		m.setState(sess, StateDeclined, "")
		return http.StatusForbidden, nil, fmt.Errorf("localsend-nas user declined the transfer")
	}
	if _, ok := m.store.Path(dec.share); !ok {
		m.setState(sess, StateFailed, "destination share vanished")
		return http.StatusInternalServerError, nil, fmt.Errorf("destination share %q not configured", dec.share)
	}

	m.mu.Lock()
	sess.DestShare = dec.share
	sess.tokens = map[string]string{}
	for id := range sess.Files {
		sess.tokens[id] = newID()
	}
	m.mu.Unlock()
	m.setState(sess, StateAccepted, "")

	resp := &localsend.PrepareResponse{SessionID: sess.ID, Files: map[string]string{}}
	for id, tok := range sess.tokens {
		resp.Files[id] = tok
	}
	return http.StatusOK, resp, nil
}

// dropboxShare returns the configured dropbox share if it currently exists.
func (m *Manager) dropboxShare() string {
	share := m.settings.Get().DropboxShare
	if share == "" {
		return ""
	}
	if _, ok := m.store.Path(share); !ok {
		m.log.Warn("dropbox share not configured; declining on timeout", "share", share)
		return ""
	}
	return share
}

// Decide answers a pending session from the UI.
func (m *Manager) Decide(sessionID string, accept bool, share string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown session")
	}
	if sess.State != StatePending {
		return fmt.Errorf("session not pending")
	}
	if accept {
		if _, ok := m.store.Path(share); !ok {
			return fmt.Errorf("unknown share %q", share)
		}
	}
	select {
	case sess.decision <- decision{accept: accept, share: share}:
	default:
	}
	return nil
}

// Upload streams one file of an accepted session to disk.
func (m *Manager) Upload(ctx context.Context, sessionID, fileID, token string, r io.Reader) (int, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok || (sess.State != StateAccepted) {
		return http.StatusNotFound, fmt.Errorf("unknown or inactive session")
	}
	if sess.tokens[fileID] == "" || sess.tokens[fileID] != token {
		return http.StatusForbidden, fmt.Errorf("invalid token")
	}
	f := sess.Files[fileID]
	if f == nil {
		return http.StatusNotFound, fmt.Errorf("unknown fileId")
	}
	if f.Done {
		return http.StatusConflict, fmt.Errorf("file already received")
	}

	rel, err := sanitizeRelPath(f.DTO.FileName)
	if err != nil {
		return http.StatusBadRequest, err
	}
	dir, base := filepath.Split(rel)
	destDir, err := m.store.EnsureDir(sess.DestShare, strings.TrimSuffix(dir, "/"))
	if err != nil {
		m.fail(sess, err)
		return http.StatusForbidden, err
	}
	final, err := m.claimFinalPath(sess.ID, destDir, base)
	if err != nil {
		m.fail(sess, err)
		return http.StatusInternalServerError, err
	}
	part := final + ".lnas-part"

	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		m.fail(sess, err)
		return http.StatusInternalServerError, err
	}
	f.partPath = part

	written, copyErr := m.streamWithProgress(sess, fileID, out, io.LimitReader(r, f.DTO.Size+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		m.failFile(sess, f, copyErr)
		return http.StatusInternalServerError, copyErr
	case closeErr != nil:
		m.failFile(sess, f, closeErr)
		return http.StatusInternalServerError, closeErr
	case written != f.DTO.Size:
		err := fmt.Errorf("size mismatch: declared %d, got %d", f.DTO.Size, written)
		m.failFile(sess, f, err)
		return http.StatusBadRequest, err
	}

	if err := os.Rename(part, final); err != nil {
		m.failFile(sess, f, err)
		return http.StatusInternalServerError, err
	}
	m.mu.Lock()
	f.Done = true
	delete(m.inProgress, final)
	allDone := true
	for _, other := range sess.Files {
		if !other.Done {
			allDone = false
			break
		}
	}
	m.mu.Unlock()
	m.hooks.ReceiveFileDone(sess.ID, fileID)
	m.log.Info("received file", "session", sess.ID, "name", f.DTO.FileName, "to", final)
	if allDone {
		m.setState(sess, StateDone, "")
		m.log.Info("receive complete", "session", sess.ID, "from", sess.Sender.Alias, "share", sess.DestShare)
	}
	return http.StatusOK, nil
}

func (m *Manager) streamWithProgress(sess *Session, fileID string, w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, 256*1024)
	var total int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			total += int64(wn)
			m.hooks.ReceiveProgress(sess.ID, fileID, int64(wn))
			if werr != nil {
				return total, werr
			}
			// sender cancelled mid-upload?
			if sess.State == StateCancelled {
				return total, context.Canceled
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return total, nil
			}
			return total, rerr
		}
	}
}

// CancelSession handles the sender's /cancel (or our UI cancel): abort and
// delete partials. Idempotent — safe to call on terminal sessions.
func (m *Manager) CancelSession(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return
	}
	if sess.State == StatePending || sess.State == StateAccepted {
		sess.cancel() // unblocks Prepare's select (pending) or stream loop
		m.cleanupPartials(sess)
		m.setState(sess, StateCancelled, "")
		m.log.Info("receive cancelled", "session", sessionID)
	}
}

func (m *Manager) cleanupPartials(sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range sess.Files {
		if f.partPath != "" && !f.Done {
			os.Remove(f.partPath)
		}
	}
}

func (m *Manager) fail(sess *Session, err error) {
	m.cleanupPartials(sess)
	m.setState(sess, StateFailed, err.Error())
}

func (m *Manager) failFile(sess *Session, f *File, err error) {
	m.mu.Lock()
	if f.partPath != "" {
		os.Remove(f.partPath)
		f.partPath = ""
	}
	m.mu.Unlock()
	m.fail(sess, err)
}

func (m *Manager) setState(sess *Session, state, errMsg string) {
	m.mu.Lock()
	sess.State = state
	m.mu.Unlock()
	m.hooks.ReceiveState(sess.ID, state, errMsg)
}

// claimFinalPath reserves a unique destination name: "name (1).ext"…
func (m *Manager) claimFinalPath(sessionID, dir, base string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		candidate := filepath.Join(dir, name)
		_, taken := m.inProgress[candidate]
		_, err := os.Lstat(candidate)
		exists := err == nil
		if !taken && !exists {
			m.inProgress[candidate] = sessionID
			return candidate, nil
		}
	}
}

// sanitizeRelPath validates a sender-provided relative path element-wise.
func sanitizeRelPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty file name")
	}
	// Sender names use POSIX separators.
	name = strings.ReplaceAll(name, "\\", "/")
	parts := strings.Split(name, "/")
	for i, p := range parts {
		s, err := shares.SanitizeElement(p)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return filepath.Join(parts...), nil
}
