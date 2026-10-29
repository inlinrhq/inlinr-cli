package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/deps"
	"github.com/inlinrhq/inlinr-cli/internal/gitctx"
	"github.com/inlinrhq/inlinr-cli/internal/report"
)

// retryAfter is how soon a repository is looked at again when the check did
// not get an answer, or ran on a branch other than the default one.
const retryAfter = time.Hour

// runSyncDeps sends a repository's npm manifests when its owner follows its
// dependencies on inlinr. The heartbeat path spawns it, detached, at most
// once a day per repository; it can also be run by hand.
//
// Only the default branch is read: a feature branch's package.json is a
// draft, and reporting its gaps as the project's would be wrong the moment
// the branch is abandoned.
func runSyncDeps(args []string) error {
	fs := flag.NewFlagSet("sync-deps", flag.ContinueOnError)
	dir := fs.String("dir", ".", "folder inside the repository to read")
	remoteFlag := fs.String("remote", "", "git remote the project is known by (default: read from .git)")
	force := fs.Bool("force", false, "check now, even if checked recently")
	dryRun := fs.Bool("dry-run", false, "print what would be sent, send nothing")
	quiet := fs.Bool("quiet", false, "print nothing on success")
	configPath := fs.String("config", "", "path to config.toml")
	logFile := fs.String("log-file", "", "append errors to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	closeLog, err := openLogFile(*logFile)
	if err != nil {
		return err
	}
	defer closeLog()
	say := func(format string, a ...any) {
		if !*quiet {
			fmt.Printf(format+"\n", a...)
		}
	}

	root := gitctx.Root(*dir)
	if root == "" {
		say("not inside a git repository, nothing to read")
		return nil
	}
	git := gitctx.Resolve(root)
	remote := *remoteFlag
	if remote == "" {
		remote = git.Remote
	}
	if remote == "" {
		say("no git remote, so no inlinr project to send to")
		return nil
	}

	home, err := config.Home()
	if err != nil {
		return err
	}
	now := time.Now()
	if !*force && !*dryRun && !deps.Due(home, remote, now) {
		return nil
	}

	if !onDefaultBranch(root, git.Branch) {
		_ = deps.Defer(home, remote, now.Add(retryAfter))
		say("on branch %q, not the default one; only the default branch is read", git.Branch)
		return nil
	}

	upload, ok := deps.Collect(root)
	if !ok {
		_ = deps.Defer(home, remote, now.Add(24*time.Hour))
		say("no package.json found")
		return nil
	}
	upload.ProjectGitRemote = remote

	if *dryRun {
		installed := 0
		for _, m := range upload.Installed {
			installed += len(m)
		}
		fmt.Printf("would send %d manifests (root %s) and %d installed versions for %s\n",
			len(upload.Manifests), upload.RootPath, installed, remote)
		for _, m := range upload.Manifests {
			fmt.Printf("  %s\n", m.Path)
		}
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.DeviceToken == "" {
		return report.Quiet("not activated, run 'inlinr activate' first")
	}
	// Claimed before the network call, so a slow server cannot make every
	// heartbeat in the meantime spawn another check.
	_ = deps.Defer(home, remote, now.Add(retryAfter))

	client := api.New(config.APIURL, cfg.Auth.DeviceToken, "inlinr-cli/"+Version)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	status, err := client.GetDepsStatus(ctx, remote)
	if err != nil {
		return fmt.Errorf("deps status: %w", err)
	}
	next := time.Duration(status.CheckAfter) * time.Second
	if next < retryAfter {
		next = retryAfter
	}
	if !status.Wanted {
		_ = deps.Defer(home, remote, now.Add(next))
		say("dependency tracking is off for this project on inlinr, nothing sent")
		return nil
	}
	if err := client.SendManifests(ctx, upload); err != nil {
		return fmt.Errorf("send manifests: %w", err)
	}
	_ = deps.Defer(home, remote, now.Add(next))
	say("sent %d manifests for %s", len(upload.Manifests), remote)
	return nil
}

// onDefaultBranch: the branch origin/HEAD names, or main/master when the
// clone never recorded one. A detached HEAD is not the default branch.
func onDefaultBranch(root, branch string) bool {
	if branch == "" {
		return false
	}
	if def := gitctx.DefaultBranch(root); def != "" {
		return branch == def
	}
	return branch == "main" || branch == "master"
}

// maybeSyncDeps is called on every heartbeat. It costs one small file read
// when nothing is due, and otherwise starts `inlinr sync-deps` in the
// background so the plugin waiting on this process is never held up by it.
func maybeSyncDeps(cfg config.Config, entity, remote string) {
	if !cfg.Behavior.DepsSync || !filepath.IsAbs(entity) {
		return
	}
	home, err := config.Home()
	if err != nil {
		return
	}
	now := time.Now()
	if !deps.Due(home, remote, now) {
		return
	}
	root := gitctx.Root(filepath.Dir(entity))
	if root == "" {
		return
	}
	// Claim the slot before spawning: the next heartbeat must not start a
	// second check while this one runs.
	if deps.Defer(home, remote, now.Add(retryAfter)) != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "sync-deps", "--dir", root, "--remote", remote, "--force", "--quiet")
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}
