package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/ai"
	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/queue"
)

// runSyncAI turns AI assistant transcripts into heartbeats.
//
// Intended to be called by a thin plugin on every assistant turn — the Claude
// Code plugin is little more than a hook that shells out to this. All the
// parsing lives here so one implementation covers the terminal, the desktop app
// and the editor extensions alike.
//
// Safe to call often: a watermark means only new transcript lines are read, and
// a lock means concurrent editors can't double-count.
func runSyncAI(args []string) error {
	fs := flag.NewFlagSet("sync-ai", flag.ExitOnError)
	plugin := fs.String("plugin", "", "plugin user-agent, e.g. claude-code-inlinr/0.1.0")
	projectFolder := fs.String("project-folder", "", "override the working directory used to resolve the git remote")
	configPath := fs.String("config", "", "path to config.toml (default: ~/.inlinr/config.toml)")
	logFile := fs.String("log-file", "", "append stderr to this file in addition to the console")
	dryRun := fs.Bool("dry-run", false, "parse and report without enqueuing or advancing the watermark")
	throttle := fs.Int("throttle", 0, "skip if a sync ran fewer than N seconds ago (0 = always run)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	closeLog, err := openLogFile(*logFile)
	if err != nil {
		return err
	}
	defer closeLog()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.DeviceToken == "" && !*dryRun {
		return errors.New("not activated — run 'inlinr activate' first")
	}

	home, err := config.Home()
	if err != nil {
		return err
	}

	state := ai.LoadState(home)

	// Checked before the lock and before touching the filesystem, because the
	// whole point is to make a hook that fires on every tool call nearly free.
	// The hooks that mean "this is over" — Stop, SessionEnd — pass no throttle
	// and always run, so nothing is ever left unsynced at the end.
	if *throttle > 0 && !state.LastSyncAt.IsZero() {
		if time.Since(state.LastSyncAt) < time.Duration(*throttle)*time.Second {
			return nil
		}
	}

	lock, err := ai.AcquireLock(home)
	if err != nil {
		if errors.Is(err, ai.ErrSyncBusy) {
			// Another editor is mid-sync. Its run covers the same transcripts,
			// so there is nothing to do and nothing to report.
			return nil
		}
		return err
	}
	defer lock.Release()

	dirs, err := ai.ClaudeHome()
	if err != nil {
		return err
	}
	paths, err := ai.TranscriptPaths(dirs, state.LastParsedAt)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		// Still record the attempt, so a throttled hook does not re-walk the
		// transcript directory on every tool call in a quiet session.
		if !*dryRun {
			state.LastSyncAt = time.Now()
			_ = ai.SaveState(home, state)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var (
		newest   = state.LastParsedAt
		enqueued int
		skipped  int
	)

	qp, err := config.QueuePath()
	if err != nil {
		return err
	}
	q, err := queue.Open(qp)
	if err != nil {
		return err
	}
	defer q.Close()

	for _, path := range paths {
		session, err := ai.ParseTranscript(path, state.LastParsedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inlinr: skipping %s: %v\n", filepath.Base(path), err)
			continue
		}

		// A session's project comes from its working directory. Without a git
		// remote there is nothing to attribute the time to — most often a
		// conversation started in a scratch folder.
		folder := session.Cwd
		if *projectFolder != "" {
			folder = *projectFolder
		}
		remote := ai.GitRemote(folder)
		if remote == "" {
			skipped++
			continue
		}

		for _, beat := range session.Heartbeats(remote, *plugin) {
			if *dryRun {
				enqueued++
				continue
			}
			if err := q.Enqueue(ctx, beat); err != nil {
				return fmt.Errorf("enqueue: %w", err)
			}
			enqueued++
		}
		if session.LastSeen.After(newest) {
			newest = session.LastSeen
		}
	}

	if *dryRun {
		fmt.Printf("%d beat(s) from %d transcript(s); %d session(s) skipped (no git remote)\n",
			enqueued, len(paths), skipped)
		return nil
	}

	// Advance the watermark only after the beats are safely on the queue, so a
	// failure replays rather than silently drops a conversation.
	if newest.After(state.LastParsedAt) {
		if err := ai.SaveState(home, ai.State{LastParsedAt: newest, LastSyncAt: time.Now()}); err != nil {
			return err
		}
	}

	if enqueued == 0 {
		return nil
	}
	return flush(ctx, q, cfg)
}
