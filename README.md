# seermon

**seer** (one who sees) + **monitor**. A single static, pure-Go binary that watches the network path from this machine and tells you the truth about it.

> **Status: M0 (scaffold).** The repo builds, and `version`, `init` and `config check` work.
> The live network feed arrives with v0.1 (M1). The full roadmap and every locked design
> decision are in [docs/BUILD_PLAN.md](docs/BUILD_PLAN.md).

## Build

Requires Go 1.24+. No cgo, so one static binary per OS:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=v0.0.1" -o seermon ./cmd/seermon
```

Cross-compile by setting `GOOS` / `GOARCH` (windows, linux and darwin on amd64 and arm64 are built in CI).

## Commands (M0)

```
seermon version
seermon init [--dir <path>] [--force]
seermon config check [--config <path>] [--targets-file <path>] [--deeper]
```

| Command | What it does |
|---|---|
| `version` | Prints version and build info. |
| `init` | Writes a commented default `seermon.yaml` and `targets.yaml` into the user config directory (or `--dir`). Never overwrites without `--force`. |
| `config check` | Validates both files and exits, reporting every problem at once. `--deeper` also applies the `--deeper` rules. |

Exit codes: `0` ok · `1` config invalid · `2` usage error · `3` could not complete (e.g. `init` cannot write).

## Configuration

Two optional files. With neither present, compiled defaults apply.

| File | Holds |
|---|---|
| `targets.yaml` | Probe targets only (host, port, optional per-target `warn_ms` / `crit_ms`). |
| `seermon.yaml` | Everything else: intervals, thresholds, geo, log, feed, and the `--deeper` section. |

Each is searched in order: explicit flag → executable directory → user config directory
(`%AppData%\seermon`, `~/.config/seermon`, or `~/Library/Application Support/seermon`).
Unknown keys are errors, so typos do not pass silently.

Nothing site-specific is compiled in. Targets, addresses, SSIDs, passwords and thresholds all live in these files.
The Deeper device password belongs in the `SEERMON_DEEPER_PW` environment variable (or whatever `password_env` names);
it is never echoed in errors or output.

State (CSV log) lives in `%LOCALAPPDATA%\seermon\`, `~/.local/state/seermon/` or `~/Library/Application Support/seermon/`.

## Development

```sh
go vet ./...
go test ./...
```

CI runs build, vet and tests on Windows, Linux and macOS with `CGO_ENABLED=0`, plus a cross-build matrix.

## License

MIT. See [LICENSE](LICENSE).
