# inlinr-cli

> **Reprise de session** — sur le poste local, lis d'abord `../PROGRESS.md` (racine du
> workspace, absent d'un clone seul) : il tient l'état du chantier en cours. Mets-le à jour
> après chaque modification.

Go binary that the VS Code extension spawns to send heartbeats. Owns: auth token storage, offline SQLite queue, batch upload, HTTP retry. The extension never sends a beat itself (it only reports its own failures); the Claude Code plugin does not use this binary at all.

AI assistant transcripts are **not** parsed here. The Claude Code plugin
(`inlinrhq/inlinr-claude-code`, `src/transcript.ts` + `src/api.ts`) reads them
and posts to `/api/v1/heartbeats` itself. This binary used to carry a second
parser (`inlinr sync-ai`) that nothing called; it was removed so a fix only has
one place to go. `sync-ai` survives as a no-op that exits 0, because early
plugin releases ran it from a Claude Code Stop hook and a failing hook
interrupts the user's turn. A new assistant (Codex, Gemini, opencode) gets its
parser next to the Claude Code one, not here.

Sibling repos:
- `inlinrhq/inlinr-web` — server (ingest + dashboard). Source of truth for the wire contract below.
- `inlinrhq/inlinr-vscode` — VS Code plugin that spawns this binary.
- `inlinrhq/inlinr-claude-code` — Claude Code plugin. Does **not** spawn this
  binary and keeps its own token in `~/.inlinr/claude-code.json`; from this
  CLI's `config.toml` it reads only `error_reports` under `[behavior]`.
- No JetBrains or Neovim plugin exists yet (both are "considering" on the roadmap).

---

## Tech

- Go 1.25+ (the `modernc.org/sqlite` driver requires it; Go 1.21 is past end of life)
- `modernc.org/sqlite` — offline queue (pure Go, no cgo, so cross-compilation stays trivial)
- `github.com/BurntSushi/toml` — config file
- Stdlib HTTP, `flag`, `encoding/json`

No heavy CLI framework — Go's `flag` pkg is enough for the subcommand surface.

## Layout

```
cmd/inlinr/              # subcommand entry points
  main.go                # dispatcher
  activate.go            # Device flow
  heartbeat.go           # the hot path — plugins call this 100× a day
  doctor.go              # config dump + server ping
  signout.go             # revoke this device's token
  upgrade.go             # self-update from GitHub Releases (signed SHA256SUMS)
  synccommits.go         # `inlinr sync-commits` — commits from any git host
  syncdeps.go            # `inlinr sync-deps` — manifests for dependency tracking
  mcp.go                 # `inlinr mcp` — MCP server over stdio
internal/
  config/                # ~/.inlinr/config.toml load/save
  device/                # OAuth 2.0 Device Grant client
  api/                   # HTTP client for the ingest endpoint
  heartbeat/             # wire struct (snake_case JSON)
  queue/                 # SQLite-backed FIFO
  gitctx/                # remote + branch from .git (worktree-aware)
  commits/               # git log reader for sync-commits
  deps/                  # manifest + lockfile reader for sync-deps
  mcp/                   # MCP protocol (JSON-RPC over stdio) + tools
```

### `inlinr sync-deps`

Sends a repository's npm manifests to inlinr's dependency tracking, for
projects whose owner turned it on in the dashboard. It is the only way a
private repository, or one not on GitHub, is read: the server never asks
GitHub for the `repo` scope. Code in `internal/deps/`.

```sh
inlinr sync-deps [--dir <folder>] [--remote <url>] [--force] [--dry-run]
```

- `heartbeat` spawns it, detached, when `~/.inlinr/deps-sync.json` says the
  repository is due: at most once a day per remote, one small file read
  otherwise. `deps_sync = false` under `[behavior]` turns it off.
- It asks `GET /api/v1/deps/status` first and sends nothing unless the answer
  is `wanted`. A project not followed is never read.
- Default branch only (`origin/HEAD`, else main/master).
- What leaves the machine: the dependency fields of each package.json (name,
  version, the four dependency blocks, workspaces, catalogs), the pnpm
  workspace file, and one installed version per direct dependency, read from
  pnpm-lock.yaml, package-lock.json, bun.lock or yarn.lock. Never scripts,
  never the transitive tree.

### `inlinr mcp`

A Model Context Protocol server for AI assistants, over stdio: JSON-RPC 2.0,
one message per line. Stdlib only, no MCP SDK: a tools-only server needs
initialize, ping, tools/list and tools/call, and nothing else. Code in
`internal/mcp/`.

| Tool | Calls |
|---|---|
| `time_summary` | `GET /api/v1/me/summary?period&from&to&project` (device token) |
| `log_time` | `POST /api/v1/me/entries` (device token) |
| `check_dependencies` | `POST /api/v1/check` |
| `package_releases` | `GET /api/v1/packages/<name>/releases` |
| `upgrade_notes` | `GET /api/v1/packages/<name>/upgrade?from&to` |
| `my_upgrades` | `GET /api/v1/me/upgrades?project&dev&limit` (device token) |
| `find_usages` | nothing: walks the folder for lines importing the package (`usages.go`) |

- Stdout is the protocol. Anything for a person goes to stderr, or a client
  reads it as a broken message.
- Without a token it still starts: the package tools are public, and the
  account tools answer with "run `inlinr activate`".
- `check_dependencies` reads the package.json of the folder the assistant
  started it in and sends only the four dependency blocks, never scripts or
  the package name.
- `find_usages` reads source files only (js, ts, jsx, tsx, vue, svelte,
  astro, up to 1 MB), skips `node_modules`, build output and hidden
  folders, and never calls the server.
- A package name must match npm's own rule before it goes in a URL path.
- An API error comes back as a tool result with `isError`, carrying the
  server's message, so the assistant can tell the user instead of retrying.

### Error reports

`heartbeat`, `sync-deps`, `upgrade` and `mcp` run unattended, so a failure
there is sent to `POST /api/v1/plugin-errors` and shows in Admin → Errors on
inlinr.com. Code in `internal/report/`.

- Not sent: no network, a 5xx, a dead token, a refused batch (the server notes
  refusals itself), anything wrapped in `report.Quiet` (the user's to fix), a
  bad flag. A panic is sent with its stack, then crashes as before.
- The same error at most once a day per machine (`~/.inlinr/error-reports.json`).
- The home folder becomes `~`; no token, nothing about who. A `dev` build sends
  nothing. `error_reports = false` under `[behavior]` turns it off.

## Build & distribution

- `make build` → `bin/inlinr` (native).
- `make build-all` → `dist/inlinr-{os}-{arch}` (5 binaries) + `SHA256SUMS.txt`.
- Release: change `VERSION` (X.Y.Z) in a pull request; its merge to main
  makes `release.yml` tag `vX.Y.Z` on the merged commit (a version already
  tagged is skipped; a `v*` tag pushed by hand still works). GitHub Actions
  cross-compiles, publishes `SHA256SUMS.txt`
  and `SHA256SUMS.txt.sig`, uploads. The signature is Ed25519 by the
  `RELEASE_SIGNING_KEY` secret (private key in PEM; Thibault keeps the copy),
  checked in the workflow against `internal/release/release-key.pub.pem`; with
  no secret the release fails. `inlinr upgrade` and the VS Code extension refuse
  a release whose list is unsigned or signed by another key. Rotating the key:
  ship a CLI and an extension carrying the new public key first.
- **No Authenticode / notarization.** This line used to claim macOS notarization and
  Windows code-signing; neither exists in `.github/workflows/release.yml`. The
  consequence is real and user-visible: Windows SmartScreen warns on the `.exe`,
  and some antivirus engines heuristically flag unsigned Go binaries. Fixing it
  needs a paid Authenticode certificate, not a workflow change — see the
  Downloads section of README.md.
- The VS Code extension auto-downloads from GitHub Releases and verifies the
  signature, then SHA256SUMS.txt. The Claude Code plugin does not use the binary.

## Who depends on this, and what to update

- **inlinr-vscode** spawns `inlinr heartbeat` with the flags in its CLAUDE.md
  and downloads the binary from this repository's latest GitHub Release. A new
  flag ships here first, in a release, before the extension uses it:
  `heartbeat` exits 2 on an unknown flag.
- **AI assistants** run `inlinr mcp`; its tools call inlinr-web's public and
  `/api/v1/me/*` endpoints.
- A release that changes behaviour: describe it in the GitHub Release notes
  (there is no CHANGELOG.md here) and keep README.md's command list and
  config keys true. A new user-facing command (as `mcp` was) also belongs on
  inlinr.com: the server repository's `docs/public-pages.md` says where.

## Version injection

`main.Version` is `dev` during development, set at build via `-ldflags "-X main.Version=..."` (see Makefile).

---

## Contract (sync with `inlinrhq/inlinr-web` — verify before changing)

If you edit any of the sections below, update the matching sections in the server repo and in every plugin repo's CLAUDE.md.

### Device flow (auth)

1. `POST https://inlinr.com/api/auth/device` with `{ client_name, editor, platform }` → `{ device_code, user_code, verification_uri, verification_uri_complete, expires_in, interval }`.
2. User opens `verification_uri_complete` in a browser, signs in with GitHub, approves.
3. `POST https://inlinr.com/api/auth/device/token` with `{ device_code }`, polled every `interval` seconds.
   - While pending: `{ "error": "authorization_pending" }` (HTTP 400).
   - On approval: `{ access_token: "in_d_...", device: {...}, user: {...} }` (HTTP 200).
4. CLI stores `access_token` in `~/.inlinr/config.toml` as `auth.device_token`.
5. All subsequent ingest requests use `Authorization: Bearer in_d_...`.

### Heartbeat wire format (POST /api/v1/heartbeats)

```jsonc
// Request: array of beats, up to 1000 per request.
[{
  "entity":              "src/routes/index.tsx",     // required — file path, URL, or app name
  "type":                "file",                     // optional, default "file"; also "app"|"domain"
  "time":                1734523920.123,             // required — unix seconds (fractional ms ok)
  "project_git_remote":  "git@github.com:you/r.git", // required unless `project` — server upserts Project by this
  "project":             "side-folder",              // a folder with no git remote: its name (last segment only)
  "branch":              "main",                     // optional
  "language":            "typescript",               // optional
  "category":            "coding",                   // optional; coding|debugging|building|code-reviewing|writing-tests
  "is_write":            false,                      // optional, default false; true on save
  "lineno":              42,                         // optional
  "cursorpos":           1023,                       // optional
  "lines":               180,                        // optional — total lines in file
  "ai_tool":             "copilot",                  // optional — see "AI tool enum" below
  "ai_line_changes":     12,                         // optional — net delta (legacy)
  "human_line_changes":  3,                          // optional — net delta (legacy)
  "lines_added":         14,                         // optional — 0 is meaningful; absent = not counted
  "lines_deleted":       2,                          // optional
  "ai_lines_added":      12,                         // optional
  "ai_lines_deleted":    1,                          // optional
  "ai_session":          "9f2c1ab0-...",             // optional — one assistant conversation
  "ai_model":            "claude-opus-5",            // optional
  "ai_input_tokens":     41233,                      // optional — uncached input only
  "ai_cache_read_tokens":  371101,                   // optional — context re-read from the prompt cache
  "ai_cache_write_tokens": 9120,                     // optional — context written to the prompt cache
  "ai_output_tokens":    8021,                       // optional
  "editor":              "vscode",                   // optional
  "editor_version":      "1.99.3",                   // optional — the HOST editor's version
  "plugin":              "vscode-inlinr/0.1.0"       // optional — user-agent style
}]
```

`editor` and `editor_version` describe the host, `plugin` describes us. They are
not interchangeable: every VS Code fork ships the same extension under the same
`vscode-inlinr/x.y.z` user-agent, so `editor` is the only field that separates
Cursor from Windsurf from stock VS Code. Inside a fork, `editor_version` is the
*embedded VS Code build* — the forks don't expose their own release to
extensions — so it reads as "which VS Code baseline", never "which Cursor".

`category` also accepts `ai-coding`, used by the Claude Code plugin for time
spent driving an assistant rather than typing.

Token fields come from assistant transcripts, sent by the Claude Code plugin.
The VS Code extension never sends them.

The server no longer stores `lineno`, `cursorpos` or `lines` (nothing read them).
They are still accepted, so older binaries keep working.

Response: `{ "responses": [[{"id":"hb_0"}, 201], ...], "accepted": N }`. Per-beat status array lets the CLI dequeue precisely.

Retry semantics:
- 200/201/202 → ack (delete from queue).
- 400 → discard (malformed, no point retrying).
- 401 → stop, surface "re-authenticate" to the user.
- 5xx / network error → leave in queue, try next invocation.

### Dependency manifests (sync-deps)

```jsonc
// GET /api/v1/deps/status?project_git_remote=<url>
{ "wanted": true, "check_after": 86400 }

// POST /api/v1/deps/manifests
{
  "project_git_remote": "git@github.com:you/r.git",
  "root_path": "package.json",                      // relative, ends in package.json
  "manifests": [{ "path": "apps/web/package.json", "content": "{...}" }], // ≤ 61
  "pnpm_workspace": "packages:\n  - apps/*\n",       // optional
  "installed": { "apps/web/package.json": { "react": "19.1.0" } } // optional
}
// 200 { "stored": N, "unreadable": [...] } · 409 tracking is off · 404 unknown project
```

### AI tool enum (on the wire)

`copilot` · `cursor` · `claude-code` · `codeium` · `windsurf` · `aider` ·
`codex` · `gemini-cli` · `opencode`. Anything else is rejected by server
validation (the server's list is `src/lib/ai/tools.ts` in inlinr-web).
`heartbeat.AITools` holds the same list and drops an unknown value before
sending. The VS Code extension only ever sends the first six; `codex` and
`gemini-cli` come from the Claude Code plugin, which posts its own beats.

---

## Conventions

- Never write to stdout unless the user asked (`doctor`, `--version`). Heartbeat command is silent on success — plugins parse exit code, not output.
- Exit codes: 0 ok, 1 runtime failure, 2 bad args.
- Errors go to stderr prefixed with `inlinr:`.
- SQLite queue must survive concurrent invocations (the plugin spawns us frequently) — WAL mode + busy_timeout handles this.
