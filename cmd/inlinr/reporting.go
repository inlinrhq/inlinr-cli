package main

import (
	"path/filepath"

	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/report"
)

// reportedCommands are the ones that run unattended, spawned by a plugin:
// a failure there reaches no one unless it is reported. `activate`,
// `doctor` and `signout` run in front of the user, who sees the error.
var reportedCommands = map[string]bool{
	"heartbeat": true,
	"sync-deps": true,
	"upgrade":   true,
	"mcp":       true,
}

// rep sends this run's failures to Admin → Errors (see internal/report).
// `heartbeat` sets it once its config is read, with the editor that spawned
// it; anything failing earlier gets one built from the default config.
var rep *report.Reporter

func reporter() *report.Reporter {
	if rep == nil {
		// A config that does not parse still reports: that is a failure
		// worth hearing about, and Load returns the defaults with it.
		cfg, _ := config.Load("")
		rep = newReporter(cfg)
	}
	return rep
}

func newReporter(cfg config.Config) *report.Reporter {
	state := ""
	if home, err := config.Home(); err == nil {
		state = filepath.Join(home, "error-reports.json")
	}
	return &report.Reporter{
		Enabled:   cfg.Behavior.ErrorReports,
		BaseURL:   config.APIURL,
		Version:   Version,
		StatePath: state,
	}
}

// reportFailure sends err when `command` runs unattended and err is the
// CLI's own failure rather than the network's or the user's.
func reportFailure(command string, err error) {
	if reportedCommands[command] {
		reporter().Error(command, err)
	}
}
