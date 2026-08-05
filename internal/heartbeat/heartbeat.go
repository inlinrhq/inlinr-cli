// Package heartbeat defines the wire shape sent to /api/v1/heartbeats.
//
// Field names are snake_case on the wire to stay compatible with the WakaTime
// plugin ecosystem convention (eases future plugin forks). Optional fields use
// `omitempty` so the CLI doesn't send nulls for data the plugin didn't capture.
package heartbeat

type Heartbeat struct {
	Entity           string  `json:"entity"`
	Type             string  `json:"type,omitempty"` // "file" (default), "app", "domain"
	Time             float64 `json:"time"`           // unix seconds, fractional ms ok
	ProjectGitRemote string  `json:"project_git_remote"`
	Branch           *string `json:"branch,omitempty"`
	Language         *string `json:"language,omitempty"`
	Category         *string `json:"category,omitempty"` // coding|debugging|building|code-reviewing|writing-tests|ai-coding
	IsWrite          bool    `json:"is_write,omitempty"`
	LineNumber       *int    `json:"lineno,omitempty"`
	CursorPos        *int    `json:"cursorpos,omitempty"`
	Lines            *int    `json:"lines,omitempty"`

	// --- Authorship -------------------------------------------------------
	// Which assistant produced this edit, if any. Set only when there is
	// evidence of a generated edit — never merely because the tool is
	// installed. See inlinr-vscode/src/edit-attribution.ts.
	AITool *string `json:"ai_tool,omitempty"` // copilot|cursor|claude-code|codeium|windsurf|aider

	// Lines touched in this beat's window, split by who wrote them. Zero is a
	// meaningful value ("nothing changed"), which is why these are pointers:
	// absent means "this plugin doesn't count lines", 0 means "counted, none".
	LinesAdded     *int `json:"lines_added,omitempty"`
	LinesDeleted   *int `json:"lines_deleted,omitempty"`
	AILinesAdded   *int `json:"ai_lines_added,omitempty"`
	AILinesDeleted *int `json:"ai_lines_deleted,omitempty"`

	// Legacy net counters, kept for plugins that already send them.
	AILineChanges    *int `json:"ai_line_changes,omitempty"`
	HumanLineChanges *int `json:"human_line_changes,omitempty"`

	// --- AI session -------------------------------------------------------
	// Groups the beats belonging to one assistant conversation, and carries
	// its token usage. Populated by `inlinr sync-ai`, which reads the
	// assistant's own local transcripts — the only place these numbers exist.
	AISession      *string `json:"ai_session,omitempty"`
	AIModel        *string `json:"ai_model,omitempty"`
	AIInputTokens  *int64  `json:"ai_input_tokens,omitempty"`
	AIOutputTokens *int64  `json:"ai_output_tokens,omitempty"`
	// Cache components are reported apart from AIInputTokens because they are
	// billed at very different rates (reads well below the input rate, writes
	// above it). The server needs them separated to cost a session honestly.
	AICacheReadTokens  *int64 `json:"ai_cache_read_tokens,omitempty"`
	AICacheWriteTokens *int64 `json:"ai_cache_write_tokens,omitempty"`

	Editor *string `json:"editor,omitempty"`
	// Version of the editor hosting the plugin — not of the plugin itself,
	// which travels in Plugin below. VS Code forks report the embedded VS Code
	// build here, because that is the only version their extension API exposes:
	// neither Cursor nor Windsurf publishes its own to extensions.
	EditorVersion *string `json:"editor_version,omitempty"`
	Plugin        *string `json:"plugin,omitempty"` // user-agent e.g. vscode-inlinr/0.1.0
}
