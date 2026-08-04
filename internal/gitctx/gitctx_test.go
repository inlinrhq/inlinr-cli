package gitctx

import (
	"os"
	"path/filepath"
	"testing"
)

const config = `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:acme/app.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func TestResolveNormalClone(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "config"), config)
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")

	got := Resolve(repo)
	if got.Remote != "git@github.com:acme/app.git" {
		t.Errorf("remote = %q", got.Remote)
	}
	if got.Branch != "main" {
		t.Errorf("branch = %q, want main", got.Branch)
	}
}

func TestResolveWalksUp(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "config"), config)
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	nested := filepath.Join(repo, "apps", "api", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if got := Resolve(nested); got.Remote == "" {
		t.Error("expected the remote to be found from a nested directory")
	}
}

func TestResolveWorktree(t *testing.T) {
	// The `app-pr755` case: a worktree must inherit the parent's remote while
	// keeping its own branch. Before this, a worktree yielded no remote at all
	// and its activity was dropped.
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	worktree := filepath.Join(root, "app-pr755")

	write(t, filepath.Join(repo, ".git", "config"), config)
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")

	wtGitDir := filepath.Join(repo, ".git", "worktrees", "app-pr755")
	write(t, filepath.Join(wtGitDir, "HEAD"), "ref: refs/heads/pr755\n")
	write(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	write(t, filepath.Join(worktree, ".git"), "gitdir: "+wtGitDir+"\n")

	got := Resolve(worktree)
	if got.Remote != "git@github.com:acme/app.git" {
		t.Errorf("remote = %q, want the parent repository's", got.Remote)
	}
	if got.Branch != "pr755" {
		t.Errorf("branch = %q, want pr755", got.Branch)
	}
}

func TestResolveWorktreeRelativeGitdir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	worktree := filepath.Join(root, "app-wt")

	write(t, filepath.Join(repo, ".git", "config"), config)
	wtGitDir := filepath.Join(repo, ".git", "worktrees", "app-wt")
	write(t, filepath.Join(wtGitDir, "HEAD"), "ref: refs/heads/feature\n")
	write(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	write(t, filepath.Join(worktree, ".git"), "gitdir: ../app/.git/worktrees/app-wt\n")

	got := Resolve(worktree)
	if got.Remote != "git@github.com:acme/app.git" {
		t.Errorf("remote = %q", got.Remote)
	}
	if got.Branch != "feature" {
		t.Errorf("branch = %q, want feature", got.Branch)
	}
}

func TestResolveFallsBackToFirstRemote(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "config"),
		"[remote \"upstream\"]\n\turl = https://gitlab.com/g/p.git\n")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/trunk\n")

	if got := Resolve(repo); got.Remote != "https://gitlab.com/g/p.git" {
		t.Errorf("remote = %q", got.Remote)
	}
}

func TestResolveDetachedHead(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "config"), config)
	write(t, filepath.Join(repo, ".git", "HEAD"), "9f2c1ab0c0ffee\n")

	if got := Resolve(repo); got.Branch != "" {
		t.Errorf("branch = %q, want empty for a detached HEAD", got.Branch)
	}
}

func TestResolveOutsideRepository(t *testing.T) {
	// t.TempDir() sits under the OS temp root, which is not a repository.
	if got := Resolve(t.TempDir()); got.Remote != "" {
		t.Errorf("remote = %q, want empty outside a repository", got.Remote)
	}
}
