// Package deps reads a working copy's npm manifests for inlinr's dependency
// tracking, and the versions its lockfile installed.
//
// The server reads public GitHub repositories itself. Everything else, a
// private repository or one hosted anywhere but GitHub, can only be read
// here, from files already on the machine: inlinr never asks GitHub for the
// `repo` scope. What leaves the machine is the dependency fields of each
// package.json, the pnpm workspace file when there is one, and one version
// per direct dependency. Not the source, not the file tree, not the
// transitive tree, and only for a project its owner chose to follow in the
// dashboard (the server says so before anything is read; see sync-deps).
package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RootCandidates is where a repository's manifest is looked for, in order.
// Same list as the server's GitHub read (src/lib/deps/manifest.ts), so both
// pick the same root.
var RootCandidates = []string{
	"package.json",
	"apps/web/package.json",
	"packages/app/package.json",
	"frontend/package.json",
	"client/package.json",
	"server/package.json",
}

// MaxWorkspaceManifests matches the server's ceiling.
const MaxWorkspaceManifests = 60

// maxFileBytes skips anything too large to be a hand-written manifest.
const maxFileBytes = 256 * 1024

// keptFields are the only package.json keys sent. Scripts, repository URLs,
// author fields and the rest stay on the machine.
var keptFields = []string{
	"name",
	"version",
	"dependencies",
	"devDependencies",
	"peerDependencies",
	"optionalDependencies",
	"workspaces",
	"catalog",
	"catalogs",
}

// File is one manifest, by its path relative to the repository root.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Upload is the body of POST /api/v1/deps/manifests.
type Upload struct {
	ProjectGitRemote string                       `json:"project_git_remote"`
	RootPath         string                       `json:"root_path"`
	Manifests        []File                       `json:"manifests"`
	PnpmWorkspace    *string                      `json:"pnpm_workspace,omitempty"`
	Installed        map[string]map[string]string `json:"installed,omitempty"`
}

type manifest struct {
	path string
	deps map[string]string // name → range, every dependency block
	raw  map[string]json.RawMessage
}

// Collect reads the manifests under repoRoot. It returns ok=false when no
// manifest is found, which is ordinary: not every repository is a Node one.
func Collect(repoRoot string) (Upload, bool) {
	var root *manifest
	for _, candidate := range RootCandidates {
		if m := readManifest(repoRoot, candidate); m != nil {
			root = m
			break
		}
	}
	if root == nil {
		return Upload{}, false
	}
	base := dirOf(root.path)

	var pnpmText *string
	var patterns []string
	if data, err := readSmall(filepath.Join(repoRoot, filepath.FromSlash(joinPath(base, "pnpm-workspace.yaml")))); err == nil {
		text := string(data)
		pnpmText = &text
		patterns = append(patterns, pnpmPackages(text)...)
	}
	patterns = append(patterns, workspacePatterns(root.raw["workspaces"])...)

	manifests := []*manifest{root}
	for _, dir := range expandWorkspaces(repoRoot, base, patterns) {
		if m := readManifest(repoRoot, joinPath(dir, "package.json")); m != nil {
			manifests = append(manifests, m)
		}
	}

	up := Upload{RootPath: root.path, PnpmWorkspace: pnpmText}
	for _, m := range manifests {
		content, err := json.Marshal(m.raw)
		if err != nil {
			continue
		}
		up.Manifests = append(up.Manifests, File{Path: m.path, Content: string(content)})
	}
	up.Installed = installedVersions(repoRoot, base, manifests)
	return up, true
}

func readSmall(path string) ([]byte, error) {
	return readSized(path, maxFileBytes)
}

func readSized(path string, max int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Size() > max {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(path)
}

// readManifest parses one package.json and keeps only the fields sent.
func readManifest(repoRoot, rel string) *manifest {
	data, err := readSmall(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil
	}
	kept := map[string]json.RawMessage{}
	for _, k := range keptFields {
		if v, ok := all[k]; ok {
			kept[k] = v
		}
	}
	deps := map[string]string{}
	for _, block := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		var m map[string]any
		if json.Unmarshal(kept[block], &m) != nil {
			continue
		}
		for name, v := range m {
			if s, ok := v.(string); ok {
				if _, seen := deps[name]; !seen {
					deps[name] = strings.TrimSpace(s)
				}
			}
		}
	}
	return &manifest{path: rel, deps: deps, raw: kept}
}

// workspacePatterns reads `workspaces` as an array or as { packages: [...] }.
func workspacePatterns(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Packages
	}
	return nil
}

// pnpmPackages reads the `packages:` list of pnpm-workspace.yaml. Only the
// block form pnpm documents (`  - 'apps/*'`); anything else is ignored.
func pnpmPackages(text string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "-") {
			in = strings.HasPrefix(trimmed, "packages:")
			continue
		}
		if !in {
			continue
		}
		item, ok := strings.CutPrefix(trimmed, "-")
		if !ok {
			continue
		}
		item = strings.TrimSpace(item)
		if i := strings.Index(item, " #"); i >= 0 {
			item = strings.TrimSpace(item[:i])
		}
		item = strings.Trim(item, `'"`)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// expandWorkspaces turns workspace patterns into folders, relative to the
// repository root, sorted and capped like the server's read.
func expandWorkspaces(repoRoot, base string, patterns []string) []string {
	found := map[string]bool{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "!") {
			continue
		}
		p = strings.TrimPrefix(strings.TrimSuffix(p, "/"), "./")
		if strings.Contains(p, "..") {
			continue
		}
		rel := joinPath(base, p)
		if prefix, _, ok := strings.Cut(rel, "**"); ok {
			walkPackages(repoRoot, strings.TrimSuffix(prefix, "/"), 3, found)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, m := range matches {
			r, err := filepath.Rel(repoRoot, m)
			if err != nil {
				continue
			}
			found[filepath.ToSlash(r)] = true
		}
	}
	delete(found, base)
	delete(found, ".")
	var dirs []string
	for d := range found {
		if skipDir(d) {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(d), "package.json")); err != nil {
			continue
		}
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	if len(dirs) > MaxWorkspaceManifests {
		dirs = dirs[:MaxWorkspaceManifests]
	}
	return dirs
}

func walkPackages(repoRoot, rel string, depth int, found map[string]bool) {
	if depth < 0 {
		return
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := joinPath(rel, e.Name())
		if skipDir(child) {
			continue
		}
		found[child] = true
		walkPackages(repoRoot, child, depth-1, found)
	}
}

func skipDir(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == "node_modules" || strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func joinPath(dir, rest string) string {
	if dir == "" {
		return rest
	}
	return dir + "/" + rest
}
