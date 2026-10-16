// Package identity manages the node's persistent device identity:
// an ECDSA self-signed certificate whose SHA-256 hash serves as the
// LocalSend fingerprint (protocol v2 §2, HTTPS mode).
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
)

// Identity is the node's persistent cryptographic identity.
type Identity struct {
	Cert        tls.Certificate
	Fingerprint string // hex(SHA-256 of cert DER)
}

// Load returns the identity stored in dir, generating and persisting a new
// one on first start. Files are written with 0600 permissions.
func Load(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	switch {
	case err == nil:
		// reused
	case errors.Is(err, os.ErrNotExist):
		cert, err = generate()
		if err != nil {
			return nil, fmt.Errorf("generate certificate: %w", err)
		}
		if err := save(certPath, keyPath, cert); err != nil {
			return nil, fmt.Errorf("persist certificate: %w", err)
		}
	default:
		return nil, fmt.Errorf("load certificate: %w", err)
	}

	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("certificate at %s has no DER data", certPath)
	}
	sum := sha256.Sum256(cert.Certificate[0])
	// Uppercase hex is the protocol-canonical fingerprint encoding.
	return &Identity{Cert: cert, Fingerprint: strings.ToUpper(hex.EncodeToString(sum[:]))}, nil
}

// TLSConfig returns a server-side TLS config presenting this identity.
// Clients are expected to skip verification (fingerprint pinning is
// informational in the LocalSend protocol). Client certificates are
// requested but not required: since protocol v2.1 senders identify
// themselves by client cert, and we want their registration to work.
func (id *Identity) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.RequestClientCert,
	}
}

func generate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localsend-nas"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour), // long-lived; clients skip verify
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		// ClientAuth is required: since protocol v2.1, receivers demand a
		// client certificate and reject ServerAuth-only certs.
		// SANs deliberately omitted: LocalSend clients use InsecureSkipVerify
		// and identify nodes by certificate fingerprint, not hostname.
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}

func save(certPath, keyPath string, cert tls.Certificate) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
