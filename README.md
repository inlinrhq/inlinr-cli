# inlinr-cli

Time tracking daemon for [Inlinr](https://inlinr.com). Accepts heartbeats from
editor plugins (VS Code, JetBrains, Neovim, …), buffers them in an on-disk
SQLite queue, and uploads batches to the Inlinr ingest endpoint.

Plugins call this binary as a subprocess — they don't talk to the API directly.

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
api_url = "https://inlinr.com"

[behavior]
heartbeat_rate_limit_seconds = 120
offline_queue_max = 10000
```

Queue at `~/.inlinr/queue.db`.

## License

BSD-3. See LICENSE. Portions of the plugin-facing CLI surface are modelled on
[wakatime-cli](https://github.com/wakatime/wakatime-cli) (also BSD-3) to ease
porting existing WakaTime editor plugins.
