package main

import (
	"testing"

	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

func strp(s string) *string { return &s }

func TestDedupBeatsCollapsesWithin1s(t *testing.T) {
	beats := []heartbeat.Heartbeat{
		{Entity: "a.ts", Branch: strp("main"), Editor: strp("vscode"), Time: 100.0},
		{Entity: "a.ts", Branch: strp("main"), Editor: strp("vscode"), Time: 100.3},
		{Entity: "a.ts", Branch: strp("main"), Editor: strp("vscode"), Time: 100.9},
	}
	out := dedupBeats(beats)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].Time != 100.9 {
		t.Errorf("kept time = %v, want 100.9 (newest)", out[0].Time)
	}
}

func TestDedupBeatsKeepsDifferentEntity(t *testing.T) {
	beats := []heartbeat.Heartbeat{
		{Entity: "a.ts", Editor: strp("vscode"), Time: 100.0},
		{Entity: "b.ts", Editor: strp("vscode"), Time: 100.5},
	}
	out := dedupBeats(beats)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
}

func TestDedupBeatsKeepsOutsideWindow(t *testing.T) {
	beats := []heartbeat.Heartbeat{
		{Entity: "a.ts", Editor: strp("vscode"), Time: 100.0},
		{Entity: "a.ts", Editor: strp("vscode"), Time: 102.0},
	}
	out := dedupBeats(beats)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (gap > 1s)", len(out))
	}
}

func TestDedupBeatsKeepsDifferentBranch(t *testing.T) {
	beats := []heartbeat.Heartbeat{
		{Entity: "a.ts", Branch: strp("main"), Editor: strp("vscode"), Time: 100.0},
		{Entity: "a.ts", Branch: strp("feat"), Editor: strp("vscode"), Time: 100.3},
	}
	out := dedupBeats(beats)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
}
