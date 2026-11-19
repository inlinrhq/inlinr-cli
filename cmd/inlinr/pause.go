package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/config"
)

// runPause stops tracking on this machine: `heartbeat` drops every beat
// while it lasts, and the Claude Code plugin, which reads the same
// config.toml, sends nothing either. What happens during a pause is never
// sent later. `--for` ends it by itself; otherwise `inlinr resume` does.
func runPause(args []string) error {
	fs := flag.NewFlagSet("pause", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.toml (default: ~/.inlinr/config.toml)")
	forFlag := fs.String("for", "", "how long, e.g. 30m or 2h (default: until 'inlinr resume')")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *forFlag == "" {
		cfg.Behavior.Paused = true
		cfg.Behavior.PausedUntil = nil
	} else {
		d, err := time.ParseDuration(*forFlag)
		if err != nil || d <= 0 {
			return errors.New("--for takes a duration such as 30m or 2h")
		}
		end := time.Now().Add(d).UTC().Truncate(time.Second)
		cfg.Behavior.Paused = false
		cfg.Behavior.PausedUntil = &end
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println(pauseLine(cfg, time.Now()))
	return nil
}

// runResume ends a pause, timed or not.
func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.toml (default: ~/.inlinr/config.toml)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	cfg.Behavior.Paused = false
	cfg.Behavior.PausedUntil = nil
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("Tracking resumed.")
	return nil
}

// pauseLine says where tracking stands, in the machine's own time zone.
func pauseLine(cfg config.Config, now time.Time) string {
	paused, until := cfg.PausedAt(now)
	switch {
	case !paused:
		return "Tracking is on."
	case until.IsZero():
		return "Tracking paused. Run 'inlinr resume' to start again."
	default:
		return fmt.Sprintf("Tracking paused until %s.", until.Local().Format("Mon 2 Jan 15:04"))
	}
}
