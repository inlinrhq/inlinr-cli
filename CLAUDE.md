# inlinr-cli

Go daemon that editor plugins spawn to send heartbeats. Owns: auth token storage, offline SQLite queue, batch upload, HTTP retry. Plugins never touch the network directly.

Sibling repos:
- `inlinrhq/my.inlinr.com` — server (ingest + dashboard). Source of truth for the wire contract below.
- `inlinrhq/inlinr-vscode` — VS Code plugin that spawns this binary.
- `inlinrhq/inlinr-intellij` — JetBrains plugin (same).

---

## Tech

- Go 1.22+
- `github.com/mattn/go-sqlite3` — offline queue
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
```

## Build & distribution

- `make build` → `bin/inlinr` (native).
- `make build-all` → `dist/inlinr-{os}-{arch}` (5 binaries) + `SHA256SUMS.txt`.
- Release: tag `vX.Y.Z`, GitHub Actions builds, signs (macOS notarization, Windows code-sign), uploads.
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
  "ai_line_changes":     12,                         // optional
  "human_line_changes":  3,                          // optional
  "editor":              "vscode",                   // optional
  "plugin":              "vscode-inlinr/0.1.0"       // optional — user-agent style
}]
```

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
