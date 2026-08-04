package ai

import (
	"os"
	"testing"
	"time"
)

func TestStateRoundTripsLastSyncAt(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	if err := SaveState(home, State{LastParsedAt: now, LastSyncAt: now}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadState(home)
	if !got.LastSyncAt.Equal(now) {
		t.Fatalf("LastSyncAt = %v, want %v", got.LastSyncAt, now)
	}
}

// A state file written by an older CLI has no `last_sync_at` at all. It has to
// decode to the zero time and mean "never synced" — not "synced at the epoch",
// which would be indistinguishable from a very old sync and is only safe here
// because the throttle explicitly checks IsZero.
func TestStateFromOlderCliHasZeroLastSyncAt(t *testing.T) {
	home := t.TempDir()
	old := `{"last_parsed_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(StatePath(home), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadState(home)
	if !got.LastSyncAt.IsZero() {
		t.Fatalf("LastSyncAt = %v, want zero so the first run is never throttled", got.LastSyncAt)
	}
	if got.LastParsedAt.IsZero() {
		t.Fatal("LastParsedAt was lost, which would replay the whole transcript history")
	}
}
