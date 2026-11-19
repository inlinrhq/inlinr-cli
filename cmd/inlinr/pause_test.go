package main

import (
	"strings"
	"testing"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/config"
)

func TestPauseLine(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	c := config.Defaults()
	if got := pauseLine(c, now); got != "Tracking is on." {
		t.Fatalf("on: %q", got)
	}
	c.Behavior.Paused = true
	if got := pauseLine(c, now); !strings.Contains(got, "inlinr resume") {
		t.Fatalf("paused: %q", got)
	}
	end := now.Add(2 * time.Hour)
	c.Behavior.Paused = false
	c.Behavior.PausedUntil = &end
	if got := pauseLine(c, now); !strings.HasPrefix(got, "Tracking paused until ") {
		t.Fatalf("timed: %q", got)
	}
	if got := pauseLine(c, end); got != "Tracking is on." {
		t.Fatalf("after the end: %q", got)
	}
}
