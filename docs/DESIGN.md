# Crystal Hybridswap WebUI Design

This document describes the current module architecture for the stage-3
Crystal Hybridswap kernel ABI.

## Goals

- own the selected zram device as early as practical;
- initialize zram, backing storage, swap, and Crystal policy from one profile;
- keep the profile persistent across reboot;
- expose diagnostics and tuning through a WebUI;
- keep frontend and backend separated by a documented local API contract;
- recover from third-party runtime drift with a userspace daemon.

Legacy OPPO score-level policy formats are out of scope. The module does not
write `memory.swapd_memcgs_param` or `memory.swapd_single_memcg_param`.

## Current Kernel ABI

The backend detects capability from actual nodes exposed by the running kernel.
There is no separate support flag.

Required zram management nodes:

- `/sys/block/zramN/initstate`
- `/sys/block/zramN/disksize`
- `/sys/block/zramN/reset`
- `/sys/block/zramN/comp_algorithm`

Crystal zram nodes:

- `/sys/block/zramN/hybridswap_enable`
- `/sys/block/zramN/hybridswap_core_enable`
- `/sys/block/zramN/hybridswap_swapd_pause`
- `/sys/block/zramN/hybridswap_loglevel`
- `/sys/block/zramN/hybridswap_report`
- `/sys/block/zramN/hybridswap_crystal_stat`
- `/sys/block/zramN/hybridswap_loop_device`
- `/sys/block/zramN/hybridswap_dev_life`
- `/sys/block/zramN/hybridswap_quota_day`
- `/sys/block/zramN/hybridswap_zram_increase`

Root memory-cgroup policy nodes:

- `memory.zram_wm_ratio`
- `memory.avail_buffers`
- `memory.erm_avail_buffer_enable`
- `memory.erm_avail_buffer`

If required nodes are missing, capabilities/status return
`frontend_gate.allowed=false` and a `required_nodes_missing` list. Mutating
endpoints fail with `E_KERNEL_NODE_MISSING` when the current request needs
absent nodes.

## Architecture

```text
WebUI (module/webroot)
  |
  | KernelSU exec
  v
chswapctl api METHOD PATH [--json BODY]
  |
  +-- profile storage
  +-- status/capability snapshot
  +-- apply/reconcile lock
  +-- diagnostics export
  +-- daemon drift reconciler
```

The frontend never reads or writes `/sys`, `/proc`, `/dev`, cgroups, or module
configuration files directly. It only calls `chswapctl api`.

## Profile Model

The profile schema is `chswap.profile.v1`. Tunables are three-state:

- `null` means unset: follow the running system and do not reconcile it;
- a concrete value means the daemon should keep that value;
- removed fields are rejected by strict JSON decoding on public API writes.

Structural invariants:

- managed zram device defaults to `zram0`;
- swap is always enabled by profile (`swap.enabled=true`);
- default backing mode is `block`;
- fixed block backing path is `/dev/block/by-name/hybridswap`;
- loop backing is module-managed and optional.

New API writes are strict and reject unknown fields. Stored profiles with
known removed boot fields from older development builds are migrated by
dropping those fields before validation; other invalid stored profiles are
ignored with a warning and fall back to diagnostic mode.

## Apply Behavior

`full-control` mode owns the zram lifecycle:

1. read selected zram status;
2. if zram is inactive or disksize mismatches, swapoff when necessary;
3. reset zram when allowed and possible;
4. optionally set init-only compression algorithm;
5. set disksize;
6. bind backing through `hybridswap_loop_device`;
7. reconcile explicitly-set runtime Crystal and policy fields;
8. run `mkswap` and `swapon -p <priority>` if swap is not active.

If the zram device already matches the desired disksize, the backend reconciles
runtime fields in place and only triggers backing or swap activation when those
are missing.

Writes to init-time zram nodes first add the user write bit when the kernel/init
left the sysfs file at `0444`. This is required for nodes such as `disksize` and
`comp_algorithm` on the target device.

`diagnostic` mode is read-only. Apply returns a skipped step and does not write
kernel nodes.

## Optional app aging

The profile may include an `aging` object. It is disabled by default and is
optional so profiles written by older module versions remain valid:

```json
"aging": {
  "enabled": false,
  "idle_after_sec": 900,
  "min_zram_mb": 64,
  "pressure_available_below_mb": 4096,
  "cooldown_sec": 1800,
  "max_apps_per_cycle": 1
}
```

When enabled, the daemon tracks each app UID's last foreground observation in
`data/state/aging.json`. It only considers an app after the idle period, with
enough ZRAM data, and while `MemAvailable` is below the pressure threshold.
Candidates without an existing ESWAP footprint are selected first, then those
with the lowest ESWAP/original-memory ratio, then by longest idle time and
largest ZRAM footprint. This gives fresh background apps a turn before an app
that has already been substantially offloaded; it is deliberately not a
permanent "already written" exclusion, because an app can later accumulate new
ZRAM pages. The per-cycle limit prevents a burst of writeback.

After unlock, a lightweight tracker reads `/dev/cpuset/top-app/cgroup.procs`
every five seconds and maps only those PIDs through `/proc/PID/status`. It
updates `last_foreground_unix` only when a UID enters the set; the full app/ZRAM
aging cycle remains on the 60-second cadence. The top-app set is checked both
before and after acquiring the apply lock. If the top-app source is unavailable,
automatic aging stops for the cycle and the next successful sample is treated as
a new foreground entry. Foreground and core system package names are excluded.
A mixed-UID memcg is resolved to its one regular application UID by inspecting
live `cgroup.procs`; isolated/WebView UIDs are ignored for identity, while the
resulting force-swapout still covers the complete memcg. If the regular UID is
missing, ambiguous, or the package has multiple matching memcgs, the candidate
is skipped fail-safe. For processes
spawned directly by a system zygote, `oom_score_adj < 200` is always protected.
The exact `oom_score_adj == 200` boundary is protected only when the package
still has a foreground service or its UID appears in Android's active
media/audio playback state. If either `dumpsys` state cannot be read, the
boundary process is protected fail-safe. Low-score children spawned inside the
same app UID are ignored because they can retain stale inherited scores after
ActivityManager demotes the real app processes. The daemon emits one compact
per-cycle summary of filter counts instead of logging every skipped app.
Successful force-swapout enters the configured cooldown. Kernel errors and
`ENODATA` results persist a
failure streak and back off at 5 minutes, 15 minutes, 30 minutes, 1 hour, 2
hours, 4 hours, then at most 6 hours. After three consecutive kernel-error or
no-data outcomes, the memcg must grow by at least the configured ZRAM floor or
25% of its prior original ZRAM footprint before it is rearmed. Quota and device
failures use only the backoff, because they are global conditions rather than a
property of that app. Busy and still-pending work use only a short retry and do
not increase that streak. This reuses
`memory.force_swapout`, so it moves a memcg's existing ZRAM pages to HybridSwap
backing storage rather than doing page-level LRU aging.

When ZMS backing capacity falls to 2% free space, aging enters a global fixed
30-minute storage fuse; this is intentionally separate from per-app cooldowns.
The fuse is reopened only after capacity reaches 5% free space for two
consecutive aging cycles. An actual `ENOSPC` force-swap result opens the same
fuse immediately, without incrementing the app failure streak. Daemon logging
is bounded independently of the aging policy: identical
messages are suppressed for 15 minutes with a repetition count emitted when
the message resumes, and `daemon.log` rotates at 512 KiB, retaining one `.1`
backup.

## Daemon

`chswapctl daemon` is the standing userspace owner. It uses a pidfile plus flock
so only one instance runs. It polls because sysfs/cgroup nodes do not provide a
usable inotify workflow.

Cadence:

- about 1 second before first user unlock;
- about 60 seconds after `/storage/emulated` or `/data/media` appears in
  `/proc/mounts`.

The daemon reconciles only fields explicitly set in the profile. It logs
degraded states and real command errors instead of reporting success after a
failed reset or write.

## Boot Scripts

- `customize.sh` copies supported Oplus/ColorOS memory-policy XML files from
  `/my_stock/etc/extension`, applies the background-retention policy to module
  copies under the module-root `my_stock/etc/extension`, and leaves the
  original partition unchanged.
- The app-management transform keeps the OSense condition and clear-policy
  tables aligned: all available 16 GiB `app_switch*` and `launcher*` normal/C
  scenes receive `900|1200|1450|1700|1900` trigger thresholds and matching
  `1150|1450|1700|2000|2200` purpose targets. Level 2 is changed from
  `smart-force` to throttled `smart`; quick-boot and other RAM tiers are not
  modified. Unsupported layouts are omitted instead of being partially
  mounted.
- `post-fs-data.sh` bind-mounts those copies before the early full-control
  attempt. Missing XML files are skipped so non-Oplus devices are unaffected.
- `post-fs-data.sh`: bounded early full-control attempt.
- `service.sh`: service-phase reconcile and detached daemon launch.
- `boot-completed.sh`: final reconcile and boot status update.

Boot scripts call backend APIs and do not duplicate sysfs policy logic. They
must not block Android boot indefinitely. Phase status is saved under
`data/state/last_boot.json`.

## API Contract

The contract lives in `contracts/chswap-api.v1.yaml`.

Read-only/status endpoints:

```sh
chswapctl api GET /v1/capabilities
chswapctl api GET /v1/status
chswapctl api GET /v1/apps
chswapctl api GET /v1/profile
chswapctl api GET /v1/boot/status
```

Validation/diagnostics endpoints:

```sh
chswapctl api POST /v1/profile/validate --json '<profile-json>'
chswapctl api POST /v1/diagnostics/export
```

Profile save and apply endpoints:

```sh
chswapctl api PUT /v1/profile --json '<profile-json>'
chswapctl api POST /v1/apply --json '{"mode":"full-control"}'
chswapctl api POST /v1/boot/apply --json '{"phase":"service"}'
```

App action endpoints:

```sh
chswapctl api POST /v1/apps/force-swap --json '{"op":"swapout","name":"<memcg>","uid":12345}'
```

`/v1/apply` accepts `dry-run`, `runtime`, `full-control`, and `reconcile`.
The WebUI's main apply action uses `full-control`; boot scripts choose the
phase-specific mode internally.

All responses use the JSON envelope documented in the OpenAPI file. API calls
print JSON to stdout for both success and failure.

## Validation

Backend validation rejects:

- unknown JSON fields;
- invalid zram device names;
- `swap.enabled=false`;
- disksize above `ceil(RAM) * 2 GiB`;
- loop backing size above 32 GiB;
- unsupported compression algorithms when the kernel reports an algorithm list;
- invalid boolean-like integer fields;
- invalid policy ranges.

Some WebUI ranges are intentionally narrower than the raw kernel ABI. For
example, `crystal.dev_life` exposes levels `0..3`, and all
`policy.avail_buffers` values are bounded as MiB-sized uint32 fields so the
saved profile remains a small, predictable policy tuple.

Frontend validation mirrors the backend, but the backend remains authoritative.

## Diagnostics

Diagnostics export includes:

- capabilities/status/profile envelopes;
- `/proc/swaps`;
- selected zram sysfs statistics;
- `hybridswap_report`.

Diagnostics are plain text snapshots. Users should review the exported file
before sharing it.

## Verification

Backend changes should run:

```sh
go test ./...
go vet ./...
./backend/run-tests.sh
```

Frontend changes should at least run JavaScript syntax checks and render all 6
tabs plus the management-unavailable view in mock mode.
