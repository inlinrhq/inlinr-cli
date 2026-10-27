package main

import "testing"

func TestProjectName(t *testing.T) {
	cases := []struct{ remote, name, want string }{
		{"git@github.com:acme/app.git", "app", ""},
		{"", "side-folder", "side-folder"},
		{"", "/home/me/side-folder/", "side-folder"},
		{"", `C:\Users\me\notes`, "notes"},
		{"", "   ", ""},
	}
	for _, c := range cases {
		if got := projectName(c.remote, c.name); got != c.want {
			t.Errorf("projectName(%q, %q) = %q, want %q", c.remote, c.name, got, c.want)
		}
	}
}
