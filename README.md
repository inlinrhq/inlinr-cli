<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://inlinr.com/brand/inlinr-logo-on-dark.svg">
  <img alt="inlinr" src="https://inlinr.com/brand/inlinr-logo-on-light.svg" height="36">
</picture>

# inlinr-cli

Time tracking daemon for [inlinr](https://inlinr.com). Accepts heartbeats from
editor plugins (VS Code, JetBrains, Neovim, …), buffers them in an on-disk
SQLite queue, and uploads batches to the inlinr ingest endpoint.

Plugins call this binary as a subprocess — they don't talk to the API directly.

## Install

```sh
# macOS / Linux
curl -fsSL https://inlinr.com/install.sh | sh
```

```powershell
# Windows
irm https://inlinr.com/install.ps1 | iex
```

Both verify the published SHA-256 before installing and refuse to continue on a
mismatch, and both put the binary somewhere already on your `PATH` — the
Windows one adds `%LOCALAPPDATA%\inlinr\bin` and sets it for the current
session too, so `inlinr activate` works immediately rather than after a restart
nobody expects.

### If you have Go, skip all of this

```
go install github.com/inlinrhq/inlinr-cli/cmd/inlinr@latest
```

Nothing is downloaded as an executable, so there is nothing for a download
scanner to object to, and the binary lands in your GOPATH bin which is usually
already on your `PATH`. For a developer this is both the shortest path and the
one with the fewest ways to go wrong.

**If your antivirus deletes the download**, that is a false positive and the
installer now says so with the ways forward. The detection is almost always
`Trojan:Win32/Wacatac.B!ml` -- the `!ml` suffix means a machine-learning guess
rather than a signature match, and Go binaries are the single most common thing
it guesses wrong about. The one that fixes it for everyone
rather than one machine at a time is
[reporting it to Microsoft](https://www.microsoft.com/en-us/wdsi/filesubmission)
-- free, reviewed by a human, and a whitelisted hash ends the problem for every
user of that release. Code signing is the real answer and is not done yet.

**On Windows this also avoids the SmartScreen dialog**, and not by suppressing
it. That warning comes from Mark-of-the-Web: browsers tag downloaded files with
a `Zone.Identifier` stream and SmartScreen challenges tagged executables from
publishers it does not recognise. `Invoke-WebRequest` does not attach the tag,
so a binary fetched by the script does not trigger it. The binary is byte for
byte the same either way; only its provenance metadata differs. Real-time
antivirus can still object — nothing but code signing fixes that.

Then:

```
inlinr activate
```

## Downloading the binary by hand

Everything below is for people who would rather not pipe a script into a shell.
It is the same binary.

## Downloads and the Windows warning

The binaries are **not code-signed**. On Windows that has two visible effects:

- SmartScreen shows *"Windows protected your PC"* or *"unrecognized app"*;
- some antivirus engines flag the `.exe` outright.

This is a false positive, and an extremely common one for Go binaries — a
statically linked executable from an unknown publisher looks, to a heuristic
scanner, a lot like a packed dropper. Being a false positive does not make it
harmless: most people stop at that dialog, and they are right to.

### Verify what you downloaded

Every release publishes `SHA256SUMS.txt` and `SHA256SUMS.txt.sig`, its
Ed25519 signature by inlinr's release key. `inlinr upgrade` and the VS Code
extension install nothing whose list that key did not sign. Builds use
`-trimpath`, so they are reproducible: anyone can rebuild the same tag from
source and get the same hash. (This is not Authenticode code signing: Windows
still warns, see below.)

To check the signature yourself, with the public key from
[internal/release/release-key.pub.pem](internal/release/release-key.pub.pem):

```sh
openssl pkeyutl -verify -rawin -pubin -inkey release-key.pub.pem \
  -in SHA256SUMS.txt -sigfile SHA256SUMS.txt.sig
```

**PowerShell**

```powershell
Get-FileHash .\inlinr-windows-amd64.exe -Algorithm SHA256
# compare against the line for inlinr-windows-amd64.exe in SHA256SUMS.txt
```

**macOS / Linux**

```sh
sha256sum -c SHA256SUMS.txt --ignore-missing
```

Once the hash matches, unblock the file:

```powershell
Unblock-File .\inlinr.exe
```

### Build it yourself instead

The surest answer, and it takes one command with Go installed:

```sh
git clone https://github.com/inlinrhq/inlinr-cli
cd inlinr-cli && go build -o inlinr.exe ./cmd/inlinr
```

### The actual fix

An Authenticode certificate. Signing removes the antivirus heuristic
immediately; SmartScreen additionally builds reputation per-certificate, so an
OV certificate still warns until enough downloads accumulate, while an EV
certificate is trusted from the first download.

It is a cost decision rather than an engineering one, which is why it is
written here plainly instead of being implied by a workflow that does not do
it.

## Usage

```sh
inlinr activate                     # authorize this machine
inlinr heartbeat \
  --entity src/routes/index.tsx \
  --project-git-remote git@github.com:you/repo.git \
  --language typescript \
  --editor vscode \
  --plugin vscode-inlinr/0.1.0 \
  --write
# a folder with no git remote: --project <folder name> instead of
# --project-git-remote (only the name leaves the machine, never the path)
inlinr doctor                       # diagnose config + connectivity
inlinr upgrade                      # update this binary (signature + SHA256 verified)
inlinr signout                      # revoke this machine's token
inlinr sync-commits                 # send local git line counts (opt-in)
inlinr sync-deps                    # send npm manifests of a followed project
inlinr mcp                          # serve inlinr's tools to an AI assistant (MCP)
inlinr --version
```

### In an AI assistant (MCP)

`inlinr mcp` lets Claude Code, Cursor, Claude Desktop or any MCP client ask
inlinr about your time and your dependencies. Sign in once with
`inlinr activate`, then add it to the assistant:

```sh
claude mcp add inlinr -- inlinr mcp
```

or, in a client's JSON config:

```json
{ "mcpServers": { "inlinr": { "command": "inlinr", "args": ["mcp"] } } }
```

Tools: `time_summary` (time by project and language, AI time and cost),
`log_time` (a manual entry), `check_dependencies` (the package.json in the
folder the assistant runs in; only its four dependency blocks are sent),
`package_releases`, `upgrade_notes`, `my_upgrades` (your Upgrades list:
what to upgrade first, from which version to which) and `find_usages` (the
lines of this codebase that import a package, searched on your machine;
nothing is sent). The package tools work without signing in.

## Build

```sh
make build           # native binary in bin/inlinr
make build-all       # cross-compiled binaries in dist/
make test
```

## Config

Stored at `~/.inlinr/config.toml` (overridable with `$INLINR_HOME`).

```toml
[auth]
device_token = "in_d_..."

[behavior]
heartbeat_rate_limit_seconds = 120
offline_queue_max = 10000
error_reports = true   # false: never send the CLI's own errors to inlinr
```

Queue at `~/.inlinr/queue.db`.

When a command a plugin started fails on its own (a queue it cannot open, an
upgrade whose checksum does not match, a crash), the CLI tells inlinr, at most
once a day for the same error: the command, the message with your home folder
replaced by `~`, the CLI version, operating system and editor. No token, no
file content. Network failures and server errors are never sent.

## License

BSD-3 — see [LICENSE](LICENSE).

The plugin-facing surface — heartbeat field names, flag names, the per-beat
response array — follows the [wakatime-cli](https://github.com/wakatime/wakatime-cli)
convention, so an existing WakaTime editor plugin ports with little work. The
compatibility is deliberate and it is a matter of interface.
