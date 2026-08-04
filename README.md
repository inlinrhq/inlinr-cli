# inlinr-cli

Time tracking daemon for [Inlinr](https://inlinr.com). Accepts heartbeats from
editor plugins (VS Code, JetBrains, Neovim, …), buffers them in an on-disk
SQLite queue, and uploads batches to the Inlinr ingest endpoint.

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
Windows one adds `%LOCALAPPDATA%\Inlinr\bin` and sets it for the current
session too, so `inlinr activate` works immediately rather than after a restart
nobody expects.

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

Every release publishes `SHA256SUMS.txt`. Builds use `-trimpath`, so they are
reproducible: anyone can rebuild the same tag from source and get the same
hash. That is the integrity guarantee available today.

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
inlinr doctor                       # diagnose config + connectivity
```

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
```

Queue at `~/.inlinr/queue.db`.

## License

BSD-3. See LICENSE. Portions of the plugin-facing CLI surface are modelled on
[wakatime-cli](https://github.com/wakatime/wakatime-cli) (also BSD-3) to ease
porting existing WakaTime editor plugins.
