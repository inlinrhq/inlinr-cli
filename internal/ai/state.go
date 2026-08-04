package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Sync state lives beside the queue in ~/.inlinr.
//
// Two things have to be remembered between runs:
//
//   - a watermark, so a sync re-reads only what changed. Transcripts are
//     append-only and can reach tens of megabytes; re-parsing them on every
//     heartbeat flush would be both slow and a source of duplicate beats.
//   - a lock, because several editors can flush at once. Without it two
//     processes parse the same lines and every token is counted twice.

// State is the persisted sync watermark.
type State struct {
	// LastParsedAt is the timestamp of the newest transcript entry we turned
	// into heartbeats. Entries at or before it are skipped.
	LastParsedAt time.Time `json:"last_parsed_at"`
}

// StatePath is where the watermark is stored.
func StatePath(home string) string {
	return filepath.Join(home, "ai-sync.json")
}

// LoadState reads the watermark. A missing or unreadable file yields a zero
// state, which means "parse everything" — the safe direction on first run.
func LoadState(home string) State {
	data, err := os.ReadFile(StatePath(home))
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

// SaveState writes the watermark.
func SaveState(home string, s State) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", home, err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode ai sync state: %w", err)
	}
	path := StatePath(home)
	// Write-then-rename: a crash mid-write must not leave a truncated file
	// that would reset the watermark and replay the whole history.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %q: %w", tmp, err)
	}
	return nil
}

// ErrSyncBusy means another process holds the sync lock.
var ErrSyncBusy = errors.New("ai sync already running")

// LockStaleAfter bounds how long a lock is trusted. A process killed mid-sync
// would otherwise block syncing forever.
const LockStaleAfter = 5 * time.Minute

// Lock is a released-on-Close sync lock.
type Lock struct{ path string }

// AcquireLock takes the sync lock, or returns ErrSyncBusy.
func AcquireLock(home string) (*Lock, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create %q: %w", home, err)
	}
	path := filepath.Join(home, "ai-sync.lock")

	if info, err := os.Stat(path); err == nil {
		if time.Since(info.ModTime()) < LockStaleAfter {
			return nil, ErrSyncBusy
		}
		// Stale — the owner died. Take it over.
		_ = os.Remove(path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrSyncBusy
		}
		return nil, fmt.Errorf("create lock %q: %w", path, err)
	}
	_ = f.Close()
	return &Lock{path: path}, nil
}

// Release drops the lock. Safe to call twice.
func (l *Lock) Release() {
	if l == nil || l.path == "" {
		return
	}
	_ = os.Remove(l.path)
	l.path = ""
}
