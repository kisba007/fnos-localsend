package localsend

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const apiBase = "/api/localsend/v2"

// Client talks to a remote LocalSend device over HTTPS.
// Receivers use self-signed certs, so verification is skipped;
// devices are identified by certificate fingerprint instead.
// Since protocol v2.1 receivers REQUIRE a client certificate whose
// SHA-256 hash equals the fingerprint we claim — we present our own
// identity cert.
type Client struct {
	hc *http.Client
}

// NewClient builds a client presenting cert as its TLS client certificate.
func NewClient(cert tls.Certificate) *Client {
	return &Client{hc: &http.Client{
		// No Client.Timeout: uploads stream large files. Timeouts live on
		// the transport phases; cancellation flows via request contexts.
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ //nolint:gosec // server verify skipped per protocol
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{cert},
				// rustls-based receivers send an AcceptableCAs hint
				// containing only their own self-signed cert's DN, which
				// would make Go withhold our cert (issuer mismatch) and get
				// the handshake rejected with "certificate required".
				// Answer every CertificateRequest with our identity cert.
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return &cert, nil
				},
			},
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			// Receivers may sit on the accept prompt indefinitely; the
			// caller's context is the only deadline that should apply.
			ResponseHeaderTimeout: 0,
		},
	}}
}

func baseURL(host string, port int) string {
	return "https://" + net.JoinHostPort(host, fmt.Sprint(port)) + apiBase
}

// Probe fetches a device's info: GET /api/localsend/v2/info.
func (c *Client) Probe(ctx context.Context, host string, port int) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(host, port)+"/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, &SendError{Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode info: %w", err)
	}
	if info.Fingerprint == "" {
		return nil, fmt.Errorf("device at %s:%d is not a LocalSend node (empty fingerprint)", host, port)
	}
	return &info, nil
}

// Prepare asks the receiver to accept a batch: POST /prepare-upload.
// The request typically blocks until the user accepts or declines.
// A 204 status ("nothing needed") yields (nil, 204, nil).
func (c *Client) Prepare(ctx context.Context, host string, port int, preq PrepareRequest) (*PrepareResponse, int, error) {
	body, err := json.Marshal(preq)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(host, port)+"/prepare-upload", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, &SendError{Message: err.Error()}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var pr PrepareResponse
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode prepare response: %w", err)
		}
		return &pr, resp.StatusCode, nil
	case http.StatusNoContent:
		return nil, resp.StatusCode, nil // nothing needed (e.g. empty batch)
	default:
		return nil, resp.StatusCode, statusError(resp)
	}
}

// Upload streams one file: POST /upload?sessionId=&fileId=&token=.
// onProgress is invoked per read chunk with the byte delta.
func (c *Client) Upload(ctx context.Context, host string, port int, sessionID, fileID, token string, size int64, r io.Reader, onProgress func(int64)) error {
	q := url.Values{"sessionId": {sessionID}, "fileId": {fileID}, "token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(host, port)+"/upload?"+q.Encode(), &progressReader{r: r, on: onProgress})
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size
	resp, err := c.hc.Do(req)
	if err != nil {
		return &SendError{Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}
	return nil
}

// Cancel aborts a session: POST /cancel?sessionId=. Any 2xx counts as done.
func (c *Client) Cancel(ctx context.Context, host string, port int, sessionID string) error {
	q := url.Values{"sessionId": {sessionID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(host, port)+"/cancel?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return &SendError{Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusError(resp)
	}
	return nil
}

// statusError maps a non-OK response to a SendError, preserving the
// receiver's message body when present.
func statusError(resp *http.Response) error {
	msg := http.StatusText(resp.StatusCode)
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && body.Message != "" {
		msg = body.Message
	}
	switch resp.StatusCode {
	case http.StatusBadRequest, // 400 malformed
		http.StatusUnauthorized,        // 401 PIN required
		http.StatusForbidden,           // 403 declined
		http.StatusConflict,            // 409 busy with another session
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError: // 500
	}
	return &SendError{Status: resp.StatusCode, Message: msg}
}

type progressReader struct {
	r  io.Reader
	on func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.on != nil {
		p.on(int64(n))
	}
	return n, err
}
