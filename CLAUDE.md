# inlinr-cli

> **Reprise de session** — lis d'abord `../PROGRESS.md` (racine du workspace) : il tient l'état
> d'avancement du chantier en cours, la prochaine action, et couvre les 3 repos. Mets-le à jour
> après chaque modification.

Go daemon that editor plugins spawn to send heartbeats. Owns: auth token storage, offline SQLite queue, batch upload, HTTP retry. Plugins never touch the network directly.

It also owns **AI transcript parsing** (`inlinr sync-ai`, `internal/ai/`). Token
counts and per-tool edit sizes only exist inside an assistant's own local
session logs, so parsing them here means one implementation covers the terminal,
the desktop app and every editor extension — the plugins stay dumb event pumps.
Same split WakaTime uses: their Claude plugin passes three flags, all the
parsers live in the CLI.

Sibling repos:
- `inlinrhq/my.inlinr.com` — server (ingest + dashboard). Source of truth for the wire contract below.
- `inlinrhq/inlinr-vscode` — VS Code plugin that spawns this binary.
- `inlinrhq/inlinr-intellij` — JetBrains plugin (same).

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
internal/
  config/                # ~/.inlinr/config.toml load/save
  device/                # OAuth 2.0 Device Grant client
  api/                   # HTTP client for the ingest endpoint
  heartbeat/             # wire struct (snake_case JSON)
  queue/                 # SQLite-backed FIFO
  ai/                    # assistant transcript parsers + sync watermark/lock
  gitctx/                # remote + branch from .git (worktree-aware)
```

### `inlinr sync-ai`

Parses AI assistant transcripts into heartbeats. Claude Code today
(`~/.claude/projects/**/*.jsonl`); one file per assistant under `internal/ai/`.

```sh
inlinr sync-ai [--plugin claude-code-inlinr/0.1.0] [--project-folder <dir>] [--dry-run]
```

Three details that matter, all learned from real transcripts:
- **Input tokens include cache creation and cache reads.** Most of a Claude Code
  turn is cached context; counting only `input_tokens` under-reports by an order
  of magnitude.
- **Streaming duplicates replace, they don't accumulate.** The same `message.id`
  is logged repeatedly with running totals; summing them multiplies every
  response's cost by its chunk count.
- **A `filePath` is not a write.** The Read tool reports one too. Only a
  `structuredPatch`, a `newString`/`oldString`, or a create/update/delete counts.

Safe to call on every assistant turn: a watermark in `~/.inlinr/ai-sync.json`
means only new lines are read, and a lock in `~/.inlinr/ai-sync.lock` stops two
editors double-counting. Sessions outside a git repository are skipped.

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
- Plugins auto-download from GitHub Releases and verify against SHA256SUMS.txt.

## Version injection

`main.Version` is `dev` during development, set at build via `-ldflags "-X main.Version=..."` (see Makefile).

---

## Contract (sync with `inlinrhq/my.inlinr.com` — verify before changing)

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
  "ai_input_tokens":     412334,                     // optional — incl. cache create + cache read
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

Token fields come only from `inlinr sync-ai`; editor plugins never send them.

Response: `{ "responses": [[{"id":"hb_0"}, 201], ...], "accepted": N }`. Per-beat status array lets the CLI dequeue precisely.

Retry semantics:
- 200/201/202 → ack (delete from queue).
- 400 → discard (malformed, no point retrying).
- 401 → stop, surface "re-authenticate" to the user.
- 5xx / network error → leave in queue, try next invocation.

### AI tool enum (on the wire)

`copilot` · `cursor` · `claude-code` · `codeium` · `windsurf` · `aider`. Anything else is rejected by server validation.

---

## Conventions

- Never write to stdout unless the user asked (`doctor`, `--version`). Heartbeat command is silent on success — plugins parse exit code, not output.
- Exit codes: 0 ok, 1 runtime failure, 2 bad args.
- Errors go to stderr prefixed with `inlinr:`.
- SQLite queue must survive concurrent invocations (the plugin spawns us frequently) — WAL mode + busy_timeout handles this.
