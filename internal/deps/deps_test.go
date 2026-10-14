package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(up Upload) []string {
	var out []string
	for _, m := range up.Manifests {
		out = append(out, m.Path)
	}
	return out
}

func TestCollectKeepsOnlyDependencyFields(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"acme","scripts":{"deploy":"secret.sh"},"repository":"x","dependencies":{"react":"^19.0.0"}}`)
	up, ok := Collect(root)
	if !ok {
		t.Fatal("no manifest found")
	}
	content := up.Manifests[0].Content
	if strings.Contains(content, "scripts") || strings.Contains(content, "repository") {
		t.Fatalf("sent more than dependency fields: %s", content)
	}
	if !strings.Contains(content, `"react":"^19.0.0"`) {
		t.Fatalf("dependencies missing: %s", content)
	}
}

func TestCollectNoManifest(t *testing.T) {
	if _, ok := Collect(t.TempDir()); ok {
		t.Fatal("found a manifest in an empty folder")
	}
}

func TestCollectWorkspaces(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"workspaces":["apps/*","packages/**"]}`)
	write(t, root, "apps/web/package.json", `{"name":"web"}`)
	write(t, root, "apps/docs/README.md", `no manifest here`)
	write(t, root, "packages/ui/package.json", `{"name":"@acme/ui"}`)
	write(t, root, "packages/ui/node_modules/x/package.json", `{"name":"x"}`)
	up, _ := Collect(root)
	want := []string{"package.json", "apps/web/package.json", "packages/ui/package.json"}
	if got := paths(up); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCollectPnpmWorkspace(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"acme"}`)
	write(t, root, "pnpm-workspace.yaml", "packages:\n  - 'apps/*' # the apps\n  - \"!apps/legacy\"\ncatalog:\n  react: ^19.1.0\n")
	write(t, root, "apps/web/package.json", `{"name":"web","dependencies":{"react":"catalog:"}}`)
	write(t, root, "apps/legacy/package.json", `{"name":"legacy"}`)
	up, _ := Collect(root)
	// Negations are not applied: the server lists the same folders and
	// lets the owner skip one, so both reads agree.
	if got := paths(up); !reflect.DeepEqual(got, []string{"package.json", "apps/legacy/package.json", "apps/web/package.json"}) {
		t.Fatalf("got %v", got)
	}
	if up.PnpmWorkspace == nil || !strings.Contains(*up.PnpmWorkspace, "catalog:") {
		t.Fatal("pnpm workspace file not sent")
	}
}

func TestCollectFallsBackToAppFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "apps/web/package.json", `{"name":"web"}`)
	up, _ := Collect(root)
	if up.RootPath != "apps/web/package.json" {
		t.Fatalf("root %q", up.RootPath)
	}
}

func TestInstalledNpm(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"workspaces":["apps/*"],"dependencies":{"react":"^19.0.0"}}`)
	write(t, root, "apps/web/package.json", `{"dependencies":{"react":"^18.2.0","zod":"^3.0.0"}}`)
	write(t, root, "package-lock.json", `{"lockfileVersion":3,"packages":{
		"node_modules/react":{"version":"19.1.0"},
		"node_modules/zod":{"version":"3.25.1"},
		"node_modules/lodash":{"version":"4.17.21"},
		"apps/web/node_modules/react":{"version":"18.3.1"}}}`)
	up, _ := Collect(root)
	want := map[string]map[string]string{
		"package.json":          {"react": "19.1.0"},
		"apps/web/package.json": {"react": "18.3.1", "zod": "3.25.1"},
	}
	if !reflect.DeepEqual(up.Installed, want) {
		t.Fatalf("got %v", up.Installed)
	}
}

func TestInstalledPnpm(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"devDependencies":{"vitest":"^4.0.0","@types/node":"^24"}}`)
	write(t, root, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	write(t, root, "apps/web/package.json", `{"dependencies":{"react":"^19.0.0","@acme/ui":"workspace:*"}}`)
	write(t, root, "pnpm-lock.yaml", `lockfileVersion: '9.0'

importers:

  .:
    devDependencies:
      '@types/node':
        specifier: ^24
        version: 24.3.0
      vitest:
        specifier: ^4.0.0
        version: 4.0.2(@types/node@24.3.0)

  apps/web:
    dependencies:
      '@acme/ui':
        specifier: workspace:*
        version: link:../../packages/ui
      react:
        specifier: ^19.0.0
        version: 19.1.0

packages:

  react@19.1.0:
    resolution: {integrity: sha512-x}
`)
	up, _ := Collect(root)
	want := map[string]map[string]string{
		"package.json":          {"@types/node": "24.3.0", "vitest": "4.0.2"},
		"apps/web/package.json": {"react": "19.1.0"},
	}
	if !reflect.DeepEqual(up.Installed, want) {
		t.Fatalf("got %v", up.Installed)
	}
}

func TestInstalledYarn(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"dependencies":{"react":"^19.0.0","@scope/pkg":"~1.2.0"}}`)
	write(t, root, "yarn.lock", `# yarn lockfile v1

"@scope/pkg@~1.2.0":
  version "1.2.5"

react@^19.0.0, react@^19.1.0:
  version "19.1.0"
`)
	up, _ := Collect(root)
	want := map[string]map[string]string{"package.json": {"react": "19.1.0", "@scope/pkg": "1.2.5"}}
	if !reflect.DeepEqual(up.Installed, want) {
		t.Fatalf("got %v", up.Installed)
	}
}

func TestInstalledYarnBerry(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"dependencies":{"react":"^19.0.0"}}`)
	write(t, root, "yarn.lock", `__metadata:
  version: 8

"react@npm:^19.0.0":
  version: 19.1.0
  resolution: "react@npm:19.1.0"
`)
	up, _ := Collect(root)
	if up.Installed["package.json"]["react"] != "19.1.0" {
		t.Fatalf("got %v", up.Installed)
	}
}

func TestInstalledBun(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"workspaces":["apps/*"],"dependencies":{"react":"^19.0.0"}}`)
	write(t, root, "apps/web/package.json", `{"name":"web","dependencies":{"react":"^18.0.0"}}`)
	write(t, root, "bun.lock", `{
  "lockfileVersion": 1,
  "workspaces": {
    "": { "name": "acme", },
    "apps/web": { "name": "web", },
  },
  "packages": {
    "react": ["react@19.1.0", "", {}, "sha512-x"],
    "web/react": ["react@18.3.1", "", {}, "sha512-y"],
  },
}`)
	up, _ := Collect(root)
	want := map[string]map[string]string{
		"package.json":          {"react": "19.1.0"},
		"apps/web/package.json": {"react": "18.3.1"},
	}
	if !reflect.DeepEqual(up.Installed, want) {
		t.Fatalf("got %v", up.Installed)
	}
}

func TestUploadJSONShape(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"dependencies":{"react":"^19.0.0"}}`)
	up, _ := Collect(root)
	up.ProjectGitRemote = "git@github.com:acme/app.git"
	data, _ := json.Marshal(up)
	for _, key := range []string{`"project_git_remote"`, `"root_path":"package.json"`, `"manifests"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("%s missing from %s", key, data)
		}
	}
	if strings.Contains(string(data), "pnpm_workspace") {
		t.Fatalf("empty pnpm_workspace sent: %s", data)
	}
}

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{
		"19.1.0":                "19.1.0",
		"4.0.2(@types/node@24)": "4.0.2",
		"link:../ui":            "",
		"github:acme/x#abc":     "",
		"'1.0.0-beta.1'":        "1.0.0-beta.1",
		"file:../x.tgz":         "",
	} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDueAndDefer(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if !Due(home, "r", now) {
		t.Fatal("a repository never checked must be due")
	}
	if err := Defer(home, "r", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if Due(home, "r", now) {
		t.Fatal("deferred repository is due")
	}
	if !Due(home, "other", now) {
		t.Fatal("deferring one repository deferred another")
	}
	if !Due(home, "r", now.Add(2*time.Hour)) {
		t.Fatal("not due after its time")
	}
}
