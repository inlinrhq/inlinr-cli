# inlinr-cli

> **Reprise de session** — lis d'abord `../PROGRESS.md` (racine du workspace) : il tient l'état
> d'avancement du chantier en cours, la prochaine action, et couvre les 3 repos. Mets-le à jour
> après chaque modification.

Go daemon that editor plugins spawn to send heartbeats. Owns: auth token storage, offline SQLite queue, batch upload, HTTP retry. Plugins never touch the network directly.

It also has an **AI transcript parser** (`inlinr sync-ai`, `internal/ai/`).
**Nothing calls it today.** The Claude Code plugin (`inlinrhq/inlinr-claude-code`,
`src/transcript.ts` + `src/api.ts`) parses the same transcripts in TypeScript and
posts to `/api/v1/heartbeats` itself, without this binary. So there are two
parsers of the same format, and a fix in one is not a fix in the other. Pick one
before adding a second assistant (Codex, Gemini, opencode): either the plugin
calls `inlinr sync-ai`, or this command goes.

Sibling repos:
- `inlinrhq/inlinr-web` — server (ingest + dashboard). Source of truth for the wire contract below.
- `inlinrhq/inlinr-vscode` — VS Code plugin that spawns this binary.
- `inlinrhq/inlinr-claude-code` — Claude Code plugin. Does **not** spawn this
  binary; it shares only `~/.inlinr/config.toml` (the device token).
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
  upgrade.go             # self-update from GitHub Releases (SHA256-verified)
  syncai.go              # `inlinr sync-ai` (see below)
  synccommits.go         # `inlinr sync-commits` — commits from any git host
  syncdeps.go            # `inlinr sync-deps` — manifests for dependency tracking
internal/
  config/                # ~/.inlinr/config.toml load/save
  device/                # OAuth 2.0 Device Grant client
  api/                   # HTTP client for the ingest endpoint
  heartbeat/             # wire struct (snake_case JSON)
  queue/                 # SQLite-backed FIFO
  ai/                    # assistant transcript parsers + sync watermark/lock
  gitctx/                # remote + branch from .git (worktree-aware)
  commits/               # git log reader for sync-commits
  deps/                  # manifest + lockfile reader for sync-deps
```

### `inlinr sync-ai`

Parses AI assistant transcripts into heartbeats. Claude Code today
(`~/.claude/projects/**/*.jsonl`); one file per assistant under `internal/ai/`.

```sh
inlinr sync-ai [--plugin claude-code-inlinr/0.1.0] [--project-folder <dir>] [--dry-run]
```

Three details that matter, all learned from real transcripts:
- **Cache tokens are counted, and kept apart.** Most of a Claude Code turn is
  cached context; counting only `input_tokens` under-reports by an order of
  magnitude. Cache reads and cache writes are billed at very different rates, so
  they travel as `ai_cache_read_tokens` / `ai_cache_write_tokens`, and
  `ai_input_tokens` is the uncached input only.
- **Streaming duplicates replace, they don't accumulate.** The same `message.id`
  is logged repeatedly with running totals; summing them multiplies every
  response's cost by its chunk count.
- **A `filePath` is not a write.** The Read tool reports one too. Only a
  `structuredPatch`, a `newString`/`oldString`, or a create/update/delete counts.

Safe to call on every assistant turn: a watermark in `~/.inlinr/ai-sync.json`
means only new lines are read, and a lock in `~/.inlinr/ai-sync.lock` stops two
editors double-counting. Sessions outside a git repository are skipped.

### `inlinr sync-deps`

Sends a repository's npm manifests to Inlinr's dependency tracking, for
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

## Build & distribution

- `make build` → `bin/inlinr` (native).
- `make build-all` → `dist/inlinr-{os}-{arch}` (5 binaries) + `SHA256SUMS.txt`.
- Release: tag `vX.Y.Z`, GitHub Actions cross-compiles, publishes `SHA256SUMS.txt`, uploads.
- **Nothing is code-signed.** This line used to claim macOS notarization and
  Windows code-signing; neither exists in `.github/workflows/release.yml`. The
  consequence is real and user-visible: Windows SmartScreen warns on the `.exe`,
  and some antivirus engines heuristically flag unsigned Go binaries. Fixing it
  needs a paid Authenticode certificate, not a workflow change — see the
  Downloads section of README.md.
- The VS Code extension auto-downloads from GitHub Releases and verifies against
  SHA256SUMS.txt. The Claude Code plugin does not use the binary.

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
  "project_git_remote":  "git@github.com:you/r.git", // required — server upserts Project by this
  "branch":              "main",                     // optional
  "language":            "typescript",               // optional
  "category":            "coding",                   // optional; coding|debugging|building|code-reviewing|writing-tests
  "is_write":            false,                      // optional, default false; true on save
  "lineno":              42,                         // optional
  "cursorpos":           1023,                       // optional
  "lines":               180,                        // optional — total lines in file
  "ai_tool":             "copilot",                  // optional — copilot|cursor|claude-code|codeium|windsurf|aider
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

`category` also accepts `ai-coding`, used by `inlinr sync-ai` for time spent
driving an assistant rather than typing.

Token fields come from assistant transcripts (today the Claude Code plugin;
`inlinr sync-ai` can produce them too). The VS Code extension never sends them.

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

`copilot` · `cursor` · `claude-code` · `codeium` · `windsurf` · `aider`. Anything else is rejected by server validation.

---

## Conventions

- Never write to stdout unless the user asked (`doctor`, `--version`). Heartbeat command is silent on success — plugins parse exit code, not output.
- Exit codes: 0 ok, 1 runtime failure, 2 bad args.
- Errors go to stderr prefixed with `inlinr:`.
- SQLite queue must survive concurrent invocations (the plugin spawns us frequently) — WAL mode + busy_timeout handles this.
