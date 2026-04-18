package main

import (
	"io"
	"os"

	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

// openLogFile opens `path` for append and redirects stderr to a MultiWriter
// that tees into both the original stderr and the file. Returns a no-op close
// func when `path` is empty.
func openLogFile(path string) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	orig := os.Stderr
	// We can't actually reassign os.Stderr atomically across goroutines without
	// plumbing; instead we duplicate by wrapping fmt.Fprintf callers. For now,
	// most of our stderr output goes through fmt.Fprintf(os.Stderr, ...) in
	// main.go's `fail()` — so we just write errors to the log file ourselves
	// via a package-level io.Writer.
	logSink = io.MultiWriter(orig, f)
	return f.Close, nil
}

// logSink is the destination for structured-ish log output. Defaults to stderr
// so callers can use it unconditionally. `openLogFile` swaps this for a
// MultiWriter when --log-file is set.
var logSink io.Writer = os.Stderr

// dedupBeats drops consecutive beats sharing the same (entity, branch, editor)
// within a 1s window. Matches WakaTime's offline-queue dedup semantics.
// Input must be pre-sorted by time asc; in practice the CLI always calls this
// with the buffer in arrival order, which is near-monotonic time.
func dedupBeats(beats []heartbeat.Heartbeat) []heartbeat.Heartbeat {
	if len(beats) <= 1 {
		return beats
	}
	const windowSec = 1.0
	out := make([]heartbeat.Heartbeat, 0, len(beats))
	for i, b := range beats {
		if i == 0 {
			out = append(out, b)
			continue
		}
		prev := out[len(out)-1]
		if isDup(prev, b, windowSec) {
			// keep the newer one in place of the older — same shape, newer time
			out[len(out)-1] = b
			continue
		}
		out = append(out, b)
	}
	return out
}

func isDup(a, b heartbeat.Heartbeat, windowSec float64) bool {
	if a.Entity != b.Entity {
		return false
	}
	if strPtrVal(a.Branch) != strPtrVal(b.Branch) {
		return false
	}
	if strPtrVal(a.Editor) != strPtrVal(b.Editor) {
		return false
	}
	gap := b.Time - a.Time
	if gap < 0 {
		gap = -gap
	}
	return gap <= windowSec
}

func strPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
