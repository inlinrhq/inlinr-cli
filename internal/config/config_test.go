package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("INLINR_HOME", dir)
	return dir
}

func TestLoadBorrowsThePluginToken(t *testing.T) {
	dir := home(t)
	plugin := `{"deviceToken":"in_d_plugin","lastParsedAt":"2026-10-30T10:00:00Z","files":{}}`
	if err := os.WriteFile(filepath.Join(dir, "claude-code.json"), []byte(plugin), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.DeviceToken != "in_d_plugin" || c.TokenFrom != "claude-code" {
		t.Fatalf("got %q from %q", c.Auth.DeviceToken, c.TokenFrom)
	}

	// Saving does not copy the borrowed token into config.toml.
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if strings.Contains(string(raw), "in_d_plugin") {
		t.Fatalf("config.toml got the plugin's token:\n%s", raw)
	}
}

func TestOwnTokenWins(t *testing.T) {
	dir := home(t)
	os.WriteFile(filepath.Join(dir, "claude-code.json"), []byte(`{"deviceToken":"in_d_plugin"}`), 0o600)
	c := Defaults()
	c.Auth.DeviceToken = "in_d_cli"
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.DeviceToken != "in_d_cli" || got.TokenFrom != "" {
		t.Fatalf("got %q from %q", got.Auth.DeviceToken, got.TokenFrom)
	}
}

func TestClearPluginToken(t *testing.T) {
	dir := home(t)
	p := filepath.Join(dir, "claude-code.json")
	os.WriteFile(p, []byte(`{"deviceToken":"in_d_a","lastParsedAt":"2026-10-30T10:00:00Z","files":{"x":{"offset":3}}}`), 0o600)

	if cleared, err := ClearPluginToken("in_d_other"); err != nil || cleared {
		t.Fatalf("cleared another token: %v %v", cleared, err)
	}
	cleared, err := ClearPluginToken("in_d_a")
	if err != nil || !cleared {
		t.Fatalf("not cleared: %v %v", cleared, err)
	}
	var state map[string]any
	raw, _ := os.ReadFile(p)
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state["deviceToken"] != "" || state["lastParsedAt"] != "2026-10-30T10:00:00Z" || state["files"] == nil {
		t.Fatalf("unexpected state: %v", state)
	}
}

func TestClearPluginTokenWithoutFile(t *testing.T) {
	home(t)
	if cleared, err := ClearPluginToken("in_d_a"); err != nil || cleared {
		t.Fatalf("%v %v", cleared, err)
	}
}
