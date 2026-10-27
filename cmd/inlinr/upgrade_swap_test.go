package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFile(t *testing.T) {
	for _, renameAway := range []bool{false, true} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "inlinr")
		if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := replaceFile(bin, []byte("new"), renameAway); err != nil {
			t.Fatalf("renameAway=%v: %v", renameAway, err)
		}
		got, _ := os.ReadFile(bin)
		if string(got) != "new" {
			t.Fatalf("renameAway=%v: got %q", renameAway, got)
		}
		if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
			t.Fatalf("renameAway=%v: .new left behind", renameAway)
		}
	}
}
