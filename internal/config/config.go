// Package config loads and saves ~/.inlinr/config.toml.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// APIURL is the fixed inlinr server base URL. Not user-configurable — the
// service is SaaS-only, not self-hostable.
const APIURL = "https://inlinr.com"

type Config struct {
	Auth struct {
		DeviceToken string `toml:"device_token"`
	} `toml:"auth"`
	Behavior struct {
		HeartbeatRateLimitSeconds int `toml:"heartbeat_rate_limit_seconds"`
		OfflineQueueMax           int `toml:"offline_queue_max"`
		// DepsSync lets the heartbeat path check, once a day per repository,
		// whether its owner follows its dependencies on inlinr, and send the
		// manifests when they do. Nothing is read for a project not followed.
		// `deps_sync = false` turns the check itself off.
		DepsSync bool `toml:"deps_sync"`
		// ErrorReports sends the CLI's own failures to inlinr (see
		// internal/report): no token, no file content, the home folder
		// replaced by "~". `error_reports = false` turns it off.
		ErrorReports bool `toml:"error_reports"`
	} `toml:"behavior"`
	Logging struct {
		Level string `toml:"level"`
		File  string `toml:"file"`
	} `toml:"logging"`

	// TokenFrom names where Auth.DeviceToken came from when config.toml had
	// none: "claude-code" for the Claude Code plugin's file. Empty when the
	// token is this file's own. Never written back (see Save).
	TokenFrom string `toml:"-"`
}

// Defaults returns a Config with sane defaults applied (no token yet).
func Defaults() Config {
	var c Config
	c.Behavior.HeartbeatRateLimitSeconds = 120
	c.Behavior.OfflineQueueMax = 10_000
	c.Behavior.DepsSync = true
	c.Behavior.ErrorReports = true
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
		borrowPluginToken(&c)
		return c, nil
	}
	if _, err := toml.DecodeFile(p, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", p, err)
	}
	if c.Auth.DeviceToken == "" && override == "" {
		borrowPluginToken(&c)
	}
	return c, nil
}

// One connection per machine: every inlinr plugin on it sends with the same
// device token. config.toml holds it; the Claude Code plugin reads it there
// too. A machine connected by an older plugin release has its token only in
// ~/.inlinr/claude-code.json, so it is read from there when this file has
// none, rather than asking the person to sign in a second time.

// PluginPath is the Claude Code plugin's state file, next to config.toml.
func PluginPath() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "claude-code.json"), nil
}

func borrowPluginToken(c *Config) {
	p, err := PluginPath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var plugin struct {
		DeviceToken string `json:"deviceToken"`
	}
	if json.Unmarshal(raw, &plugin) != nil || plugin.DeviceToken == "" {
		return
	}
	c.Auth.DeviceToken = plugin.DeviceToken
	c.TokenFrom = "claude-code"
}

// ClearPluginToken removes token from the Claude Code plugin's file, when
// that file holds this very token. Signing out revokes the token on the
// server; leaving it there would have the plugin send with a dead token.
// Everything else in the file (read positions) is kept as it is: the plugin
// starts over by itself when it next sees another token.
func ClearPluginToken(token string) (bool, error) {
	p, err := PluginPath()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, fmt.Errorf("parse %s: %w", p, err)
	}
	var current string
	if json.Unmarshal(state["deviceToken"], &current) != nil || current != token {
		return false, nil
	}
	state["deviceToken"] = json.RawMessage(`""`)
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeAtomic(p, append(out, '\n'))
}

// Save writes the config to disk with mode 0600. A token borrowed from the
// Claude Code plugin's file stays there: Save writes only this file's own.
func Save(c Config) error {
	h, err := Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return err
	}
	if c.TokenFrom != "" {
		c.Auth.DeviceToken = ""
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(h, "config.toml"), buf.Bytes())
}

// writeAtomic replaces path in one step: the Claude Code plugin reads the
// token from config.toml on every hook, and a file caught half written would
// read as "not connected".
func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Platform returns a short platform string for device activation metadata.
func Platform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
