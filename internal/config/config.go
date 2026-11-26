// Package config loads, validates and persists the FritzFlow configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Capture modes.
const (
	ModeOnDemand = "on-demand" // capture only while somebody looks at the UI
	ModeAlways   = "always"    // capture permanently
)

// Snaplen bounds. Small values keep the load on the FRITZ!Box low.
const (
	MinSnaplen     = 64
	MaxSnaplen     = 1600
	DefaultSnaplen = 200
)

// Config is the persisted configuration.
type Config struct {
	FritzBox    string `json:"fritzbox"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Interface   string `json:"interface"`
	Snaplen     int    `json:"snaplen"`
	Listen      string `json:"listen"`
	CaptureMode string `json:"capture_mode"`
	ReverseDNS  bool   `json:"reverse_dns"`
	OpenBrowser bool   `json:"open_browser"`
	InsecureTLS bool   `json:"insecure_tls"`
	// TR064URL overrides the TR-064 endpoint (default: same host, port 49000).
	TR064URL string `json:"tr064_url,omitempty"`

	passwordFromEnv bool
}

// Default returns a configuration with sensible defaults.
func Default() Config {
	return Config{
		FritzBox:    "http://fritz.box",
		Interface:   "1-lan",
		Snaplen:     DefaultSnaplen,
		Listen:      "127.0.0.1:8484",
		CaptureMode: ModeOnDemand,
		ReverseDNS:  true,
		OpenBrowser: true,
		InsecureTLS: true, // FRITZ!Box uses a self-signed certificate
	}
}

// DefaultPath returns the per-user config file location.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "fritzflow.json"
	}
	return filepath.Join(dir, "fritzflow", "config.json")
}

var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

// Normalize trims values and fills in defaults.
func (c *Config) Normalize() {
	d := Default()
	c.FritzBox = strings.TrimRight(strings.TrimSpace(c.FritzBox), "/")
	if c.FritzBox == "" {
		c.FritzBox = d.FritzBox
	}
	if !strings.Contains(c.FritzBox, "://") {
		c.FritzBox = "http://" + c.FritzBox
	}
	c.Username = strings.TrimSpace(c.Username)
	c.Interface = strings.TrimSpace(c.Interface)
	if c.Interface == "" {
		c.Interface = d.Interface
	}
	if c.Snaplen == 0 {
		c.Snaplen = d.Snaplen
	}
	c.Snaplen = max(MinSnaplen, min(MaxSnaplen, c.Snaplen))
	c.Listen = strings.TrimSpace(c.Listen)
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.CaptureMode != ModeAlways {
		c.CaptureMode = ModeOnDemand
	}
	c.TR064URL = strings.TrimRight(strings.TrimSpace(c.TR064URL), "/")
}

// Validate reports configuration errors in user-facing German.
func (c Config) Validate() error {
	u, err := url.Parse(c.FritzBox)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("ungültige FRITZ!Box-Adresse %q (Beispiel: http://fritz.box)", c.FritzBox)
	}
	if !ifaceRe.MatchString(c.Interface) {
		return fmt.Errorf("ungültige Schnittstelle %q", c.Interface)
	}
	if c.TR064URL != "" {
		if u, err := url.Parse(c.TR064URL); err != nil || u.Host == "" {
			return fmt.Errorf("ungültige TR-064-Adresse %q", c.TR064URL)
		}
	}
	return nil
}

// Configured reports whether enough is known to talk to the box.
func (c Config) Configured() bool { return c.Password != "" }

// PasswordFromEnv reports whether the password came from FRITZFLOW_PASSWORD.
func (c Config) PasswordFromEnv() bool { return c.passwordFromEnv }

// Load reads the config file. A missing file yields defaults (exists=false).
func Load(path string) (cfg Config, exists bool, err error) {
	cfg = Default()
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		exists = true
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Default(), true, fmt.Errorf("konfigurationsdatei %s ist fehlerhaft: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return cfg, false, err
	}
	applyEnv(&cfg)
	cfg.Normalize()
	return cfg, exists, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("FRITZFLOW_HOST"); v != "" {
		c.FritzBox = v
	}
	if v := os.Getenv("FRITZFLOW_USER"); v != "" {
		c.Username = v
	}
	if v := os.Getenv("FRITZFLOW_PASSWORD"); v != "" {
		c.Password = v
		c.passwordFromEnv = true
	}
}

// WithPasswordFrom keeps the env-origin flag of the previous config when the
// password is carried over unchanged.
func (c Config) WithPasswordFrom(prev Config) Config {
	if c.Password == "" {
		c.Password = prev.Password
		c.passwordFromEnv = prev.passwordFromEnv
	}
	return c
}

// Save writes the config atomically with owner-only permissions.
// A password that came from the environment is never written to disk.
func Save(path string, c Config) error {
	if c.passwordFromEnv {
		c.Password = ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
