package heartbeat

import "testing"

func TestDropUnknownAITool(t *testing.T) {
	tool := func(s string) *string { return &s }
	cases := []struct {
		in      *string
		dropped string
		kept    bool
	}{
		{nil, "", false},
		{tool("claude-code"), "", true},
		{tool("aider"), "", true},
		{tool("claude_code"), "claude_code", false},
		{tool("chatgpt"), "chatgpt", false},
	}
	for _, c := range cases {
		h := Heartbeat{AITool: c.in}
		if got := h.DropUnknownAITool(); got != c.dropped {
			t.Errorf("DropUnknownAITool(%v) = %q, want %q", c.in, got, c.dropped)
		}
		if (h.AITool != nil) != c.kept {
			t.Errorf("DropUnknownAITool(%v) kept = %v, want %v", c.in, h.AITool != nil, c.kept)
		}
	}
}
