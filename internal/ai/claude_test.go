package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, session string, lines ...string) string {
	t.Helper()
	projectDir := filepath.Join(dir, "projects", "-home-dev-app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(projectDir, session+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestParseTranscriptTokens(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-1",
		`{"timestamp":"2026-08-04T10:00:00Z","sessionId":"sess-1","cwd":"/home/dev/app","message":{"id":"m1","role":"assistant","model":"claude-opus-5","usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":5000,"output_tokens":300}}}`,
		`{"timestamp":"2026-08-04T10:01:00Z","sessionId":"sess-1","message":{"id":"m2","role":"assistant","usage":{"input_tokens":10,"output_tokens":40}}}`,
	)

	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Cache components are billed input tokens but at very different rates, so
	// they are tracked apart from fresh input. Dropping them entirely would
	// under-report a Claude Code turn by an order of magnitude (most of the
	// context is cached); folding them into Input would over-report the cost by
	// pricing cache reads at ~10x their real rate.
	if want := int64(100 + 10); s.Tokens.Input != want {
		t.Errorf("input tokens = %d, want %d", s.Tokens.Input, want)
	}
	if want := int64(20); s.Tokens.CacheWrite != want {
		t.Errorf("cache write tokens = %d, want %d", s.Tokens.CacheWrite, want)
	}
	if want := int64(5000); s.Tokens.CacheRead != want {
		t.Errorf("cache read tokens = %d, want %d", s.Tokens.CacheRead, want)
	}
	if want := int64(100 + 20 + 5000 + 10); s.Tokens.TotalInput() != want {
		t.Errorf("total input = %d, want %d", s.Tokens.TotalInput(), want)
	}
	if want := int64(340); s.Tokens.Output != want {
		t.Errorf("output tokens = %d, want %d", s.Tokens.Output, want)
	}
	if s.Model != "claude-opus-5" {
		t.Errorf("model = %q, want claude-opus-5", s.Model)
	}
	if s.ID != "sess-1" {
		t.Errorf("session id = %q", s.ID)
	}
}

func TestParseTranscriptStreamingDuplicatesReplace(t *testing.T) {
	// The same message id is logged repeatedly as a response streams in, each
	// time with the running totals. Summing them would multiply the cost of
	// every response by its chunk count.
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-2",
		`{"timestamp":"2026-08-04T10:00:00Z","sessionId":"sess-2","message":{"id":"m1","usage":{"input_tokens":1000,"cache_read_input_tokens":400,"output_tokens":10}}}`,
		`{"timestamp":"2026-08-04T10:00:01Z","sessionId":"sess-2","message":{"id":"m1","usage":{"input_tokens":1000,"cache_read_input_tokens":400,"output_tokens":250}}}`,
		`{"timestamp":"2026-08-04T10:00:02Z","sessionId":"sess-2","message":{"id":"m1","usage":{"input_tokens":1000,"cache_read_input_tokens":400,"output_tokens":900}}}`,
	)

	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tokens.Input != 1000 {
		t.Errorf("input tokens = %d, want 1000 (not 3000)", s.Tokens.Input)
	}
	if s.Tokens.CacheRead != 400 {
		t.Errorf("cache read tokens = %d, want 400 (not 1200)", s.Tokens.CacheRead)
	}
	if s.Tokens.Output != 900 {
		t.Errorf("output tokens = %d, want 900 (not 1160)", s.Tokens.Output)
	}
}

func TestParseTranscriptFileEdits(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-3",
		// A patch: two hunks, 12 new lines replacing 4 old ones.
		`{"timestamp":"2026-08-04T10:00:00Z","sessionId":"sess-3","toolUseResult":{"filePath":"/home/dev/app/src/app.ts","structuredPatch":[{"newLines":8,"oldLines":3},{"newLines":4,"oldLines":1}]}}`,
		// A read: filePath present, but nothing written.
		`{"timestamp":"2026-08-04T10:00:05Z","sessionId":"sess-3","toolUseResult":{"filePath":"/home/dev/app/README.md","originalFile":"# app\n"}}`,
		// An Edit tool result without a patch.
		`{"timestamp":"2026-08-04T10:00:09Z","sessionId":"sess-3","toolUseResult":{"filePath":"/home/dev/app/src/util.ts","oldString":"a\nb\n","newString":"a\nb\nc\nd\n"}}`,
	)

	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(s.Edits) != 2 {
		t.Fatalf("edits = %d, want 2 (the read must not count)", len(s.Edits))
	}

	first := s.Edits[0]
	if first.LinesAdded != 12 || first.LinesRemoved != 4 || first.LineChanges != 8 {
		t.Errorf("patch edit = +%d/-%d net %d, want +12/-4 net 8",
			first.LinesAdded, first.LinesRemoved, first.LineChanges)
	}

	second := s.Edits[1]
	if second.LinesAdded != 4 || second.LinesRemoved != 2 {
		t.Errorf("string edit = +%d/-%d, want +4/-2",
			second.LinesAdded, second.LinesRemoved)
	}
}

func TestParseTranscriptSkipsSidechains(t *testing.T) {
	// Sub-agent turns are already reflected in the parent session's usage.
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-4",
		`{"timestamp":"2026-08-04T10:00:00Z","sessionId":"sess-4","message":{"id":"m1","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"timestamp":"2026-08-04T10:00:01Z","sessionId":"sess-4","isSidechain":true,"message":{"id":"m2","usage":{"input_tokens":9999,"output_tokens":9999}}}`,
	)

	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tokens.Input != 100 || s.Tokens.Output != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", s.Tokens.Input, s.Tokens.Output)
	}
}

func TestParseTranscriptHonoursWatermark(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-5",
		`{"timestamp":"2026-08-04T09:00:00Z","sessionId":"sess-5","message":{"id":"old","usage":{"input_tokens":500,"output_tokens":500}}}`,
		`{"timestamp":"2026-08-04T11:00:00Z","sessionId":"sess-5","message":{"id":"new","usage":{"input_tokens":7,"output_tokens":3}}}`,
	)

	after := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	s, err := ParseTranscript(path, after)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tokens.Input != 7 || s.Tokens.Output != 3 {
		t.Errorf("tokens = %d/%d, want 7/3 — entries before the watermark must be skipped",
			s.Tokens.Input, s.Tokens.Output)
	}
}

func TestParseTranscriptToleratesGarbage(t *testing.T) {
	// Transcripts are appended to live, so the tail is routinely a partial
	// write. One bad line must not lose the whole file.
	dir := t.TempDir()
	path := writeTranscript(t, dir, "sess-6",
		`{"timestamp":"2026-08-04T10:00:00Z","sessionId":"sess-6","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5}}}`,
		`{"timestamp":"2026-08-04T10:00:01Z","sessionId":"sess-6","messa`,
	)

	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tokens.Input != 10 {
		t.Errorf("input tokens = %d, want 10", s.Tokens.Input)
	}
}

func TestParseTranscriptFallsBackToFilenameForSessionID(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "9f2c1ab0-dead-beef",
		`{"timestamp":"2026-08-04T10:00:00Z","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":1}}}`,
	)
	s, err := ParseTranscript(path, time.Time{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.ID != "9f2c1ab0-dead-beef" {
		t.Errorf("session id = %q, want the filename", s.ID)
	}
}

func TestTranscriptPathsFiltersByModTime(t *testing.T) {
	dir := t.TempDir()
	old := writeTranscript(t, dir, "old", `{"sessionId":"old"}`)
	recent := writeTranscript(t, dir, "recent", `{"sessionId":"recent"}`)

	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	paths, err := TranscriptPaths([]string{dir}, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("transcript paths: %v", err)
	}
	if len(paths) != 1 || paths[0] != recent {
		t.Errorf("paths = %v, want just %q", paths, recent)
	}
}

func TestTranscriptPathsMissingDirectory(t *testing.T) {
	paths, err := TranscriptPaths([]string{filepath.Join(t.TempDir(), "nope")}, time.Time{})
	if err != nil {
		t.Fatalf("expected no error for a missing directory, got %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty", paths)
	}
}

func TestSessionHeartbeats(t *testing.T) {
	s := &Session{
		ID:       "sess-7",
		Model:    "claude-opus-5",
		Tokens:   Tokens{Input: 1234, CacheRead: 90_000, CacheWrite: 800, Output: 567},
		LastSeen: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		Edits: []FileEdit{{
			Path:         "/home/dev/app/src/app.ts",
			Timestamp:    time.Date(2026, 8, 4, 11, 59, 0, 0, time.UTC),
			LinesAdded:   12,
			LinesRemoved: 4,
			LineChanges:  8,
		}},
	}

	beats := s.Heartbeats("git@github.com:acme/app.git", "claude-code-inlinr/0.1.0")
	if len(beats) != 2 {
		t.Fatalf("beats = %d, want 2 (one app + one file)", len(beats))
	}

	app := beats[0]
	if app.Type != "app" {
		t.Errorf("first beat type = %q, want app", app.Type)
	}
	if app.AIInputTokens == nil || *app.AIInputTokens != 1234 {
		t.Errorf("app input tokens = %v, want 1234", app.AIInputTokens)
	}
	if app.AIModel == nil || *app.AIModel != "claude-opus-5" {
		t.Errorf("app model = %v", app.AIModel)
	}
	if app.AICacheReadTokens == nil || *app.AICacheReadTokens != 90_000 {
		t.Errorf("app cache read tokens = %v, want 90000", app.AICacheReadTokens)
	}
	if app.AICacheWriteTokens == nil || *app.AICacheWriteTokens != 800 {
		t.Errorf("app cache write tokens = %v, want 800", app.AICacheWriteTokens)
	}
	if app.Category == nil || *app.Category != CategoryAICoding {
		t.Errorf("app category = %v, want %q", app.Category, CategoryAICoding)
	}

	file := beats[1]
	if file.Entity != "/home/dev/app/src/app.ts" {
		t.Errorf("file entity = %q", file.Entity)
	}
	if file.AILinesAdded == nil || *file.AILinesAdded != 12 {
		t.Errorf("ai lines added = %v, want 12", file.AILinesAdded)
	}
	if file.AITool == nil || *file.AITool != AIToolClaudeCode {
		t.Errorf("ai tool = %v", file.AITool)
	}
	if !file.IsWrite {
		t.Error("file beat should be a write")
	}
	// Both beats must share the session so the dashboard can group them.
	if app.AISession == nil || file.AISession == nil || *app.AISession != *file.AISession {
		t.Error("app and file beats must carry the same ai session")
	}
}

func TestSessionHeartbeatsWithoutRemote(t *testing.T) {
	// A session started outside a repository has nothing to attribute time to.
	s := &Session{ID: "x", Tokens: Tokens{Input: 1}}
	if beats := s.Heartbeats("", ""); beats != nil {
		t.Errorf("beats = %v, want nil without a git remote", beats)
	}
}

func TestTranscriptPathsSkipsSubagents(t *testing.T) {
	// Subagent transcripts live in <session>/subagents/agent-*.jsonl. They are
	// not sessions: their work is already in the parent's file as isSidechain
	// lines, and a subagent transcript carries its own cwd — so reading them
	// relies entirely on that flag being present on every line to avoid
	// counting the same tokens and edits twice.
	//
	// On a real machine 1436 of 1504 files were subagents, every one reported
	// as a session "skipped (no git remote)".
	home := t.TempDir()
	session := filepath.Join(home, "projects", "D--repo", "abc-123")
	if err := os.MkdirAll(filepath.Join(session, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}

	real := filepath.Join(home, "projects", "D--repo", "abc-123.jsonl")
	if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(session, "subagents", "agent-deadbeef.jsonl")
	if err := os.WriteFile(sub, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := TranscriptPaths([]string{home}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("want only the session transcript, got %d: %v", len(paths), paths)
	}
	if filepath.Base(paths[0]) != "abc-123.jsonl" {
		t.Fatalf("wrong file kept: %s", paths[0])
	}
	for _, p := range paths {
		if strings.Contains(filepath.ToSlash(p), "/subagents/") {
			t.Fatalf("subagent transcript not skipped: %s", p)
		}
	}
}
