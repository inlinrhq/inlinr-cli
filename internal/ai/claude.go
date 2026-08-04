// Package ai turns an AI assistant's own local session logs into heartbeats.
//
// Why here and not in the editor plugin: token counts and per-tool edit sizes
// only exist inside the assistant's transcripts. An editor extension can see
// that a block of code appeared, but not that it cost 42k input tokens, nor
// that it came from the same conversation as the previous block — and it sees
// nothing at all when the assistant runs in a terminal or a desktop app.
//
// Putting the parser in the CLI means one implementation serves Claude Code in
// the terminal, in Claude Desktop and inside VS Code, and the editor plugins
// stay dumb event pumps. WakaTime landed on the same split: their Claude plugin
// passes three flags and all nineteen parsers live in wakatime-cli.
//
// Claude Code writes one JSON-lines transcript per session under
// ~/.claude/projects/<slugified-cwd>/<session-uuid>.jsonl.
package ai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/gitctx"
	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

// Transcripts can contain very long tool outputs on a single line.
const maxTranscriptLineBytes = 10 * 1024 * 1024

// AIToolClaudeCode is the wire enum value for Claude Code.
const AIToolClaudeCode = "claude-code"

// CategoryAICoding marks time spent driving an assistant rather than typing.
const CategoryAICoding = "ai-coding"

type usage struct {
	InputTokens              *int64 `json:"input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	TotalTokens              *int64 `json:"total_tokens"`
}

type message struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Model string `json:"model"`
	Usage *usage `json:"usage"`
}

// structuredPatch is Claude Code's per-hunk diff summary on an Edit/Write
// tool result. Summing (newLines - oldLines) over the hunks is the most
// reliable line-change signal available.
type structuredPatch struct {
	NewLines int `json:"newLines"`
	OldLines int `json:"oldLines"`
}

type toolUseResult struct {
	Type            *string           `json:"type"`
	FilePath        *string           `json:"filePath"`
	OriginalFile    *string           `json:"originalFile"`
	OldString       *string           `json:"oldString"`
	NewString       *string           `json:"newString"`
	StructuredPatch []structuredPatch `json:"structuredPatch"`
}

type logLine struct {
	Timestamp     time.Time      `json:"timestamp"`
	SessionID     string         `json:"sessionId"`
	Cwd           *string        `json:"cwd"`
	Type          *string        `json:"type"`
	IsSidechain   *bool          `json:"isSidechain"`
	Message       *message       `json:"message"`
	Usage         *usage         `json:"usage"`
	ToolUseResult *toolUseResult `json:"toolUseResult"`
}

// Tokens accumulated for one session.
//
// The cache components are kept apart from Input on purpose: they are billed at
// very different rates (cache reads at a fraction of the input rate, cache
// writes above it). Collapsing them into one number and pricing it at the input
// rate overstates a long assistant session by close to an order of magnitude —
// most of a Claude Code turn is re-read context.
type Tokens struct {
	// Fresh input tokens, billed at the full input rate.
	Input int64
	// Context re-read from the prompt cache.
	CacheRead int64
	// Context written to the prompt cache.
	CacheWrite int64
	Output     int64
}

// TotalInput is every token that counted as input, for display. Not a billing
// figure — see the note on Tokens.
func (t Tokens) TotalInput() int64 {
	return t.Input + t.CacheRead + t.CacheWrite
}

// FileEdit is one assistant write to one file.
type FileEdit struct {
	Path      string
	Timestamp time.Time
	// Net line delta: positive when the assistant added more than it removed.
	LineChanges  int
	LinesAdded   int
	LinesRemoved int
}

// Session is one assistant conversation, with everything we learned from it.
type Session struct {
	ID        string
	Cwd       string
	Model     string
	Tokens    Tokens
	Edits     []FileEdit
	FirstSeen time.Time
	LastSeen  time.Time
}

// lastMessage remembers the previous message's token contribution.
//
// Claude Code logs the *same* message id several times while a response
// streams in, each time with the running usage totals. Adding them up
// multiplies a response's cost by the number of chunks, so the previous
// contribution is subtracted before the new one is added.
type lastMessage struct {
	id         string
	input      int64
	cacheRead  int64
	cacheWrite int64
	output     int64
}

// ClaudeHome returns the directories that may hold Claude Code transcripts.
// CLAUDE_CONFIG_DIR overrides the default, and is what the tests use.
func ClaudeHome() ([]string, error) {
	if override := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); override != "" {
		return []string{override}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return []string{filepath.Join(home, ".claude")}, nil
}

// TranscriptPaths lists session transcripts modified at or after `after`.
//
// Filtering on mtime first means a long history costs one stat per file
// instead of a full parse — this runs on every heartbeat flush.
func TranscriptPaths(dirs []string, after time.Time) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		projects := filepath.Join(dir, "projects")
		info, err := os.Stat(projects)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %q: %w", projects, err)
		}
		if !info.IsDir() {
			continue
		}

		err = filepath.WalkDir(projects, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				// A single unreadable subdirectory must not abort the sweep.
				return nil //nolint:nilerr
			}
			if d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil //nolint:nilerr
			}
			if !after.IsZero() && fi.ModTime().Before(after) {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %q: %w", projects, err)
		}
	}
	return out, nil
}

// ParseTranscript reads one session transcript, keeping only entries at or
// after `after`.
//
// Malformed lines are skipped rather than failing the file: transcripts are
// appended to live, so the last line is routinely a partial write.
func ParseTranscript(path string, after time.Time) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	session := &Session{}
	var last lastMessage

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxTranscriptLineBytes)

	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line logLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		// Sub-agent chatter is already accounted for by the parent session.
		if line.IsSidechain != nil && *line.IsSidechain {
			continue
		}
		if !after.IsZero() && !line.Timestamp.IsZero() && line.Timestamp.Before(after) {
			continue
		}

		if session.ID == "" && line.SessionID != "" {
			session.ID = line.SessionID
		}
		if session.Cwd == "" && line.Cwd != nil {
			session.Cwd = *line.Cwd
		}
		if line.Message != nil && line.Message.Model != "" {
			session.Model = line.Message.Model
		}
		if !line.Timestamp.IsZero() {
			if session.FirstSeen.IsZero() || line.Timestamp.Before(session.FirstSeen) {
				session.FirstSeen = line.Timestamp
			}
			if line.Timestamp.After(session.LastSeen) {
				session.LastSeen = line.Timestamp
			}
		}

		accumulateTokens(line, &session.Tokens, &last)

		if edit, ok := fileEdit(line); ok {
			session.Edits = append(session.Edits, edit)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	if session.ID == "" {
		// Derive it from the filename — Claude names transcripts after the
		// session uuid, and a session with no id can't be grouped.
		session.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	return session, nil
}

func accumulateTokens(line logLine, totals *Tokens, last *lastMessage) {
	u := line.Usage
	id := ""
	if line.Message != nil {
		if line.Message.Usage != nil {
			u = line.Message.Usage
		}
		id = line.Message.ID
	}
	if u == nil {
		return
	}

	var in, cacheRead, cacheWrite int64
	if u.InputTokens != nil {
		in = *u.InputTokens
	}
	// Cache reads and writes are billed input tokens — dropping them
	// under-reports a long conversation badly, since most of a Claude Code turn
	// is cached context. They are tracked separately because they are billed at
	// different rates; see the note on Tokens.
	if u.CacheCreationInputTokens != nil {
		cacheWrite = *u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens != nil {
		cacheRead = *u.CacheReadInputTokens
	}

	var out int64
	switch {
	case u.OutputTokens != nil:
		out = *u.OutputTokens
	case u.TotalTokens != nil:
		out = *u.TotalTokens
	}

	if id != "" && id == last.id {
		// Streaming update of the message we just counted: replace its
		// contribution instead of adding a second one.
		totals.Input += in - last.input
		totals.CacheRead += cacheRead - last.cacheRead
		totals.CacheWrite += cacheWrite - last.cacheWrite
		totals.Output += out - last.output
	} else {
		totals.Input += in
		totals.CacheRead += cacheRead
		totals.CacheWrite += cacheWrite
		totals.Output += out
	}

	if id != "" {
		last.id = id
		last.input, last.cacheRead, last.cacheWrite, last.output = in, cacheRead, cacheWrite, out
	}
}

// fileEdit extracts a write from a tool result, or reports that the line
// wasn't one.
func fileEdit(line logLine) (FileEdit, bool) {
	r := line.ToolUseResult
	if r == nil || r.FilePath == nil || *r.FilePath == "" {
		return FileEdit{}, false
	}
	if !isWrite(r) {
		return FileEdit{}, false
	}

	added, removed := lineDelta(r)
	return FileEdit{
		Path:         *r.FilePath,
		Timestamp:    line.Timestamp,
		LineChanges:  added - removed,
		LinesAdded:   added,
		LinesRemoved: removed,
	}, true
}

// isWrite distinguishes an edit from a read. The Read tool also reports a
// filePath, and counting reads as authored lines would inflate every number
// downstream.
func isWrite(r *toolUseResult) bool {
	if len(r.StructuredPatch) > 0 {
		return true
	}
	if r.Type != nil {
		switch *r.Type {
		case "create", "update", "delete":
			return true
		}
	}
	if r.NewString != nil || r.OldString != nil {
		return true
	}
	// A non-empty originalFile with no patch and no new string is a read.
	return false
}

func lineDelta(r *toolUseResult) (added, removed int) {
	if len(r.StructuredPatch) > 0 {
		for _, p := range r.StructuredPatch {
			added += p.NewLines
			removed += p.OldLines
		}
		return added, removed
	}
	if r.NewString != nil {
		added = countLines(*r.NewString)
	}
	if r.OldString != nil {
		removed = countLines(*r.OldString)
	}
	return added, removed
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	// A trailing newline terminates the last line rather than starting a new
	// one: "a\nb\n" is two lines, not three.
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// Heartbeats turns a parsed session into beats for one project.
//
// Two shapes, mirroring what the dashboard needs:
//   - one app beat carrying the session's tokens and model, so "what did this
//     conversation cost" is answerable without touching file rows;
//   - one file beat per edited file carrying its line counts.
//
// `gitRemote` is resolved by the caller from the session's cwd; sessions
// outside a repository are skipped there.
func (s *Session) Heartbeats(gitRemote, plugin string) []heartbeat.Heartbeat {
	if s == nil || gitRemote == "" {
		return nil
	}

	tool := AIToolClaudeCode
	category := CategoryAICoding
	editor := AIToolClaudeCode
	session := s.ID
	appType := "app"

	var beats []heartbeat.Heartbeat

	if s.Tokens.TotalInput() > 0 || s.Tokens.Output > 0 {
		at := s.LastSeen
		if at.IsZero() {
			at = time.Now()
		}
		in, out := s.Tokens.Input, s.Tokens.Output
		cacheRead, cacheWrite := s.Tokens.CacheRead, s.Tokens.CacheWrite
		b := heartbeat.Heartbeat{
			Entity:             "claude-code",
			Type:               appType,
			Time:               float64(at.UnixNano()) / 1e9,
			ProjectGitRemote:   gitRemote,
			Category:           &category,
			AITool:             &tool,
			AISession:          &session,
			AIInputTokens:      &in,
			AIOutputTokens:     &out,
			AICacheReadTokens:  &cacheRead,
			AICacheWriteTokens: &cacheWrite,
			Editor:             &editor,
			Plugin:             strOrNil(plugin),
		}
		if s.Model != "" {
			model := s.Model
			b.AIModel = &model
		}
		beats = append(beats, b)
	}

	for _, e := range s.Edits {
		at := e.Timestamp
		if at.IsZero() {
			at = s.LastSeen
		}
		if at.IsZero() {
			at = time.Now()
		}
		added, removed, net := e.LinesAdded, e.LinesRemoved, e.LineChanges
		path := e.Path
		beats = append(beats, heartbeat.Heartbeat{
			Entity:           path,
			Type:             "file",
			Time:             float64(at.UnixNano()) / 1e9,
			ProjectGitRemote: gitRemote,
			Category:         &category,
			IsWrite:          true,
			AITool:           &tool,
			AISession:        &session,
			AILineChanges:    &net,
			AILinesAdded:     &added,
			AILinesDeleted:   &removed,
			LinesAdded:       &added,
			LinesDeleted:     &removed,
			Editor:           &editor,
			Plugin:           strOrNil(plugin),
		})
	}

	return beats
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GitRemote resolves the origin remote for a session's working directory.
// Empty when the directory isn't in a repository — such sessions are skipped,
// since there's no project to attribute the conversation to.
func GitRemote(dir string) string {
	if dir == "" {
		return ""
	}
	return gitctx.Resolve(dir).Remote
}
