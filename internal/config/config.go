// Package config loads and saves ~/.inlinr/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// APIURL is the fixed Inlinr server base URL. Not user-configurable — the
// service is SaaS-only, not self-hostable.
const APIURL = "https://inlinr.com"

type Config struct {
	Auth struct {
		DeviceToken string `toml:"device_token"`
	} `toml:"auth"`
	Behavior struct {
		HeartbeatRateLimitSeconds int `toml:"heartbeat_rate_limit_seconds"`
		OfflineQueueMax           int `toml:"offline_queue_max"`
	} `toml:"behavior"`
	Logging struct {
		Level string `toml:"level"`
		File  string `toml:"file"`
	} `toml:"logging"`
}

// Defaults returns a Config with sane defaults applied (no token yet).
func Defaults() Config {
	var c Config
	c.Behavior.HeartbeatRateLimitSeconds = 120
	c.Behavior.OfflineQueueMax = 10_000
	c.Logging.Level = "info"
	return c
}

// Home returns ~/.inlinr (per-OS). Honours $INLINR_HOME override.
func Home() (string, error) {
	if v := os.Getenv("INLINR_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".inlinr"), nil
}

// Path returns the full config file path.
func Path() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "config.toml"), nil
}

// QueuePath returns the SQLite queue file path.
func QueuePath() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "queue.db"), nil
}

// Load reads config from disk, merging with defaults for missing fields.
// If override is non-empty, it's used as the config file path instead of
// ~/.inlinr/config.toml (maps to the `--config` flag).
func Load(override string) (Config, error) {
	c := Defaults()
	p := override
	if p == "" {
		def, err := Path()
		if err != nil {
			return c, err
		}
		p = def
	}
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if override != "" {
			return c, fmt.Errorf("config file not found: %s", override)
		}
		return c, nil
	}
	if _, err := toml.DecodeFile(p, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", p, err)
	}
	return c, nil
}

// Save writes the config to disk with mode 0600.
func Save(c Config) error {
	h, err := Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return err
	}
	p := filepath.Join(h, "config.toml")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// Platform returns a short platform string for device activation metadata.
func Platform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
