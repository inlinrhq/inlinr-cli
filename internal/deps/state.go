package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// When each repository may be checked again, beside the queue in ~/.inlinr.
// The heartbeat path reads it on every flush, so the answer has to come from
// one small file and never from the network: a repository is asked about at
// most once a day, whatever the editor does.

// StatePath is where the schedule is stored.
func StatePath(home string) string {
	return filepath.Join(home, "deps-sync.json")
}

type state map[string]int64 // remote → unix seconds of the next check

func load(home string) state {
	data, err := os.ReadFile(StatePath(home))
	if err != nil {
		return state{}
	}
	var s state
	if json.Unmarshal(data, &s) != nil || s == nil {
		return state{}
	}
	return s
}

// Due reports whether `remote` may be checked now.
func Due(home, remote string, now time.Time) bool {
	return now.Unix() >= load(home)[remote]
}

// Defer records that `remote` is not to be checked before `until`. Entries
// long past are dropped so the file does not grow with every clone ever
// opened.
func Defer(home, remote string, until time.Time) error {
	s := load(home)
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	for k, v := range s {
		if v < cutoff {
			delete(s, k)
		}
	}
	s[remote] = until.Unix()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := StatePath(home)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
