// Package gitctx resolves the git remote and branch of a directory by reading
// the git metadata directly.
//
// Editor plugins get this from their host's git integration, but `inlinr
// sync-ai` runs with nothing but a working directory taken from an assistant
// transcript, so it has to do the lookup itself.
//
// Handles worktrees: in a `git worktree` checkout `.git` is a *file* pointing
// at `<repo>/.git/worktrees/<name>`, whose `commondir` points back at the
// shared directory holding `config`. The remote therefore comes from the parent
// repository while the branch stays worktree-local — same project, different
// branch, which is exactly right. Mirrors inlinr-vscode/src/git-fs.ts.
package gitctx

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Context is what a heartbeat needs to know about a directory.
type Context struct {
	Remote string
	Branch string
}

// maxWalkUp bounds the search for `.git`. A repository nested deeper than this
// is not a real case, and an unbounded walk on a bad path is a hang.
const maxWalkUp = 40

// Resolve reads the remote and branch for `dir`, walking up to find `.git`.
// Missing pieces come back empty rather than as errors — a directory outside
// any repository is an ordinary situation, not a failure.
func Resolve(dir string) Context {
	gitPath := findGitPath(dir)
	if gitPath == "" {
		return Context{}
	}
	gitDir := resolveGitDir(gitPath)
	if gitDir == "" {
		return Context{}
	}
	return Context{
		Remote: readOriginURL(filepath.Join(commonDir(gitDir), "config")),
		Branch: readHeadBranch(filepath.Join(gitDir, "HEAD")),
	}
}

func findGitPath(start string) string {
	dir := start
	for i := 0; i < maxWalkUp; i++ {
		candidate := filepath.Join(dir, ".git")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// resolveGitDir returns the `.git` directory, following the `gitdir:` pointer
// when `.git` is a file (worktree or submodule).
func resolveGitDir(gitPath string) string {
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return gitPath
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "gitdir:")
		if !ok {
			continue
		}
		target := strings.TrimSpace(rest)
		if target == "" {
			return ""
		}
		if filepath.IsAbs(target) {
			return filepath.Clean(target)
		}
		return filepath.Clean(filepath.Join(filepath.Dir(gitPath), target))
	}
	return ""
}

// commonDir returns the shared repository directory holding `config`. For a
// normal clone that is the git dir itself.
func commonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		return gitDir
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(gitDir, target))
}

// readOriginURL pulls the url of `[remote "origin"]` out of a git config,
// falling back to the first remote defined.
func readOriginURL(configPath string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	var (
		section     string
		firstRemote string
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section = remoteSectionName(line)
			continue
		}
		if section == "" {
			continue
		}
		value, ok := strings.CutPrefix(line, "url")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value, ok = strings.CutPrefix(value, "=")
		if !ok {
			continue
		}
		url := strings.TrimSpace(value)
		if url == "" {
			continue
		}
		if section == "origin" {
			return url
		}
		if firstRemote == "" {
			firstRemote = url
		}
	}
	return firstRemote
}

// remoteSectionName returns the remote name for a `[remote "name"]` header,
// or "" for any other section.
func remoteSectionName(header string) string {
	rest, ok := strings.CutPrefix(header, "[remote ")
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(strings.TrimSpace(rest), "]")
	return strings.Trim(rest, `"`)
}

// readHeadBranch returns the branch named in HEAD, or "" when detached.
func readHeadBranch(headPath string) string {
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	rest, ok := strings.CutPrefix(head, "ref:")
	if !ok {
		return ""
	}
	ref := strings.TrimSpace(rest)
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(branch)
}
