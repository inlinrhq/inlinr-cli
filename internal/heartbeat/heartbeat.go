// Package heartbeat defines the wire shape sent to /api/v1/heartbeats.
//
// Field names are snake_case on the wire to stay compatible with the WakaTime
// plugin ecosystem convention (eases future plugin forks). Optional fields use
// `omitempty` so the CLI doesn't send nulls for data the plugin didn't capture.
package heartbeat

type Heartbeat struct {
	Entity           string   `json:"entity"`
	Type             string   `json:"type,omitempty"` // "file" (default), "app", "domain"
	Time             float64  `json:"time"`           // unix seconds, fractional ms ok
	ProjectGitRemote string   `json:"project_git_remote"`
	Branch           *string  `json:"branch,omitempty"`
	Language         *string  `json:"language,omitempty"`
	Category         *string  `json:"category,omitempty"` // coding|debugging|building|code-reviewing|writing-tests
	IsWrite          bool     `json:"is_write,omitempty"`
	LineNumber       *int     `json:"lineno,omitempty"`
	CursorPos        *int     `json:"cursorpos,omitempty"`
	Lines            *int     `json:"lines,omitempty"`
	AITool           *string  `json:"ai_tool,omitempty"` // copilot|cursor|claude-code|codeium|windsurf|aider
	AILineChanges    *int     `json:"ai_line_changes,omitempty"`
	HumanLineChanges *int     `json:"human_line_changes,omitempty"`
	Editor           *string  `json:"editor,omitempty"`
	Plugin           *string  `json:"plugin,omitempty"` // user-agent e.g. vscode-inlinr/0.1.0
}
