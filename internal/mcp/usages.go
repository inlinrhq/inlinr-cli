package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Folders find_usages never enters: installed packages, build output and
// caches hold the package's own code, not this codebase's use of it.
var skipDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "out": true,
	"coverage": true, "vendor": true, "storybook-static": true,
}

// Files that can import a JavaScript package.
var sourceExt = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".vue": true, ".svelte": true, ".astro": true,
}

// A generated bundle is not where anyone fixes a breaking change.
const maxSourceBytes = 1 << 20

// A line longer than this is cut: minified code says nothing useful.
const maxLineChars = 200

// Usage is one line that imports the package.
type Usage struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Text      string `json:"text"`
	Specifier string `json:"specifier"`
}

// specifierPattern matches the package, or a subpath of it, as a quoted
// module specifier: "react", 'react/jsx-runtime', `next/navigation`.
func specifierPattern(name string) *regexp.Regexp {
	return regexp.MustCompile("[\"'`](" + regexp.QuoteMeta(name) + "(?:/[^\"'`\\s]*)?)[\"'`]")
}

// importWords are what a line that loads a module says beside the specifier.
var importWords = regexp.MustCompile(`\b(?:import|from|require|export)\b`)

// FindUsages walks root for lines that import name, at most max of them.
// It also says whether more were found than returned, and in how many
// files the package is imported.
func FindUsages(ctx context.Context, root, name string, max int) ([]Usage, bool, int, error) {
	re := specifierPattern(name)
	var out []Usage
	files := map[string]bool{}
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable folder is skipped, not the whole search.
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExt[filepath.Ext(path)] || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxSourceBytes {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), maxSourceBytes)
		for n := 1; sc.Scan(); n++ {
			text := sc.Text()
			m := re.FindStringSubmatch(text)
			if m == nil || !importWords.MatchString(text) {
				continue
			}
			files[rel] = true
			if len(out) >= max {
				truncated = true
				continue
			}
			text = strings.TrimSpace(text)
			if len(text) > maxLineChars {
				text = text[:maxLineChars] + "…"
			}
			out = append(out, Usage{File: rel, Line: n, Text: text, Specifier: m[1]})
		}
		return nil
	})
	return out, truncated, len(files), err
}

func (t *Tools) findUsages(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Name string `json:"name"`
		Dir  string `json:"dir"`
		Max  int    `json:"max"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if !packageName.MatchString(a.Name) {
		return failure("%q is not an npm package name.", a.Name)
	}
	max := a.Max
	if max <= 0 {
		max = 200
	}
	if max > 1000 {
		max = 1000
	}
	root := t.Dir
	if a.Dir != "" {
		if filepath.IsAbs(a.Dir) {
			root = a.Dir
		} else {
			root = filepath.Join(t.Dir, a.Dir)
		}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return failure("%s is not a folder.", root)
	}
	usages, truncated, files, err := FindUsages(ctx, root, a.Name, max)
	if err != nil && !errors.Is(err, context.Canceled) {
		return failure("Could not search %s: %v", root, err)
	}
	bySpecifier := map[string]int{}
	for _, u := range usages {
		bySpecifier[u.Specifier]++
	}
	if usages == nil {
		usages = []Usage{}
	}
	out, _ := json.MarshalIndent(map[string]any{
		"name":         a.Name,
		"root":         root,
		"files":        files,
		"by_specifier": bySpecifier,
		"usages":       usages,
		"truncated":    truncated,
	}, "", "  ")
	return Result{Content: []Content{{Type: "text", Text: string(out)}}}
}
