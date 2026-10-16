// Package transfer runs basket→session send jobs: prepare-upload against
// the target device, then sequential per-file streaming uploads with
// progress events and cancellation. One active job per target; further
// jobs queue.
package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alexindigo/localsend-nas/internal/discovery"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/receive"
	"github.com/alexindigo/localsend-nas/internal/shares"
)

// Job states.
const (
	StateQueued         = "queued"
	StatePreparing      = "preparing"
	StateAwaitingAccept = "awaiting-accept"
	StateSending        = "sending"
	StateReceiving      = "receiving"
	StateDone           = "done"
	StateFailed         = "failed"
	StateCancelled      = "cancelled"
	StateDeclined       = "declined"
)

// ItemRef is one basket item: a file or directory within a share.
type ItemRef struct {
	Share string `json:"share"`
	Rel   string `json:"rel"`
}

// FileProgress tracks one expanded file within a job.
type FileProgress struct {
	ID    string `json:"id"`
	Name  string `json:"name"` // receiver-visible POSIX name (may include dir prefix)
	Share string `json:"-"`    // source share
	Rel   string `json:"-"`    // source relative path
	Size  int64  `json:"size"`
	Sent  int64  `json:"sent"`
	Done  bool   `json:"done"`
}

// Job is one send batch to one target, or one inbound receive session.
type Job struct {
	ID        string         `json:"id"`
	Direction string         `json:"direction"` // "send" | "receive"
	TargetFP  string         `json:"target"`
	Alias     string         `json:"alias"`
	State     string         `json:"state"`
	Error     string         `json:"error,omitempty"`
	Files     []FileProgress `json:"files"`
	Total     int64          `json:"total"`
	Sent      int64          `json:"sent"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Resolver resolves device fingerprints to connection info.
type Resolver interface {
	Get(fingerprint string) (discovery.Device, bool)
}

type Manager struct {
	store    *shares.Store
	resolver Resolver
	client   *localsend.Client
	info     localsend.Info
	log      *slog.Logger

	mu            sync.Mutex
	jobs          map[string]*job
	order         []string // job IDs, creation order
	queues        map[string]chan *job
	cancelReceive func(id string) // routes cancel to the receive pipeline
	globalNotify  func(Job)       // fan-out for UI popups (incoming offers)
}

type subscriber struct {
	ch chan Job
}

type job struct {
	Job
	cancel   context.CancelFunc
	subs     map[*subscriber]struct{}
	throttle *progressThrottle
}

// New builds a Manager. info is our own LocalSend info DTO.
func New(store *shares.Store, resolver Resolver, client *localsend.Client, info localsend.Info, log *slog.Logger) *Manager {
	return &Manager{
		store:    store,
		resolver: resolver,
		client:   client,
		info:     info,
		log:      log,
		jobs:     map[string]*job{},
		queues:   map[string]chan *job{},
	}
}

// Send validates and enqueues a batch for the target; the job runs
// asynchronously. Returns the job ID.
func (m *Manager) Send(targetFP string, items []ItemRef) (string, error) {
	dev, ok := m.resolver.Get(targetFP)
	if !ok {
		return "", fmt.Errorf("unknown target %q", targetFP)
	}
	if len(items) == 0 {
		return "", errors.New("empty basket")
	}
	files, total, err := m.expand(items)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", errors.New("basket contains no files")
	}

	id, err := newID()
	if err != nil {
		return "", err
	}
	j := &job{subs: map[*subscriber]struct{}{}}
	j.ID = id
	j.TargetFP = targetFP
	j.Alias = dev.Info.Alias
	j.State = StateQueued
	j.Files = files
	j.Total = total
	j.CreatedAt = time.Now()

	m.mu.Lock()
	m.jobs[id] = j
	m.order = append(m.order, id)
	q, ok := m.queues[targetFP]
	if !ok {
		q = make(chan *job, 32)
		m.queues[targetFP] = q
		go m.targetWorker(targetFP, q)
	}
	m.mu.Unlock()

	select {
	case q <- j:
	default:
		m.mu.Lock()
		j.State = StateFailed
		j.Error = "target queue full"
		m.mu.Unlock()
		return id, fmt.Errorf("target queue full")
	}
	return id, nil
}

// SetReceiveCancel wires the receive pipeline's cancel (job ID == session ID).
func (m *Manager) SetReceiveCancel(fn func(string)) { m.cancelReceive = fn }

// SetGlobalNotify wires fan-out for UI popups on incoming offers.
func (m *Manager) SetGlobalNotify(fn func(Job)) { m.globalNotify = fn }

// List returns all jobs, newest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, m.jobs[m.order[i]].snapshot())
	}
	return out
}

// Cancel requests cancellation of a queued or running job.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	if j.Direction == "receive" {
		m.mu.Unlock()
		if m.cancelReceive != nil {
			m.cancelReceive(id) // state flows back via ReceiveState hook
			return true
		}
		return false
	}
	terminal := j.State == StateDone || j.State == StateFailed || j.State == StateCancelled
	cancel := j.cancel
	if terminal {
		m.mu.Unlock()
		return false
	}
	// Mark cancelled immediately (covers queued and running jobs); the
	// worker skips or unwinds into StateCancelled via fail().
	j.State = StateCancelled
	m.broadcastLocked(j)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// Forget removes a terminal (done/failed/cancelled) job from the list.
// Returns (found, removed); active jobs report (true, false).
func (m *Manager) Forget(id string) (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return false, false
	}
	if j.State != StateDone && j.State != StateFailed && j.State != StateCancelled && j.State != StateDeclined {
		return true, false
	}
	delete(m.jobs, id)
	for i, jid := range m.order {
		if jid == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return true, true
}

// Subscribe registers for job updates. The current snapshot is delivered
// immediately; further updates until the returned unsubscribe is called.
func (m *Manager) Subscribe(id string) (<-chan Job, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, nil, false
	}
	sub := &subscriber{ch: make(chan Job, 32)}
	j.subs[sub] = struct{}{}
	sub.ch <- j.snapshot()
	return sub.ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := j.subs[sub]; ok {
			delete(j.subs, sub)
			close(sub.ch)
		}
	}, true
}

// --- worker ---

func (m *Manager) targetWorker(targetFP string, q chan *job) {
	for j := range q {
		m.run(targetFP, j)
	} // runs for process lifetime; Send spawns one worker per target
}

func (m *Manager) run(targetFP string, j *job) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if j.State == StateCancelled { // cancelled while queued
		m.mu.Unlock()
		cancel()
		return
	}
	j.cancel = cancel
	m.mu.Unlock()
	defer cancel()

	dev, _ := m.resolver.Get(targetFP)
	host, port := dev.IP, dev.Info.Port

	// Prepare (blocks on the receiver's accept prompt).
	m.setState(j, StatePreparing)
	preq := localsend.PrepareRequest{Info: m.info, Files: map[string]localsend.FileDTO{}}
	for _, f := range j.Files {
		preq.Files[f.ID] = localsend.FileDTO{
			ID:       f.ID,
			FileName: f.Name,
			Size:     f.Size,
			FileType: mimeType(f.Name),
		}
	}
	m.setState(j, StateAwaitingAccept)
	prep, status, err := m.client.Prepare(ctx, host, port, preq)
	if err != nil {
		m.fail(j, err)
		return
	}
	if status == 204 {
		m.setState(j, StateDone) // receiver needs nothing
		return
	}

	// Upload sequentially; per-file tokens come from the prepare response.
	m.setState(j, StateSending)
	throttle := &progressThrottle{last: time.Now(), emit: func() { m.broadcast(j) }}
	for i := range j.Files {
		f := &j.Files[i]
		token, ok := prep.Files[f.ID]
		if !ok {
			continue // receiver declined this file
		}
		if err := m.uploadOne(ctx, j, f, host, port, prep.SessionID, token, throttle); err != nil {
			m.fail(j, err)
			return
		}
		m.mu.Lock()
		f.Done = true
		m.mu.Unlock()
		m.broadcast(j)
	}
	m.setState(j, StateDone)
	m.log.Info("transfer complete", "job", j.ID, "target", j.Alias, "files", len(j.Files), "bytes", j.Total)
}

func (m *Manager) uploadOne(ctx context.Context, j *job, f *FileProgress, host string, port int, sessionID, token string, throttle *progressThrottle) error {
	rc, _, err := m.store.Open(f.Share, f.Rel)
	if err != nil {
		return fmt.Errorf("open %s:%s: %w", f.Share, f.Rel, err)
	}
	defer rc.Close()
	onProgress := func(delta int64) {
		m.mu.Lock()
		f.Sent += delta
		j.Sent += delta
		m.mu.Unlock()
		throttle.maybe()
	}
	return m.client.Upload(ctx, host, port, sessionID, f.ID, token, f.Size, rc, onProgress)
}

// --- state / events ---

func (m *Manager) setState(j *job, state string) {
	m.mu.Lock()
	j.State = state
	m.mu.Unlock()
	m.broadcast(j)
}

func (m *Manager) fail(j *job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.State == StateCancelled || errors.Is(err, context.Canceled) {
		j.State = StateCancelled
		j.Error = ""
	} else {
		j.State = StateFailed
		j.Error = err.Error()
	}
	m.log.Warn("transfer ended", "job", j.ID, "state", j.State, "error", err)
	m.broadcastLocked(j)
	// Best-effort protocol cancel: no session ID tracked in v1; the
	// receiver expires half-open sessions on its own.
}

func (m *Manager) broadcast(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastLocked(j)
}

func (m *Manager) broadcastLocked(j *job) {
	snap := j.snapshot()
	for sub := range j.subs {
		select {
		case sub.ch <- snap:
		default: // slow consumer; snapshots are idempotent
		}
	}
}

func (j *job) snapshot() Job {
	cp := j.Job
	cp.Files = make([]FileProgress, len(j.Files))
	copy(cp.Files, j.Files)
	return cp
}

// --- expansion ---

// expand resolves basket items to flat file lists. Directories expand
// recursively; expanded files carry the top directory's name as a POSIX
// path prefix so receivers recreate the structure.
func (m *Manager) expand(items []ItemRef) ([]FileProgress, int64, error) {
	var files []FileProgress
	var total int64
	n := 0
	for _, item := range items {
		if item.Share == "" {
			return nil, 0, errors.New("item missing share")
		}
		err := m.expandItem(item, "", &files, &total, &n)
		if err != nil {
			return nil, 0, err
		}
	}
	return files, total, nil
}

func (m *Manager) expandItem(item ItemRef, prefix string, files *[]FileProgress, total *int64, n *int) error {
	entries, listErr := m.store.List(item.Share, item.Rel)
	if listErr != nil {
		// Not a listable directory — treat as file.
		rc, e, err := m.store.Open(item.Share, item.Rel)
		if err != nil {
			return fmt.Errorf("%s:%s: %w", item.Share, item.Rel, err)
		}
		rc.Close()
		m.addFile(files, total, n, joinName(prefix, e.Name), item, e.Size)
		return nil
	}

	// Directory: recurse with the dir's own name as prefix.
	dirPrefix := prefix
	if item.Rel != "" { // share root itself gets no synthetic top dir
		dirPrefix = joinName(prefix, filepath.Base(filepath.Clean("/"+item.Rel)))
	}
	for _, e := range entries {
		child := ItemRef{Share: item.Share, Rel: e.Rel}
		if e.IsDir {
			if err := m.expandItem(child, dirPrefix, files, total, n); err != nil {
				return err
			}
			continue
		}
		m.addFile(files, total, n, joinName(dirPrefix, e.Name), child, e.Size)
	}
	return nil
}

func (m *Manager) addFile(files *[]FileProgress, total *int64, n *int, name string, src ItemRef, size int64) {
	*files = append(*files, FileProgress{ID: fileID(*n), Name: name, Share: src.Share, Rel: src.Rel, Size: size})
	*total += size
	*n++
}

func joinName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func fileID(n int) string { return fmt.Sprintf("f%d", n) }

func mimeType(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// progressThrottle limits SSE churn on large uploads.
type progressThrottle struct {
	mu   sync.Mutex
	last time.Time
	emit func()
}

func (t *progressThrottle) maybe() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.last) < 100*time.Millisecond {
		return
	}
	t.last = time.Now()
	t.emit()
}

// --- receive.Hooks implementation (inbound sessions ride the job list) ---

func (m *Manager) ReceiveRegistered(s *receive.Session) {
	j := &job{subs: map[*subscriber]struct{}{}}
	j.ID = s.ID
	j.Direction = "receive"
	j.TargetFP = s.Sender.Fingerprint
	j.Alias = s.Sender.Alias
	j.State = StateAwaitingAccept
	j.CreatedAt = s.CreatedAt
	files := make([]*receive.File, 0, len(s.Files))
	for _, f := range s.Files {
		files = append(files, f)
	}
	sort.Slice(files, func(i, k int) bool { return files[i].DTO.FileName < files[k].DTO.FileName })
	for _, f := range files {
		j.Files = append(j.Files, FileProgress{ID: f.DTO.ID, Name: f.DTO.FileName, Size: f.DTO.Size})
		j.Total += f.DTO.Size
	}
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	snap := j.snapshot()
	m.mu.Unlock()
	m.broadcast(j)
	if m.globalNotify != nil {
		m.globalNotify(snap) // UI popup
	}
}

func (m *Manager) ReceiveState(id, state, errMsg string) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	switch state {
	case "pending":
		j.State = StateAwaitingAccept
	case "accepted":
		j.State = StateReceiving
	case "done":
		j.State = StateDone
	case "declined":
		j.State = StateDeclined
	case "cancelled":
		j.State = StateCancelled
	case "failed":
		j.State = StateFailed
	default:
		j.State = state
	}
	j.Error = errMsg
	m.mu.Unlock()
	m.broadcast(j)
}

func (m *Manager) ReceiveProgress(id, fileID string, delta int64) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	for i := range j.Files {
		if j.Files[i].ID == fileID {
			j.Files[i].Sent += delta
			break
		}
	}
	j.Sent += delta
	if j.throttle == nil {
		j.throttle = &progressThrottle{emit: func() { m.broadcast(j) }}
	}
	th := j.throttle
	m.mu.Unlock()
	th.maybe()
}

func (m *Manager) ReceiveFileDone(id, fileID string) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	for i := range j.Files {
		if j.Files[i].ID == fileID {
			j.Files[i].Done = true
			break
		}
	}
	m.mu.Unlock()
	m.broadcast(j)
}
