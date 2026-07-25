# KernelSU Module Packaging

> This module is not standalone. It requires the matching custom
> `crystal_hybridswap` kernel; on stock or incompatible kernels it is
> intentionally fail-closed and has no memory-management effect.

Packaged files:

```text
module.prop
launcher.png
skip_mount
oplus-policy.sh
post-fs-data.sh
service.sh
boot-completed.sh
daemon.sh
uninstall.sh
webroot/
bin/
```

The module root `launcher.png` is referenced by `actionIcon` for module-manager
icon display. `webroot/config.json` points WebUI X shortcut creation at
`webroot/launcher.png`; keep both copies in sync.

Boot script roles:

- `oplus-policy.sh`: install-time ColorOS XML tuning and early-boot bind mounts
  that delay vendor background cleanup on supported Oplus layouts.
- `post-fs-data.sh`: early full-control attempt with bounded timeout.
- `service.sh`: mid-boot reconcile and detached daemon launch.
- `boot-completed.sh`: final verification, runtime reconcile, status update.

## Oplus App-Management Policy

During installation, `oplus-policy.sh` copies the device's
`/my_stock/etc/extension/sys_osense_appmng_decisionmaker_config.xml` and only
patches the 16 GiB entries for the normal and `C*` variants of `app_switch`
and `launcher`. Quick-boot (`*_qb`) entries and every other RAM tier remain
unchanged.

The generated overlay applies these level-5-to-level-1 values to both the
condition and clear-policy tables:

```text
triggerMemory: 900 | 1200 | 1450 | 1700 | 1900 MiB
purposeMemory: 1150 | 1450 | 1700 | 2000 | 2200 MiB
```

Level 2 changes from `smart-force` to `smart`, so repeated requests are subject
to OSense's adjacent-request suppression. Level 3 remains `smart-force` for
genuine low-memory pressure. The generated XML version is `2099123101` so the
local overlay wins over older common RUS data.

The app-management overlay is fail-closed: if the source lacks the two config
tables, both base scenes, both rules-engine scenes, or their 16 GiB rows, that
file is removed from the generated policy and is not mounted. Other supported
Oplus policy files can still be installed.

## Optional app aging

The WebUI's `老化` page edits the optional `profile.aging` policy. It is off by
default. When enabled, the daemon only invokes `memory.force_swapout` after an
app has been idle for the configured period and `MemAvailable` is below the
configured pressure threshold. After unlock, a lightweight tracker samples the
current crystalfrostwork `/dev/cpuset/top-app/cgroup.procs` PID set every five
seconds and reads only those PIDs' `/proc/status` entries. The complete app/ZRAM
aging cycle remains 60 seconds, and the state file is written only when a UID
enters top-app. The top-app set is checked before and after the apply lock; if it
is unavailable, aging stops for that cycle and the next successful sample resets
the UID timer. Direct zygote processes with `oom_score_adj < 200` remain
protected. At exactly `200`,
the daemon protects the app only when Android reports a foreground service or
active media/audio playback; unreadable Android state fails safe and keeps it
protected. Mixed-UID memcgs are resolved to their one regular application UID
by inspecting live `cgroup.procs`; isolated/WebView UIDs are not treated as
separate applications, so force-swapout still covers the complete memcg. If
the regular UID is absent or ambiguous, the candidate is skipped fail-safe.
Each 60-second cycle writes one compact filter summary (unresolved,
foreground/protected, cooldown and rearm counts); actual force-swap successes
and failures remain detailed. Repeated identical daemon messages are held for
15 minutes and summarized on the next emission. `data/logs/daemon.log` is
rotated at 512 KiB to `.1`. State and cooldowns are stored in
`data/state/aging.json`. When ZMS backing capacity reaches 2% free space, aging
enters a global 30-minute fuse and stops all force-swapout attempts. It resumes
only after capacity reaches 5% free space for two consecutive aging cycles;
`ENOSPC` from any force-swapout also opens this fuse immediately.

Scripts must not implement their own sysfs policy. They should call
`bin/chswapctl api ...` or backend internal commands.

Required calls:

```sh
bin/chswapctl api POST /v1/boot/apply --json '{"phase":"post-fs-data"}'
bin/chswapctl api POST /v1/boot/apply --json '{"phase":"service"}'
bin/chswapctl api POST /v1/boot/apply --json '{"phase":"boot-completed"}'
```

Scripts must never loop forever during boot. If the backend reports degraded
state, record it and let Android continue booting.

`service.sh` launches `daemon.sh` after the service-phase boot apply. The
daemon is the standing owner: it polls managed nodes and reconciles only when
runtime state drifts. If required management nodes are missing, the backend
reports `E_KERNEL_NODE_MISSING` and the daemon supervisor stops doing useful
work.

`webroot/` now contains the shipping no-build WebUI assets. Backend and
module behavior can still be tested directly through `bin/chswapctl api ...`.

Create a release KernelSU/WebUI zip from the project root:

```sh
scripts/release.sh
```

The release script validates scripts, backend tests, frontend modules,
JSON/YAML fixtures, rebuilds the static arm64 backend, packages the module,
and writes a `.sha256` checksum next to the artifact.

For packaging-only local work after a manual backend build, use
`scripts/package-module.sh`. The packaging script includes only install-time
module files, verifies the static arm64 backend binary, excludes runtime state
and development-only WebUI mock assets, and writes the artifact under `dist/`
unless an output path is provided.

The packaging script injects `META-INF/com/google/android/` and
`customize.sh` from the project-level `installer/` directory. Those files are
installer metadata and are intentionally not kept under `module/`.
