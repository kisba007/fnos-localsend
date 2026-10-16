// Package localsend implements the client side of the LocalSend
// protocol v2.2 (https://github.com/localsend/protocol).
package localsend

import "fmt"

// ProtocolVersion is the implemented protocol version.
const ProtocolVersion = "2.2"

// Info is the device discovery/identity DTO.
type Info struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`     // "2.2"
	DeviceModel string `json:"deviceModel"` // "localsend-nas"
	DeviceType  string `json:"deviceType"`  // "server"
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"` // "https"
	Download    bool   `json:"download"` // false
	Announce    bool   `json:"announce,omitempty"`
}

// FileMetadata is optional per-file metadata.
type FileMetadata struct {
	Modified *string `json:"modified,omitempty"`
	Accessed *string `json:"accessed,omitempty"`
}

// FileDTO describes one file in a transfer batch.
type FileDTO struct {
	ID       string        `json:"id"`
	FileName string        `json:"fileName"`
	Size     int64         `json:"size"`
	FileType string        `json:"fileType"`         // mime.GuessType fallback "application/octet-stream"
	SHA256   *string       `json:"sha256,omitempty"` // optional; omitted in v1 (skip hash cost on big shares)
	Metadata *FileMetadata `json:"metadata,omitempty"`
}

// PrepareRequest is POSTed to /prepare-upload.
type PrepareRequest struct {
	Info  Info               `json:"info"`
	Files map[string]FileDTO `json:"files"`
}

// PrepareResponse maps file IDs to per-file upload tokens.
type PrepareResponse struct {
	SessionID string            `json:"sessionId"`
	Files     map[string]string `json:"files"` // fileId → token
}

// SendError is a typed protocol-level error; the UI renders Message verbatim.
type SendError struct {
	Status  int    // HTTP status from the receiver, 0 for transport errors
	Message string // receiver's message or a local description
}

func (e *SendError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("receiver responded %d: %s", e.Status, e.Message)
}
