package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
	"github.com/inlinrhq/inlinr-cli/internal/commits"
	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/gitctx"
)

// How far back a sync looks. Commits older than this are assumed already sent —
// the endpoint is idempotent, so re-sending is harmless, but reading a decade
// of history on every run is not.
const defaultSinceDays = 30

func runSyncCommits(args []string) int {
	fs := flag.NewFlagSet("sync-commits", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository to read")
	days := fs.Int("days", defaultSinceDays, "how many days of history to read")
	limit := fs.Int("limit", 1000, "maximum commits per run")
	dryRun := fs.Bool("dry-run", false, "print what would be sent, send nothing")
	configPath := fs.String("config", "", "path to inlinr.toml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	git := gitctx.Resolve(*dir)
	if git.Remote == "" {
		// Not a git repository, or one with no remote. There is no project to
		// attribute commits to, and guessing would attach them to the wrong one.
		fmt.Fprintln(os.Stderr, "no git remote found — nothing to sync")
		return 0
	}

	since := time.Now().AddDate(0, 0, -*days)
	list, err := commits.Read(*dir, since, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read commits: %v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Printf("no commits in the last %d days\n", *days)
		return 0
	}

	if *dryRun {
		var ins, del int
		for _, c := range list {
			ins += c.Insertions
			del += c.Deletions
		}
		fmt.Printf("would send %d commits for %s (+%d/-%d)\n",
			len(list), git.Remote, ins, del)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil || cfg.Auth.DeviceToken == "" {
		fmt.Fprintln(os.Stderr, "not activated — run 'inlinr activate' first")
		return 1
	}

	client := api.New(config.APIURL, cfg.Auth.DeviceToken, "inlinr-cli/"+Version)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := client.SendCommits(ctx, git.Remote, list)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send commits: %v\n", err)
		return 1
	}
	if res.Reason != "" {
		fmt.Printf("%s (%s)\n", res.Reason, git.Remote)
		return 0
	}
	fmt.Printf("%d new, %d backfilled\n", res.Accepted, res.Backfilled)
	return 0
}
