// Package config parses command-line flags and environment variables
// (LOCALSEND_NAS_*) into the service configuration.
package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Share maps a display name to an absolute host path.
type Share struct {
	Name string
	Path string
}

// Config is the runtime configuration.
type Config struct {
	Listen     string  // web UI bind address
	LSPort     int     // LocalSend protocol port (TCP protocol server)
	Shares     []Share // ordered as given on the command line / env
	ReceiveDir string  // absolute receive destination (also share "inbox")
	Alias      string  // device alias advertised to other nodes
	DataDir    string  // identity + saved manual targets + settings
	ReadOnly   bool    // send-only posture: no receiving, strict read-only shares
}

// shareList collects repeatable --share name=path flags.
type shareList []Share

func (s *shareList) String() string {
	var parts []string
	for _, sh := range *s {
		parts = append(parts, sh.Name+"="+sh.Path)
	}
	return strings.Join(parts, ",")
}

func (s *shareList) Set(v string) error {
	sh, err := parseShare(v)
	if err != nil {
		return err
	}
	*s = append(*s, sh)
	return nil
}

func parseShare(v string) (Share, error) {
	name, path, ok := strings.Cut(v, "=")
	if !ok || name == "" || path == "" {
		return Share{}, fmt.Errorf("invalid share %q: want name=path", v)
	}
	if strings.ContainsAny(name, "/\x00") {
		return Share{}, fmt.Errorf("invalid share name %q", name)
	}
	return Share{Name: name, Path: path}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Parse reads flags (env-overridable via LOCALSEND_NAS_* variables).
// Precedence: flag > env > default.
func Parse(args []string) (*Config, error) {
	fs := flag.NewFlagSet("localsend-nas", flag.ContinueOnError)

	cfg := &Config{}
	fs.StringVar(&cfg.Listen, "listen", envOr("LOCALSEND_NAS_LISTEN", ":80"), "web UI bind address")
	fs.IntVar(&cfg.LSPort, "ls-port", envOrInt("LOCALSEND_NAS_LS_PORT", 53317), "LocalSend protocol port (TCP+multicast peer port)")
	fs.StringVar(&cfg.Alias, "alias", envOr("LOCALSEND_NAS_ALIAS", ""), `device alias (default: "Nas <hostname>")`)
	fs.StringVar(&cfg.DataDir, "data-dir", envOr("LOCALSEND_NAS_DATA_DIR", "/var/lib/localsend-nas"), "identity + saved manual targets directory")
	fs.BoolVar(&cfg.ReadOnly, "read-only", envOrBool("LOCALSEND_NAS_READ_ONLY", false), "send-only mode: no receiving, shares strictly read-only")
	fs.StringVar(&cfg.ReceiveDir, "receive-dir", envOr("LOCALSEND_NAS_RECEIVE_DIR", ""), "receive destination (absolute path); registered as share 'inbox'")
	var shares shareList
	fs.Var(&shares, "share", "share root as name=path (repeatable; at least one required unless --receive-dir is set)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Shares: flags win; fall back to comma-separated env list.
	if len(shares) == 0 {
		if env := os.Getenv("LOCALSEND_NAS_SHARES"); env != "" {
			for _, part := range strings.Split(env, ",") {
				sh, err := parseShare(strings.TrimSpace(part))
				if err != nil {
					return nil, err
				}
				shares = append(shares, sh)
			}
		}
	}
	if len(shares) == 0 && cfg.ReceiveDir == "" {
		return nil, fmt.Errorf("at least one --share name=path or --receive-dir is required")
	}
	seen := map[string]bool{}
	for _, sh := range shares {
		if seen[sh.Name] {
			return nil, fmt.Errorf("duplicate share name %q", sh.Name)
		}
		seen[sh.Name] = true
	}
	cfg.Shares = shares

	if cfg.Alias == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "node"
		}
		cfg.Alias = "Nas " + host
	}
	if cfg.LSPort < 1 || cfg.LSPort > 65535 {
		return nil, fmt.Errorf("invalid --ls-port %d", cfg.LSPort)
	}
	return cfg, nil
}

func envOrBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}
