// Package lsserver runs the node's LocalSend-facing HTTPS endpoint.
// It answers discovery-protocol requests (/info, /register) so the node
// appears in other devices' lists, and handles inbound transfers via the
// receive pipeline — or politely declines them in --read-only mode.
package lsserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/alexindigo/localsend-nas/internal/identity"
	"github.com/alexindigo/localsend-nas/internal/localsend"
	"github.com/alexindigo/localsend-nas/internal/receive"
)

// Registry records peers that register with us.
type Registry interface {
	Upsert(info localsend.Info, sourceIP string)
}

type Server struct {
	info     localsend.Info
	registry Registry
	receiver *receive.Manager // nil = read-only (send-only) mode
	http     *http.Server
}

// New builds the server bound to the LocalSend protocol port. A nil
// receiver keeps the send-only polite-reject posture (--read-only).
func New(port int, id *identity.Identity, info localsend.Info, registry Registry, receiver *receive.Manager) *Server {
	s := &Server{info: info, registry: registry, receiver: receiver}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/localsend/v2/info", s.handleInfo)
	mux.HandleFunc("POST /api/localsend/v2/register", s.handleRegister)
	mux.HandleFunc("POST /api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("POST /api/localsend/v2/upload", s.handleUpload)
	mux.HandleFunc("POST /api/localsend/v2/cancel", s.handleCancel)
	s.http = &http.Server{
		Addr:      fmt.Sprintf(":%d", port),
		Handler:   mux,
		TLSConfig: id.TLSConfig(),
	}
	return s
}

// ListenAndServeTLS runs until the server is shut down.
func (s *Server) ListenAndServeTLS() error {
	return s.http.ListenAndServeTLS("", "") // certs come from TLSConfig
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.info)
}

// handleRegister: peers reply to our multicast announcements by POSTing
// their info here; respond with ours (protocol v2 §3).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var peer localsend.Info
	if err := json.NewDecoder(r.Body).Decode(&peer); err != nil || peer.Fingerprint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid register body"})
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	s.registry.Upsert(peer, host)
	writeJSON(w, http.StatusOK, s.info)
}

func (s *Server) handlePrepareUpload(w http.ResponseWriter, r *http.Request) {
	if s.receiver == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "localsend-nas is send-only"})
		return
	}
	var req localsend.PrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid prepare-upload body"})
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	status, resp, err := s.receiver.Prepare(r.Context(), req.Info, host, clientCertFP(r), req.Files)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // sender went away; nothing to answer
		}
		writeJSON(w, status, map[string]string{"message": err.Error()})
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.receiver == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "localsend-nas is send-only"})
		return
	}
	q := r.URL.Query()
	status, err := s.receiver.Upload(r.Context(), q.Get("sessionId"), q.Get("fileId"), q.Get("token"), r.Body)
	if err != nil {
		writeJSON(w, status, map[string]string{"message": err.Error()})
		return
	}
	w.WriteHeader(status)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if s.receiver != nil {
		s.receiver.CancelSession(r.URL.Query().Get("sessionId"))
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientCertFP is the SHA-256 (uppercase hex) of the sender's client
// certificate, when one was presented — the unspoofable device identity.
func clientCertFP(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
