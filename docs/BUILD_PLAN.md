# seermon — Build Plan

**Version 1.0 · 2026-11-20**
Supersedes: *NetMon Build Guide 1.0*, *Deeper Connect Monitor + Failover — Build Plan*, *Unified Guide v2.0 / v2.1*.
Every item below is a final decision. Start at **M0**.

---

## 0. Fill-ins before M0

| Item | Value |
|---|---|
| GitHub handle | `<FILL IN>` — module path becomes `github.com/<HANDLE>/seermon` |
| License | MIT |
| Binary name | `seermon` |
| State dir (Windows) | `%LOCALAPPDATA%\seermon\` |
| State dir (Linux) | `~/.local/state/seermon/` |
| State dir (macOS) | `~/Library/Application Support/seermon/` |

Nothing site-specific belongs in code. Targets, addresses, SSIDs, passwords, thresholds all live in user-owned config files.

---

## 1. What seermon is

**seer** (one who sees) + **monitor**. A single static, pure-Go binary that watches the network path from this machine and tells you the truth about it.

Type `seermon` in a terminal → a live, append-only feed of network status. That's the whole product at v0.1.

Everything else is opt-in and arrives in phases:

- `--full` adds system telemetry (CPU/RAM/GPU/temps/disk) to the view — **held in memory only, never written to disk**.
- `--deeper` adds Deeper Connect device attribution and, much later, guarded local failover.

Design commitments:

- **Best-effort everywhere.** Any sensor may be unavailable. Unavailable renders `--` in the feed and `null` in JSON — never a fake `0`.
- **One source of truth.** Collectors build one immutable snapshot; the feed, the TUI, the JSON encoder, and the CSV writer all consume that same struct.
- **A monitor must not be a load.** Cache expensive subprocess calls. No polling faster than the data changes.
- **Single static binary.** `CGO_ENABLED=0` on every platform.

---

## 2. Locked decisions

| Question | Decision |
|---|---|
| Feed style | **Append-only lines first** (test stage). Nicer in-place TUI redraw comes later at v0.3. |
| Persistence | **Simple CSV log.** No SQLite anywhere in this plan. |
| Targets | **Separate config file** (`targets.yaml`), not embedded in the main config. |
| Public IP / geo | **On by default** in the default view. Shows `--` when the lookup fails; keeps last good value flagged stale. `--no-geo` opts out. |
| Disk space | Checked every **30 minutes**. |
| `--full` gating | Identical on every surface. Without `--full`, the `system` key is absent from JSON and system lines never print. System metrics live in memory ring buffers only — gone on exit. |
| `--deeper` safety | `--deeper` alone = read-only attribution + manual subcommands. Automation needs **two keys**: config `failover.auto: true` **and** running as `seermon daemon`. |
| Daemon mode | Ships only in the deeper phase (v0.6), not before. |
| Platforms | Windows primary. Linux + macOS best-effort at v0.1, full parity by v0.3. |
| TUI stack | bubbletea + lipgloss + bubbles — introduced at v0.3, not v0.1. |
| Default targets | The 8 listed in §7. `--targets host:port,…` ships in v0.1. |

---

## 3. Non-negotiables

1. **Null, never fake zero.** Unavailable ⇒ `--` in the feed, `null` in JSON, empty field in CSV. A missing temp sensor must not read as perfect health.
2. **System metrics are never persisted.** `--full` data does not touch CSV, JSON files, or any disk artifact. stdout only.
3. **Targets live in their own file.** `targets.yaml` is independently editable and independently validatable.
4. **One snapshot struct** feeds every consumer. No divergent code paths for feed vs JSON vs TUI.
5. **`CGO_ENABLED=0` everywhere.** Single cross-compilable static binary.
6. **Single writer.** Exactly one process writes CSV/state at a time. When a daemon is running, attached clients never write.
7. **The deeper control loop is 100% local.** No cloud service, no LLM in the decision path. It must keep working when the internet is down. Geo is decoration and can never gate an action.
8. **Automation is daemon-only and double-opt-in.** A TUI or CLI crash can never take down failover.
9. **Fast-tier flaps never act.** Only consecutive deep-cycle failures drive the deeper engine.
10. **Display verdict ≠ action verdict.** The target-quorum status never triggers a node switch.
11. **Secrets live only in config/env.** Device password never in code, logs, CSV, JSON, or IPC responses.
12. **Gates are preserved.** Phase 0 device capture before deeper code; ≥24 h read-only observation before actions; manual validation before automation; tiers enabled cheapest-first.

---

## 4. Phasing at a glance

| Version | Milestone | Ships | Gate to pass |
|---|---|---|---|
| — | M0 | Repo scaffold, CI, license, config loader | Builds on 3 OSes |
| **v0.1** | M1 | Append-only network feed, CSV log, `targets.yaml`, geo, `--json` / `--stream` | Golden JSON + CSV header validate; 1 h clean run |
| **v0.2** | M2 | `--full` system overlay (ephemeral), disk @ 30 m | Zero system bytes on disk, verified |
| **v0.3** | M3 | bubbletea TUI redraw, `--plain` fallback | Golden frames pass on 3 OSes + SSH |
| **v0.4** | M4 | Deeper Phase 0 capture + read-only client + deep cycle | **≥24 h attribution soak** |
| **v0.5** | M5 | Manual deeper actions + all guards | Each action validated live, one at a time |
| **v0.6** | M6 | Ranking, automation, daemon, IPC | Sim proofs + staged T1→T5 soaks |
| **v1.0** | M7 | Polish, service install, packaging | One static binary per OS |

---

## 5. Architecture

### 5.1 Snapshot store

```go
type Snapshot struct {
    SchemaVersion int
    Timestamp     time.Time
    UptimeSec     int64
    SystemUptime  int64
    Network       NetworkState
    System        *SystemState  // nil unless --full
    Deeper        *DeeperState  // nil unless --deeper
}

var state atomic.Pointer[Snapshot]

// Collector goroutine:
next := buildSnapshot(prev, newSamples)   // immutable swap, no torn reads
state.Store(next)
notifyChanged()

// Any reader — feed line, TUI frame, JSON encoder, CSV writer:
snap := state.Load()
```

Immutable snapshots, atomic pointer swap. No mutex-guarded mutable struct, no torn reads.

### 5.2 Pipeline

```
COLLECTORS (staggered tickers + jitter)
  traffic   1s ─┐
  targets   2s ─┤
  wifi     20s ─┼→ buildSnapshot() → atomic.Pointer[Snapshot]
  lan      30s ─┤                          │
  geo       5m ─┘                          ├→ feed line / TUI frame
                                           ├→ --json / --stream
  --full only:                             └→ CSV writer (every log.interval)
  cpu/ram   2s ─┐
  gpu       5s ─┼→ System (memory only, never persisted)
  temps    10s ─┤
  disk     30m ─┘

  --deeper only (v0.4+):
  deep cycle 30s → verdict → deeper.csv → engine → executor
```

### 5.3 Ring buffers

Fixed-size arrays with a write index — no append/shift slices:

- `[30]ProbeResult` per target (≈60 s window at 2 s cadence)
- `[60]TrafficSample` (60 s graph window)
- `[60]float64` CPU/GPU history (`--full`, memory only)

Failed probes stored as NaN → rendered as gaps in the TUI sparkline, `--` in the feed.

### 5.4 Lifecycle

- One root `context.Context` cancels every ticker, subprocess, probe, and in-flight action on quit.
- Stagger ticker starts, add slight jitter.
- Windows: every `exec.Command` sets `SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}` — no console flashes.
- SIGINT/SIGTERM → cancel root, flush CSV writer, close files, exit with the health code of the last snapshot.
- Prime `cpu.Percent` at startup (first call is since-boot). Prime traffic counters (a rate needs two reads).

---

## 6. Repo layout & dependencies

```
cmd/seermon/main.go           mode dispatch (feed | json | stream | tui | daemon | deeper)
internal/config/              defaults.go + seermon.yaml + targets.yaml loaders + validation
internal/telemetry/           collectors, snapshot, ring buffers
internal/probe/               target probes, dns timing, gateway ping
internal/system/              --full collectors (cpu/ram/gpu/temp/disk), memory only
internal/feed/                append-only line renderer (pure functions)
internal/csvlog/              rotation, header management, prune, single-writer
internal/tui/                 v0.3: pure view funcs + thin tea.Model shell
internal/deeper/              v0.4+: device client, deep cycle, engine, executor
internal/store/               v0.5+: state.json, actions.csv, lock
internal/ipc/                 v0.6: loopback API server + client, token file
internal/redact/
fixtures/                     golden: netsh locales, nmcli, redacted profiler,
                              device API captures (secrets scrubbed)
```

| Dependency | Introduced | Purpose |
|---|---|---|
| `github.com/shirou/gopsutil/v4` | v0.1 | Interface counters, host uptime; CPU/RAM/disk at v0.2 |
| `golang.org/x/sys` | v0.1 | Windows wlanapi, hidden console windows; service install at v1.0 |
| `golang.org/x/net/icmp` | v0.1 | Unprivileged gateway ping |
| `gopkg.in/yaml.v3` | v0.1 | Both config files |
| `charmbracelet/bubbletea` + `lipgloss` + `bubbles` | **v0.3** | TUI runtime, layout, table/viewport/help |
| `github.com/miekg/dns` (optional) | v0.4 | Direct resolver queries, avoids system-cache skew |
| `nvidia-smi` (subprocess) | v0.2 | NVIDIA GPU telemetry — preferred over NVML cgo |

**Not used:** SQLite (CSV instead), MyGo (TUI instead), `jackpal/gateway` (the UDP-dial trick is five lines of stdlib).

Default-route interface discovery:

```go
conn, _ := net.Dial("udp", "8.8.8.8:53")   // sends no packets
localIP := conn.LocalAddr().(*net.UDPAddr).IP
conn.Close()
// match localIP against net.Interfaces() addrs → the default-route interface
```

Pin every version exactly in `go.mod`. Upgrade deliberately.

---

## 7. Configuration — two files

Both are optional. With neither present, compiled defaults apply and seermon prints one hint line:
`no config found — using defaults; run 'seermon init' to write targets.yaml`

Search order for each: explicit flag → executable directory → user config dir.

### 7.1 `targets.yaml` — targets only

```yaml
# targets.yaml — probe targets. Edit freely; seermon reloads on SIGHUP.
targets:
  - { host: bing.com,        port: 443 }
  - { host: google.com,      port: 443 }
  - { host: reddit.com,      port: 443 }
  - { host: github.com,      port: 443 }
  - { host: huggingface.co,  port: 443 }
  - { host: baidu.com,       port: 443, warn_ms: 500, crit_ms: 1500 }
  - { host: cloudflare.com,  port: 443 }
  - { host: 1.1.1.1,         port: 53 }
```

`warn_ms` / `crit_ms` are per-target overrides; `0` or absent means use the global latency thresholds. baidu.com is legitimately slow from outside CN — the override exists so users aren't trained to ignore a permanently amber row.

### 7.2 `seermon.yaml` — everything else

```yaml
intervals:
  traffic: 1s
  probe: 2s
  wifi: 20s
  lan: 30s
  geo: 5m
  # --full only:
  cpu: 2s
  memory: 2s
  gpu: 5s
  temps: 10s
  disk: 30m

probe:
  timeout: 3s          # a timeout counts as a loss sample
  window: 30           # rolling samples per target

thresholds:
  latency_warn_ms: 300
  latency_crit_ms: 800
  loss_warn_pct: 1
  loss_crit_pct: 5
  usage_warn_pct: 60
  usage_crit_pct: 85
  cpu_temp_warn: 70
  cpu_temp_crit: 85
  gpu_temp_warn: 75
  gpu_temp_crit: 85
  traffic_warn_mbps: 1
  traffic_crit_mbps: 10

geo:
  enabled: true
  provider: ipinfo      # https://ipinfo.io/json
  timeout: 5s

log:
  enabled: true
  dir: ""               # empty = platform state dir
  interval: 30s         # one CSV row per target per this interval
  retention_days: 30
  rotate: daily

feed:
  interval: 2s          # tick line cadence
  verbose: false        # true = per-target detail every tick
  color: auto           # auto|always|never — NO_COLOR always respected
  ascii: false          # true = ASCII-only markers for legacy consoles

# ---- v0.4+ only; ignored entirely without --deeper ----
deeper:
  device:
    host: "<management-address>"
    password_env: "SEERMON_DEEPER_PW"    # preferred; or password: inline (perm-warned)
  network:
    deeper_wifi: "<SSID of the Deeper device>"
    upstream_wifi: "<direct-internet SSID>"          # optional
    fallback_wifi: ["<ssid1>", "<ssid2>"]            # priority order
    router_lan_ip: "<router LAN IP>"
    device_gateway_ip: "<Deeper LAN IP as seen from PC>"
    device_upstream_ip: "<device IP on router LAN>"  # optional liveness from fallback
  deep_probe:
    interval: 30s
    timeout: 10s
    https_targets:
      - { name: google, url: "https://www.google.com", expect_status: 200 }
      - { name: reddit, url: "https://www.reddit.com", expect_status: 200 }
    dns_test_domain: "<well-known domain>"
    dns_doh_url: "https://dns.google/resolve"
    dns_doh_bootstrap_ips: ["8.8.8.8", "8.8.4.4"]
    exit_ip_every_n_cycles: 10
  failover:
    auto: false                    # key 1 of 2; key 2 is running as `seermon daemon`
    tiers_enabled: []              # e.g. [t1] — enable in order, cheapest first
    consecutive_failures: 3
    verify_cycles: 2
    cooldowns: { t1: 5m, t2: 10m, t3: 15m, t4: 20m, t5: 10m }
    mode_hold: 10m
    max_actions_per_hour: 12
    max_reboots_per_day: 2
    boot_wait: 120s
    hold_default: 30m
    t5:
      path_fail_cycles: 6
      link_fail_cycles: 6
      isp_fail_cycles: 10
      return_after_recovery: true
      return_probe_after: 10m      # backoff x2, cap 60m
      return_trial_window: 90s
  ranking:
    min_samples: 20

daemon:                          # v0.6+
  listen: "127.0.0.1:8788"       # loopback only; never 0.0.0.0 in v1
```

### 7.3 Validation — startup, fatal on error

- Durations parse; every `warn < crit` pair ordered.
- `targets.yaml`: at least one target; host non-empty; port 1–65535; no duplicate host:port.
- `log.interval` ≥ `probe.interval`; `retention_days` ≥ 1.
- With `--deeper`: `router_lan_ip` ≠ `device_gateway_ip`; password resolvable; `tiers_enabled` ⊆ known tiers; `auto: true` with empty `tiers_enabled` is an error.
- The password value is **never echoed** in error messages.
- `seermon config check` validates both files and exits without starting collectors.

---

## 8. The append-only feed (v0.1 default)

Bare `seermon` prints lines to stdout and never clears the screen. Two line types.

### 8.1 Tick line — every `feed.interval` (2 s)

```
10:45:02  up 8/8  gw 3ms  ↓36.5K/s ↑15.8K/s  avg 212ms  loss 0.0%
10:45:04  up 8/8  gw 3ms  ↓41.2K/s ↑12.1K/s  avg 208ms  loss 0.0%
```

### 8.2 Event lines — printed when something changes

```
10:45:06  !! reddit.com:443 timeout after 3s
10:45:08  up 7/8  gw 3ms  ↓2.1K/s ↑0.4K/s  avg 245ms  loss 3.3%
10:45:10  ++ reddit.com:443 recovered 183ms
10:45:22  ~~ iface wlan0 → eth0 · ip 192.168.1.73 → 192.168.0.44
10:45:30  ~~ ssid DeeperNet-5G → MiFi-2
10:47:01  geo 59.126.203.192 · Taichung TW · Chunghwa Telecom
10:52:01  geo -- (lookup failed: 429) · last good 5m ago
```

Events fire on: target status change (ok↔error), interface/IP/gateway change, SSID change, geo refresh or failure, threshold crossing (green↔amber↔red), collector becoming unavailable.

### 8.3 `--verbose` — per-target detail every tick

```
10:45:02  bing 120  google 132  reddit 180  github 210  hf 240  baidu 470  cf 95  1.1.1.1 24
```

### 8.4 Rules

- Unavailable renders `--`. A failed geo lookup, an absent SSID, a missing gateway ping — all `--`, never `0`.
- Color follows the single threshold rule: `v ≥ crit` red, `v ≥ warn` amber, else green. `NO_COLOR` env and `--no-color` honored. `--ascii` swaps `↓↑!!++~~` for `dn/up/ERR/OK/CHG`.
- Startup banner: version, interface, target count, config paths in use, log path (or `logging off`).
- Non-TTY stdout is fine — the feed is designed to pipe. `seermon >> feed.log` works.
- Ctrl-C exits with the health code of the last snapshot (§12).

---

## 9. CSV logging

Simple, greppable, spreadsheet-friendly. **No SQLite.** One writer process.

### 9.1 Files

| File | Written when | Contents |
|---|---|---|
| `probes-YYYY-MM-DD.csv` | always (unless `--no-log`) | one row per target per `log.interval` |
| `deeper-YYYY-MM-DD.csv` | `--deeper` | one row per deep cycle |
| `actions.csv` | v0.5+, on any action | one row per action attempt |
| `state.json` | v0.5+ | last-known mode/node, lock, cooldowns, caps, kill switch |

**System metrics (`--full`) are never written to any of these.** Enforced by a test (§17).

### 9.2 `probes.csv` — exact header

```
ts,host,port,status,dns_ms,resolved_ip,tcp_ms,avg_ms,loss_pct,samples,iface,ssid,local_ip,gateway_ip,gateway_ms,public_ip,country,down_bps,up_bps,online,total
```

Example rows:

```
2026-11-20T10:45:00Z,google.com,443,ok,1.0,74.125.23.102,132.0,226.0,0.0,30,wlan0,DeeperNet-5G,192.168.1.73,192.168.1.1,3.0,59.126.203.192,TW,37376,16179,8,8
2026-11-20T10:45:00Z,reddit.com,443,timeout,,,-1,,3.3,30,wlan0,DeeperNet-5G,192.168.1.73,192.168.1.1,3.0,59.126.203.192,TW,37376,16179,7,8
```

Field rules:

- **Empty field = unavailable.** Never `0` as a stand-in for missing. (`-1` above is illustrative of what *not* to do — the correct row leaves `tcp_ms` and `avg_ms` empty.)
- `ts` RFC3339 UTC. `status` ∈ `ok | dns_error | timeout | connect_error`.
- `avg_ms` = mean of successful connects in the window; empty if none.
- `loss_pct` rounded to 1 decimal. With window 30, granularity is 3.33% (1/30 = 3.3).
- `samples` = actual sample count (1 on cold start, 30 once warm).
- Context columns (`iface`…`total`) repeat on every row — tidy long format, stable header regardless of target count.

### 9.3 Volume, rotation, retention

- 8 targets × 2,880 log ticks/day = **23,040 rows/day ≈ 2.5 MB/day**.
- Daily rotation. Header written on file creation. Prune files older than `retention_days` (30) at startup and at each rotation.
- Buffered writer, flushed after each log tick. Flush + close on shutdown.
- Header is a golden fixture — a schema change requires a deliberate version bump and a new header fixture.

### 9.4 Single writer

When `seermon daemon` is running (v0.6+), attached TUI/CLI clients read via IPC and **do not write**. Standalone runs own the log. `state.json` is written atomically (temp file + rename).

---

## 10. Network collectors — per-platform reality

Every collector is best-effort with per-field null and a human-readable reason.

| Collector | Interval | Notes |
|---|---|---|
| Traffic | 1 s | Two counter reads for a rate; prime at startup; clamp negative deltas on counter reset. Default-route interface only; exclude loopback/Docker/VPN. Raw bytes/sec. |
| Target probes | 2 s | All targets concurrent (goroutines + WaitGroup + context). 3 s dial timeout = loss. One slow target must not serialize the tick. |
| Wi-Fi | 20 s | Expensive calls cached for the interval. |
| LAN | 30 s or on change | Default-route interface selection. |
| Geo | 5 min | ≈8,640 req/month, under ipinfo's free tier. Failure keeps last good value with `stale: true`; never blocks other collectors. |

### 10.1 Wi-Fi

| OS | Pitfall | Required approach |
|---|---|---|
| Windows | `netsh wlan show interfaces` output is localized; spawning flashes a console | Call `wlanapi.dll WlanQueryInterface` via `x/sys/windows`, or parse structurally by position/colon. Always `HideWindow` + `CREATE_NO_WINDOW`. Derive band from an explicit field, not channel number (6E ambiguity). |
| Linux | `nmcli` absent on iwd/systemd-networkd; signal is dBm | `nmcli -t -f …` terse mode; fallback `iw dev <if> link`. Normalize once: `clamp(2*(dBm+100), 0, 100)`, documented. |
| macOS | `airport` removed in Sonoma 14.4; `networksetup` disabled in 15; SSID redacted without Location Services (TCC grants only to signed notarized bundles) | Keep `system_profiler SPAirPortDataType`, poll 20 s, cache. Treat `<redacted>`/empty SSID as a first-class "unavailable (permission)" state → `--`. |

### 10.2 LAN plumbing

- **DNS servers:** Windows `GetAdaptersAddresses` (not localized `ipconfig /all`). Linux prefer `resolvectl` or `/run/systemd/resolve/resolv.conf` — the `127.0.0.53` stub is useless to display. macOS `scutil --dns`, dedupe scoped resolvers.
- **Gateway ping:** unprivileged ICMP (`x/net/icmp`, udp4) first — Linux needs `net.ipv4.ping_group_range`, macOS allows it, Windows raw ICMP usually needs admin. Fall back to TCP connect on 80/443/53. Record `gateway_ping_method`. Never shell out to `ping` per tick.
- **Interface/IP/gateway:** `net.Interfaces()` order is arbitrary. Select by the UDP-dial trick (§6). Refresh on change or every 30 s.

### 10.3 Probe semantics

- TCP-connect measures the **handshake only** — document this in the README.
- A DNS failure counts as a loss sample (the attempt failed).
- `dns_ms` is misleading with the system resolver (cached after first lookup). Document caching semantics; direct resolver queries via `miekg/dns` arrive at v0.4.
- `resolved_ip` = latest result; CDN rotation is normal.

---

## 11. `--full` system overlay (v0.2)

Off by default. With `--full`, system collectors start and system data appears in the feed/TUI/JSON. **Nothing is persisted.**

| Collector | Interval | Source |
|---|---|---|
| CPU usage | 2 s | gopsutil, primed at startup |
| RAM | 2 s | `UsedPercent` (MemAvailable-based on Linux), not total−free |
| GPU | 5 s | `nvidia-smi` subprocess; AMD Linux sysfs; else null |
| Temperatures | 10 s | Linux hwmon; Windows often unavailable; macOS limited |
| **Disk space** | **30 m** | Fixed local mounts only |

### 11.1 Disk rules

- Usage % per fixed local mount. **Skip network/mapped drives** (a hung Windows mapped drive can block `statfs`), virtual/snap/overlay mounts, and optical drives.
- 2 s timeout guard per mount so one hung filesystem cannot stall the collector.
- First reading taken at startup, then every 30 minutes.

### 11.2 Temperature reality — honest N/A

- **Linux:** hwmon (`coretemp`, `k10temp`) via gopsutil works.
- **Windows:** gopsutil sensor readings frequently empty; `MSAcpi_ThermalZoneTemperature` unreliable; real values need admin + a driver → `--`.
- **macOS:** Intel via SMC; Apple Silicon exposes nothing without `sudo powermetrics` → `--`.
- **NVIDIA:** `nvidia-smi --query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total --format=csv,noheader,nounits`, ~50–150 ms per call, cached.
- **AMD Linux:** sysfs `gpu_busy_percent`, hwmon, `mem_info_vram_used/total`.
- **Intel iGPU / Apple:** largely unavailable → `--`.
- `gpus` is an **array** (iGPU + dGPU laptops), VRAM included.

### 11.3 Feed rendering with `--full`

A system line every 5th tick (10 s), plus a disk line whenever the 30-minute check lands:

```
10:45:10  sys cpu 28% 55C  ram 45% 7.2/16G  gpu 2% 38C  vram 1.0/10G
10:45:10  dsk C: 62% 412/931G  D: 88% 1.7/2.0T
11:15:10  dsk C: 62% 412/931G  D: 88% 1.7/2.0T
```

Unavailable sensors print `--`: `sys cpu 28% --  ram 45% 7.2/16G  gpu -- --`.

---

## 12. CLI contract

```
seermon                              # append-only feed (v0.1); TUI from v0.3 when TTY
seermon --plain                      # force append-only feed (v0.3+)
seermon --json                       # one snapshot, then exit
seermon --stream                     # NDJSON, one object per tick
seermon --full                       # add system telemetry (memory only)
seermon --deeper                     # v0.4+: device attribution, read-only + manual
seermon daemon                       # v0.6+: headless, automation per config
seermon init                         # write default targets.yaml + seermon.yaml
seermon config check                 # validate both files, exit
seermon version
```

| Flag | Behavior |
|---|---|
| `--json` | Snapshot after ~1.5–2 s warm-up: two traffic reads 1 s apart, one concurrent probe round. Rolling fields from available samples with `samples: n`, or null. |
| `--stream` / `--watch` | NDJSON, full windows once warm. The long-running agent mode. |
| `--duration 60s` | Run exactly long enough for a full window, then emit one object. |
| `--timeout` | Bounds total runtime. |
| `--targets host:port,…` | Override `targets.yaml` for this run. |
| `--config <path>` / `--targets-file <path>` | Explicit config paths. |
| `--interval <dur>` | Feed tick / probe cadence override. |
| `--no-log` | Disable CSV writes entirely. |
| `--log-dir <path>` | Override log directory. |
| `--no-geo` | Skip ipinfo entirely; `public` becomes null. |
| `--redact` | Mask public IP, geo, SSIDs, LAN IPs; with `--deeper` also device host/id and node ids/names. |
| `--no-color` / `--ascii` / `--verbose` / `--quiet` | Feed rendering controls. |

### 12.1 Exit codes

| Context | 0 | 1 | 2 | 3 |
|---|---|---|---|---|
| feed / json / stream | healthy | degraded | critical | collection error |
| `deeper` actions (v0.5+) | ok | action failed | refused by guard (reason printed) | device unreachable |
| `daemon` | — | — | — | fatal startup/config error only |

Health verdict: all targets online → healthy; partial → degraded; >50% offline → critical. Feed/stream exit with the code of the last snapshot on Ctrl-C.

### 12.2 Deeper subcommands (v0.5+)

```
seermon deeper status [--json]
seermon deeper stats [--window 24h] [--json]
seermon deeper switch-node <id|name>
seermon deeper set-mode <smart|full>
seermon deeper refresh-tunnel
seermon deeper reboot                    # confirms; shows remaining daily cap
seermon deeper fallback-wifi [<ssid>]
seermon deeper return                    # manual trial reconnect now
seermon deeper hold <duration> | unhold
seermon deeper kill-switch on|off
```

---

## 13. JSON schema v1

`schema_version: 1`. Stable field names, RFC3339 UTC, null-not-zero, additive-only evolution.
`system` is **absent** without `--full`. `deeper` is **absent** without `--deeper`.

```json
{
  "schema_version": 1,
  "timestamp": "2026-11-20T10:45:00Z",
  "mode": "snapshot",
  "uptime_sec": 932,
  "system_uptime_sec": 86400,
  "network": {
    "wifi": {
      "available": true, "ssid": "DeeperNet-5G", "band": "5 GHz", "channel": 36,
      "standard": "802.11ac", "rx_mbps": 433.3, "tx_mbps": 433.3,
      "signal_percent": 94, "signal_dbm": -53
    },
    "lan": {
      "interface": "wlan0", "local_ip": "192.168.1.73", "gateway_ip": "192.168.1.1",
      "gateway_ping_ms": 3.0, "gateway_ping_method": "icmp",
      "dns_servers": ["8.8.8.8", "1.1.1.1"]
    },
    "public": {
      "ip": "59.126.203.192", "city": "Taichung", "country": "TW",
      "isp": "Chunghwa Telecom Co., Ltd.",
      "fetched_at": "2026-11-20T10:44:31Z", "age_sec": 29, "stale": false
    },
    "traffic": { "interface": "wlan0", "down_bytes_per_sec": 37376, "up_bytes_per_sec": 16179 },
    "summary": {
      "window": 30, "targets_online": 8, "targets_total": 8,
      "gateway_loss_percent": 0.0, "internet_loss_percent": 0.0, "internet_avg_ms": 212
    },
    "targets": [
      { "host": "google.com", "port": 443, "status": "ok",
        "dns_ms": 1.0, "resolved_ip": "74.125.23.102",
        "tcp_ms": 132.0, "avg_ms": 226.0, "loss_percent": 0.0,
        "samples": 30, "error": null }
    ]
  },
  "system": {
    "cpu": { "usage_percent": 28.0, "temp_celsius": 55.0 },
    "memory": { "total_mb": 16384, "used_mb": 7405, "used_percent": 45.2 },
    "gpus": [
      { "name": "NVIDIA RTX 3080", "usage_percent": 2.0, "temp_celsius": 38.0,
        "vram_used_mb": 1024, "vram_total_mb": 10240 }
    ],
    "disks": [
      { "mount": "C:", "total_gb": 931.0, "used_gb": 412.0, "used_percent": 62.0,
        "checked_at": "2026-11-20T10:15:10Z" }
    ]
  },
  "deeper": {
    "enabled": true, "reachable": true, "device_id": "…",
    "mode": "smart", "active_node": { "id": "…", "name": "DE-04" },
    "state_stale": false,
    "last_cycle": {
      "ts": "2026-11-20T10:45:00Z", "verdict": "healthy",
      "dns_poisoned": false, "dns_system_ms": 12.0, "dns_doh_ms": 45.0,
      "https": [ { "name": "google", "status": 200, "connect_ms": 130,
                   "tls_ms": 210, "ttfb_ms": 340, "error": null } ],
      "hop_deeper_ms": 3.0, "hop_router_ms": 1.2, "exit_ip": "59.126.203.192"
    },
    "failover": {
      "auto": false, "tiers_enabled": [], "kill_switch": false, "hold_until": null,
      "streaks": { "path_fail": 0, "link_fail": 0, "isp_fail": 0 },
      "cooldowns_remaining_sec": {}, "actions_last_hour": 0, "reboots_today": 0,
      "fallback": { "active": false, "ssid": null, "since": null, "next_return_trial": null }
    },
    "last_action": null,
    "node_ranking": []
  }
}
```

Field rules:

- Unavailable sensor ⇒ `null` (or `"available": false` for Wi-Fi). Never `0`.
- `status` ∈ `ok | dns_error | timeout | connect_error`; `error` carries a human-readable reason.
- `avg_ms` = mean of successful connects in window; null if none. DNS failure = loss sample.
- `loss_percent` rounded to 1 decimal.
- `internet_*` derives from a target quorum (≥50% reachable). `1.1.1.1` is an ordinary row, not the verdict.
- Traffic is raw bytes/sec; the feed/TUI formats to K/s, M/s.
- `disks[].checked_at` makes the 30-minute staleness explicit.
- Geo failure keeps the last good value with `stale: true`; `--no-geo` ⇒ `public: null`.
- LAN examples are `192.168.x.x` everywhere.

---

## 14. TUI (v0.3)

Bare `seermon` becomes an in-place redrawn panel **when stdout is a TTY**. `--plain` forces the v0.1 append feed. Non-TTY auto-falls back to plain.

Views are **pure functions** `render(snapshot, width, theme) → string` behind a thin `tea.Model` shell. Snapshot published → one message → one re-render. A 1 s tick refreshes relative age text only.

```
 SEERMON  up 00:15:32  ● 8/8 online                        q quit · ? help
─────────────────────────────────────────────────────────────────────
 Wi-Fi  DeeperNet-5G 5GHz ch36 94%    ↓ ▁▂▃▅▂▁▁▂ 36.5 KB/s
 LAN    192.168.1.73 → gw .1 (3ms)    ↑ ▁▁▂▁▁▁▂▁ 15.8 KB/s
 Public 59.126.203.192 Taichung TW (29s)
─────────────────────────────────────────────────────────────────────
 TARGET              DNS   RESOLVED IP      TCP ms   AVG   LOSS
 google.com:443      1ms   74.125.23.102       132   226   0.0%
 baidu.com:443      22ms   39.156.66.10        470   455   0.0%
 …
─────────────────────────────────────────────────────────────────────
 loss(30): gw 0% · inet 0% · avg 212ms
```

With `--full`, a system block appears (bars `▓▓▓░░░░░`, temps, disk with its `checked_at` age). With `--deeper`, a Deeper card and a Nodes tab appear.

Rules:

- Sparklines: `▁▂▃▄▅▆▇█`, one column per ring sample, ~50 lines of code, **no chart library**. NaN → gap.
- Same threshold colors as the feed. Null renders `--`, never a zero bar.
- `NO_COLOR`, `--no-color`, `--ascii`, `--redact` all honored identically.
- Windows 10+; VT processing enabled programmatically; Windows Terminal recommended, legacy conhost degrades gracefully.
- Works over SSH: `ssh -t host seermon` is the remote dashboard.
- v0.6+: attach to a running daemon via IPC (badge `attached: daemon`), zero local collectors, no CSV writes.

---

## 15. Deeper module (v0.4+)

Enabled only by `--deeper`. Compiled in, inert otherwise.

### 15.1 Phase 0 — live discovery, before any code

Capture from the admin UI's network traffic:

- Exact request shapes for `switchNode`, `setDpnMode`, `wifi/connect`, and the auth flow.
- The management address; which SSID is the Deeper device's (admin UI, or empirically — the SSID whose gateway answers on the management port).
- Whether the firmware exposes upstream-Wi-Fi endpoints.
- Whether the PC account has rights to connect WLAN profiles.

Store as scrubbed golden fixtures. **Nothing else in this section starts until Phase 0 exits.**

### 15.2 Device client

Auth: `POST /api/admin/validateToken` with the admin password → bearer token, **memory only**, re-login on 401. `GET /api/admin/getDeviceId` is public and doubles as the unauthenticated reachability check.

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/api/smartRoute/getDpnMode` | route mode (smart vs full) |
| GET | `/api/smartRoute/listTunnels` | configured tunnels / active node |
| GET | `/api/smartRoute/listTunnelOptions` | available node choices |
| GET | `/api/smartRoute/nodeLbMode` | load-balance mode |
| POST | `/api/smartRoute/switchNode` | T1 |
| POST | `/api/smartRoute/setDpnMode` | T2 |
| POST | `/api/smartRoute/refreshTunnel` | T3 |
| POST | `/api/smartRoute/addTunnel` / `deleteTunnels` | manual only |
| POST | `/api/admin/reboot` | T4 |
| GET | `/api/wifi/upstreamList` | upstream Wi-Fi list (if supported) |
| POST | `/api/wifi/connect` / `forget` | device-side Wi-Fi (if supported) |

Capability map probed at startup + every 10 min; absent endpoints ⇒ that tier is skipped and logged once. Unknown JSON fields tolerated. 3 s timeout per call; one retry on transient errors for **reads only**. Action POSTs are fire-once — never blind-retried; outcome verified by subsequent state reads.

### 15.3 Deep cycle — every 30 s, 10 s deadline, sub-probes concurrent

1. **Attribution stamp:** device mode + active node (last-known with `stale: true` on failure); SSID fresh per cycle via platform API, falling back to the 20 s cache with recorded age.
2. **DNS poisoning — corrected rule:**
   - **Poisoned** ⇐ system answer is `0.0.0.0`, unroutable, or RFC1918 for a public name, **or** NXDOMAIN/empty while DoH resolves successfully.
   - Disjoint-but-plausible public IP sets are **not** poisoning (CDN geo-rotation is normal) — only a soft signal when HTTPS also fails.
   - Poisoning may only be declared when the DoH reference **succeeded**.
   - **DoH bootstrap:** custom `DialContext` pins the DoH host to `dns_doh_bootstrap_ips`; TLS SNI/verification stays on the hostname. A poisoned system resolver cannot break the reference channel.
3. **HTTPS reachability:** HEAD per target (GET if a keyword is configured), `httptrace` recording dns/connect/TLS/TTFB ms. **Any HTTP response within timeout = reachable** — a 405 to HEAD is a live path. `expect_status`/keyword mismatches are quality flags, not reachability failures.
4. **Hop baselines:** TCP dial `device_gateway_ip` (management port, then 80/443) and `router_lan_ip` (80/443/53). ICMP best-effort. Evaluated **before** DNS/HTTPS interpretation.
5. **Exit IP (optional):** reuse `geo.ip` when fresh; else dedicated check every N cycles. Never required for a verdict.

### 15.4 Cycle verdict — first match wins

| # | Observation | Verdict | Action eligibility |
|---|---|---|---|
| 1 | Device gateway unreachable | `device_link_issue` | No API action possible. T5 track |
| 2 | Router LAN unreachable (device gw OK) | `isp_issue` | **Never** node/mode actions. T5 long-streak only |
| 3 | DNS poisoned + HTTPS majority fails | `bad_node` | T1→T2→T3→T4 ladder |
| 4 | DNS clean, hops OK, HTTPS majority fails | `upstream_issue` | Same ladder, shared streak |
| 5 | DNS clean, hops OK, single HTTPS target fails | `healthy` | None — site flag noted |
| 6 | All checks pass | `healthy` | None — resets streaks |
| 7 | Anything else / insufficient data | `unknown` | None — logged, streaks frozen |

Majority = strictly more than half of configured targets. Streaks: `path_fail` (3–4), `link_fail` (1), `isp_fail` (2). `healthy` resets all; `unknown` freezes them.

### 15.5 `deeper.csv` — exact header

```
ts,mode,node_id,node_name,ssid,ssid_age_sec,verdict,dns_system_ms,dns_doh_ms,dns_poisoned,https_ok,https_total,https_detail_json,hop_deeper_ms,hop_router_ms,exit_ip
```

2,880 rows/day ≈ 400 KB/day. Same rotation and retention as `probes.csv`.

### 15.6 Tiers

| Tier | Action | Trigger | Cooldown | Verify |
|---|---|---|---|---|
| T1 | Switch to best-ranked **other** node | `path_fail ≥ 3` | 5 m | 2 cycles |
| T2 | Toggle smart↔full | T1 tried/cooled, still failing | 10 m | 2 cycles + `mode_hold` |
| T3 | `refreshTunnel` | T2 tried/cooled, still failing | 15 m | 2 cycles |
| T4 | Device reboot | T1–T3 exhausted in episode | 20 m, cap 2/day | `boot_wait` + API reachable + 2 cycles |
| T5 | PC Wi-Fi fallback | §15.8 conditions | 10 m | 1 cycle on fallback |

Ladder: `ladder_pos` advances only when the previous tier was attempted-and-verified-failed or is in cooldown. **Two consecutive healthy cycles end the episode.** T2 revert rule: fails verification → revert (counts as a T2 action); holds `mode_hold` → keep. T1 candidates come from ranking with the `min_samples` gate, else config preference order; current node excluded.

### 15.7 Guards — executor-enforced, every entry point

Checked in order: **kill switch → manual hold → boot grace → single-flight lock → hourly cap (12) → tier cooldown → tier daily cap (T4)**.

Refusals logged to `actions.csv` as `status=skipped` with a reason. Lock lives in `state.json`: `lock_owner` = pid+boot-id, TTL = max(5 m, `boot_wait`+2 m); stale reclaim logged loudly. Every manual action resets streaks and starts a 2-cycle post-manual grace.

### 15.8 T5 detail — Windows-only in v1

Triggers: (a) ladder exhausted **and** `path_fail ≥ 6`; (b) `link_fail ≥ 6`; (c) `isp_fail ≥ 10`. Other OSes skip the tier, logged.

Execution via `WlanConnect` (`x/sys/windows`) to saved profiles in config order — not a `netsh` subprocess. **Pre-step:** if the PC is unexpectedly off `deeper_wifi`, first try reassociating to it (logged `t5/reassoc`, 2 m cooldown).

Once on fallback the device API is unreachable — **act from stored state only.**

**Auto-return:** after `return_probe_after` (10 m) on fallback, and only while the fallback path itself is healthy, run a **trial reconnect**: join `deeper_wifi`, run one deep cycle within 90 s. Healthy → stay, reset state. Failing → return to fallback, backoff ×2 (10 → 20 → 40 m, cap 60 m). Optional: require device liveness via `device_upstream_ip` before spending a trial.

### 15.9 Ranking

From `deeper.csv` over rolling 24 h / 7 d, grouped by (node, mode): cycle count, healthy %, MTBF, mean recovery time, DNS-poisoning rate, last-seen. Rank by healthy % → lower poisoning rate → higher MTBF. Below `min_samples` (20), config preference order wins. ~86k rows at 30 days parses into memory in well under a second — no database needed.

### 15.10 Crash consistency

`actions.csv` row written `started` **before** dispatch. On restart, `started` rows older than lock TTL → `interrupted`, conservatively counted against the hourly cap, streaks reset, fallback schedule resumed from `state.json`.

### 15.11 The engine may never

Act on fast-tier data or the display quorum · switch nodes during `isp_issue` · act during hold or boot grace · exceed caps · run outside `daemon` mode · consult geo, an LLM, or any cloud service for a decision.

---

## 16. Milestones & exit criteria

### M0 — Scaffold
Go module, MIT license, `.gitignore`, CI skeleton, config loaders for both files with validation, `seermon init`, `seermon config check`, `seermon version`.
**Exit:** builds with `CGO_ENABLED=0` on Windows/Linux/macOS; `config check` catches every invalid case in §7.3.

### M1 — v0.1 · Bare network feed
Snapshot store, concurrent target probes, traffic, LAN, gateway ping, Wi-Fi, geo. Append-only feed with tick + event lines. CSV writer with rotation and prune. `--json`, `--stream`, `--targets`, `--no-log`, `--no-geo`, `--redact`, `--verbose`, `--no-color`, `--ascii`. Exit codes.
**Exit:**
- Golden JSON validates against §13 (nulls, samples, dual uptimes, RFC3339).
- Golden CSV header + row fixtures match byte-for-byte.
- 1 h continuous run: no goroutine leak, no memory growth, CSV rows land on schedule, geo failure shows `--` and keeps the last good value.
- Feed pipes cleanly to a file; Ctrl-C flushes and exits with the right code.

### M2 — v0.2 · `--full`
System collectors (CPU/RAM 2 s, GPU 5 s, temps 10 s, **disk 30 m**), memory-only ring buffers, system feed lines, `system` block in JSON, honest `--` everywhere.
**Exit:**
- **Disk-write audit: with `--full` running for 1 h, no system metric appears in any file on disk.** Automated test.
- Without `--full`, the `system` key is absent and no system collector goroutine starts.
- Hung-mount simulation does not stall the disk collector.
- NVIDIA/AMD/hwmon paths tested where available; unavailable paths render `--`, never `0`.

### M3 — v0.3 · TUI
bubbletea panel, sparklines, targets table, `--full` system block, `--plain` fallback, non-TTY detection, attach-ready view layer.
**Exit:** golden frames pass (ANSI-stripped, fixed widths, null/stale/redacted/degraded states) on Windows Terminal, conhost, Linux, macOS, and over SSH. Platform parity for Wi-Fi/LAN collectors achieved.

### M4 — v0.4 · Deeper discovery + read-only
Phase 0 capture. Device read client + capability map. Deep cycle with DoH bootstrap and corrected poisoning rule. `deeper.csv`. `--deeper` flag. `deeper status`.
**Exit:** Phase 0 fixtures committed; **≥24 h read-only soak** with every cycle attributed (or explicit null + reason), verdicts sane, zero fake zeros.

### M5 — v0.5 · Manual actions
Executor + all guards + `state.json` lock + caps + kill switch + hold. Every `deeper` subcommand. TUI action keys with confirms and refusal messages.
**Exit:** each action validated live **one at a time**; guard-refusal tests pass (exit code 2 with reason); T5 fallback + return trial rehearsed manually end-to-end.

### M6 — v0.6 · Automation + daemon
Ranking. Decision engine + ladder, `auto: false` default. `seermon daemon`. Loopback IPC (`/v1/health`, `/v1/snapshot`, `/v1/stream`, `/v1/stats`, `/v1/action`, `/v1/killswitch`, `/v1/hold`) with an owner-only token file. TUI/CLI attach.
**Exit:**
- Sim proofs: no action on ISP outage, no flap action from the fast tier, `unknown` freezes streaks, exact expected action sequences on scripted cycles.
- Caps/cooldowns/kill switch/hold honored in sim and live.
- Staged real-world enable **T1→T2→T3→T4→T5**, ≥48 h soak per tier.
- Attached clients write nothing to disk.

### M7 — v1.0 · Polish + packaging
`--duration`/`--timeout` polish, boundary tests, Windows service install (`x/sys/windows/svc`, scheduled-task fallback), CI matrix, release binaries.
**Exit:** one static binary per OS; redaction grep test zero hits; daemon unaffected by TUI crash (fault injection).

---

## 17. Testing & CI

- **Golden fixtures:** every parser (localized netsh/ipconfig, `nmcli -t`, redacted `system_profiler`) and, from v0.4, device API requests/responses with secrets scrubbed.
- **CSV schema:** header golden per file; a schema change requires a deliberate bump plus a new fixture.
- **No-persist audit (M2 gate):** run with `--full`, snapshot the state dir before/after, assert no system metric appears in any written byte.
- **Threshold boundaries:** 69.9 / 70 / 84.9 / 85 · latency 299 / 300 / 800 / 801 · loss 0 / 1 / 2 of 30 (3.33% granularity documented).
- **Ring math:** wraparound, cold start (`samples: 1`), all-failed window (`avg_ms` null).
- **Feed goldens:** tick lines, every event type, `--ascii`, `--no-color`, `--redact`, unavailable-everywhere.
- **TUI goldens (v0.3):** pure view functions at fixed widths, ANSI-stripped.
- **Verdict matrix (v0.4):** all 7 rows + priority conflicts — poisoned DNS *and* dead router LAN ⇒ `isp_issue`, not `bad_node`. CDN-rotation answers must not flag poisoning; DoH-down must not flag poisoning.
- **Ladder sim (v0.6):** fake clock + fake device + scripted cycles → exact action sequences.
- **Guard tests:** kill switch, hold, boot grace, lock contention + stale reclaim, hourly cap, reboot cap, cooldown boundaries, post-manual grace, crash reconciliation.
- **Redaction grep:** every output surface (feed, JSON, NDJSON, CSV, state.json, IPC, TUI frames) scanned for the fixture secret — zero hits.
- **Chaos:** interface change mid-cycle, Wi-Fi drop, counter reset, DNS timeout, geo 429, disk mount hang, device unreachable mid-action, daemon killed mid-T4.
- **CI:** windows/linux/macos, fully headless, sensors and device mocked. `CGO_ENABLED=0` across the board.

---

## 18. Packaging

- `CGO_ENABLED=0` on all three platforms; one static binary each.
- Version stamped via `-ldflags "-X main.version=…"`.
- Checksums + GitHub release artifacts.
- Windows: service install at v1.0; before that, run in a terminal or as a scheduled task.
- README documents: TCP-connect measures the handshake only; system-resolver DNS caching semantics; the dBm→% formula; why some sensors are permanently `--` on some platforms.

---

## 19. Deferred (post-v1)

- SQLite (CSV is sufficient at these volumes; revisit only if query latency becomes a real problem).
- Browser dashboard over the existing IPC endpoints; non-loopback IPC with TLS (until then `ssh -L 8788:127.0.0.1:8788 host`).
- macOS / Linux Wi-Fi fallback (T5); signed macOS helper bundle for real SSID.
- Runtime target editing from the TUI; multi-device support; NVML.
- A native GUI. The snapshot architecture makes it just another consumer if it's ever wanted.

---

## 20. What changed from the prior drafts

| Topic | Decision |
|---|---|
| Name | `netmon` → **`seermon`** (name collision on GitHub) |
| First interface | Append-only feed, not a GUI, not a TUI. TUI moves to v0.3. |
| Persistence | SQLite → **simple CSV** + `state.json`. No database dependency at all. |
| Targets | Moved out of the main config into **`targets.yaml`**. |
| Geo | On by default, `--` on failure, `--no-geo` to opt out. |
| System telemetry | Behind **`--full`**, memory-only, **never persisted**. |
| Disk | Usage checked every **30 minutes**, fixed local mounts only, timeout-guarded. |
| Deeper | Deferred to v0.4+ behind **`--deeper`**; automation needs a second key (`daemon` + `auto: true`). |
| Cadence conflict | Two tiers: fast 2 s for display/CSV, deep 30 s for the engine. Engine never sees fast-tier flaps. |
| Verdict conflict | Quorum = display; failure matrix = tier selection. Never crossed. |
| "Mismatch = poisoned" | **Corrected:** sinkhole/NXDOMAIN-while-DoH-works only; CDN variance excluded; DoH bootstrapped by pinned IP. |
| HEAD 405 | Any HTTP response = reachable; status/keyword are quality flags. |
| Auto-return | **Finalized:** trial reconnect with hysteresis + exponential backoff, only while fallback is healthy. |
| Device-link failure | No API action possible → reassoc pre-step, then T5 track. Never node churn. |
| Wi-Fi actions | `WlanConnect` via `x/sys`, not a `netsh` subprocess. T5 Windows-only in v1. |
| Schema | Fresh `schema_version: 1`; `system` and `deeper` are additive optional blocks. |
| Gates | Phase 0 capture, 24 h observation, manual-before-auto, cheapest-first staging — all preserved. |

---

**Start at M0. The first thing you should be able to do is type `seermon` and watch the network.**
