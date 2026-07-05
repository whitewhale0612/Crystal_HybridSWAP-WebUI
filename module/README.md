# KernelSU Module Packaging

Packaged files:

```text
module.prop
launcher.png
skip_mount
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

- `post-fs-data.sh`: early full-control attempt with bounded timeout.
- `service.sh`: mid-boot reconcile and detached daemon launch.
- `boot-completed.sh`: final verification, runtime reconcile, status update.

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
