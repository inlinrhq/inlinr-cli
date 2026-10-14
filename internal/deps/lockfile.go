package deps

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Installed versions come from whichever lockfile sits next to the root
// manifest: npm, pnpm, Yarn (classic and berry) or Bun. Direct dependencies
// only: each manifest's declared names are looked up, nothing else is read
// out of the lockfile.

// maxLockBytes: lockfiles of large monorepos run to tens of megabytes. Past
// this one, installed versions are skipped rather than slowing the editor.
const maxLockBytes = 32 * 1024 * 1024

var plainVersion = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,64}$`)

// cleanVersion drops pnpm's peer suffix (`19.1.0(react@19.1.0)`) and returns
// "" for anything that is not a registry version (links, git, file paths).
func cleanVersion(v string) string {
	v = strings.Trim(strings.TrimSpace(v), `'"`)
	if i := strings.Index(v, "("); i >= 0 {
		v = v[:i]
	}
	if !plainVersion.MatchString(v) || v[0] < '0' || v[0] > '9' {
		return ""
	}
	return v
}

func installedVersions(repoRoot, base string, manifests []*manifest) map[string]map[string]string {
	var lookup func(dir, name, rng string) string
	for _, lf := range []struct {
		file  string
		parse func([]byte) func(dir, name, rng string) string
	}{
		{"pnpm-lock.yaml", func(b []byte) func(string, string, string) string { return pnpmLookup(string(b)) }},
		{"package-lock.json", npmLookup},
		{"bun.lock", bunLookup},
		{"yarn.lock", func(b []byte) func(string, string, string) string { return yarnLookup(string(b)) }},
	} {
		data, err := readLock(filepath.Join(repoRoot, filepath.FromSlash(joinPath(base, lf.file))))
		if err == nil {
			lookup = lf.parse(data)
			break
		}
	}
	if lookup == nil {
		return nil
	}

	out := map[string]map[string]string{}
	for _, m := range manifests {
		dir := dirOf(m.path)
		// Lockfile paths are relative to the lockfile's folder, the base.
		relDir := strings.TrimPrefix(strings.TrimPrefix(dir, base), "/")
		for name, rng := range m.deps {
			if v := cleanVersion(lookup(relDir, name, rng)); v != "" {
				if out[m.path] == nil {
					out[m.path] = map[string]string{}
				}
				out[m.path][name] = v
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readLock(path string) ([]byte, error) {
	data, err := readSized(path, maxLockBytes)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// npmLookup reads package-lock.json v2/v3 (`packages`), falling back to v1
// (`dependencies`, root only).
func npmLookup(data []byte) func(dir, name, rng string) string {
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return func(string, string, string) string { return "" }
	}
	return func(dir, name, _ string) string {
		if dir != "" {
			if p, ok := lock.Packages[dir+"/node_modules/"+name]; ok {
				return p.Version
			}
		}
		if p, ok := lock.Packages["node_modules/"+name]; ok {
			return p.Version
		}
		if dir == "" {
			return lock.Dependencies[name].Version
		}
		return ""
	}
}

// pnpmLookup reads the `importers:` section of pnpm-lock.yaml, v6 and later
// (`version:` under each entry) and v5 (`name: version` on one line).
func pnpmLookup(text string) func(dir, name, rng string) string {
	byImporter := map[string]map[string]string{}
	var importer string
	inImporters, inDeps := false, false
	var entry string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if indent == 0 {
			inImporters = trimmed == "importers:"
			importer, entry, inDeps = "", "", false
			continue
		}
		if !inImporters {
			continue
		}
		switch {
		case indent == 2:
			importer = strings.Trim(strings.TrimSuffix(trimmed, ":"), `'"`)
			if importer == "." {
				importer = ""
			}
			inDeps, entry = false, ""
		case indent == 4:
			key := strings.TrimSuffix(trimmed, ":")
			inDeps = key == "dependencies" || key == "devDependencies" || key == "optionalDependencies"
			entry = ""
		case indent == 6 && inDeps:
			key, value, _ := strings.Cut(trimmed, ":")
			entry = strings.Trim(strings.TrimSpace(key), `'"`)
			if v := strings.TrimSpace(value); v != "" {
				set(byImporter, importer, entry, v)
			}
		case indent == 8 && inDeps && entry != "":
			if v, ok := strings.CutPrefix(trimmed, "version:"); ok {
				set(byImporter, importer, entry, strings.TrimSpace(v))
			}
		}
	}
	return func(dir, name, _ string) string {
		return byImporter[dir][name]
	}
}

func set(m map[string]map[string]string, a, b, v string) {
	if m[a] == nil {
		m[a] = map[string]string{}
	}
	m[a][b] = v
}

// bunLookup reads bun.lock (JSON with trailing commas). Its `packages` map is
// keyed by the hoisted name, or `<workspace>/<name>` when a workspace has its
// own copy; each value starts with "name@version".
func bunLookup(data []byte) func(dir, name, rng string) string {
	var lock struct {
		Workspaces map[string]struct {
			Name string `json:"name"`
		} `json:"workspaces"`
		Packages map[string][]json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(stripTrailingCommas(data), &lock) != nil {
		return func(string, string, string) string { return "" }
	}
	version := func(key, name string) string {
		entry := lock.Packages[key]
		if len(entry) == 0 {
			return ""
		}
		var spec string
		if json.Unmarshal(entry[0], &spec) != nil {
			return ""
		}
		v, ok := strings.CutPrefix(spec, name+"@")
		if !ok {
			return ""
		}
		return v
	}
	return func(dir, name, _ string) string {
		if dir != "" {
			if ws, ok := lock.Workspaces[dir]; ok && ws.Name != "" {
				if v := version(ws.Name+"/"+name, name); v != "" {
					return v
				}
			}
		}
		return version(name, name)
	}
}

// stripTrailingCommas removes a comma that only whitespace separates from a
// closing bracket, outside strings.
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\n' || data[j] == '\r' || data[j] == '\t') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// yarnLookup reads yarn.lock, classic (`version "1.2.3"`) and berry
// (`version: 1.2.3`, specs written `name@npm:range`). An entry header lists
// every spec it satisfies, so a dependency is found by its declared range.
func yarnLookup(text string) func(dir, name, rng string) string {
	bySpec := map[string]string{}
	var specs []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			specs = specs[:0]
			header := strings.TrimSuffix(line, ":")
			for _, s := range strings.Split(header, ",") {
				s = strings.Trim(strings.TrimSpace(s), `"`)
				if s != "" {
					specs = append(specs, normaliseYarnSpec(s))
				}
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		v, ok := strings.CutPrefix(trimmed, "version")
		if !ok || len(specs) == 0 {
			continue
		}
		v = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), ":")), `"`)
		for _, s := range specs {
			bySpec[s] = v
		}
		specs = specs[:0]
	}
	return func(_ string, name, rng string) string {
		return bySpec[name+"@"+rng]
	}
}

// normaliseYarnSpec turns berry's `react@npm:^19.0.0` into `react@^19.0.0`.
func normaliseYarnSpec(spec string) string {
	at := strings.LastIndex(spec, "@")
	if at <= 0 {
		return spec
	}
	return spec[:at+1] + strings.TrimPrefix(spec[at+1:], "npm:")
}
