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
