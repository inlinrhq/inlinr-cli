package heartbeat

import "testing"

func TestHide(t *testing.T) {
	branch := "client-acme/feature"
	cases := []struct {
		entity, typ, want string
	}{
		{"src/routes/index.tsx", "", "HIDDEN.tsx"},
		{"C:\\work\\acme\\api\\main.go", "file", "HIDDEN.go"},
		{"Makefile", "file", "HIDDEN"},
		{".env", "file", "HIDDEN"},
		{"claude-code", "app", "claude-code"},
	}
	for _, c := range cases {
		h := Heartbeat{Entity: c.entity, Type: c.typ, Branch: &branch}
		h.Hide(true, false)
		if h.Entity != c.want {
			t.Errorf("%q: got %q, want %q", c.entity, h.Entity, c.want)
		}
		if h.Branch == nil {
			t.Errorf("%q: the branch went with the file name", c.entity)
		}
	}

	h := Heartbeat{Entity: "src/a.ts", Branch: &branch}
	h.Hide(false, true)
	if h.Entity != "src/a.ts" || h.Branch != nil {
		t.Fatalf("branches only: got %q, %v", h.Entity, h.Branch)
	}
}
