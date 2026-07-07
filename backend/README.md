# Backend

The backend provides `chswapctl`, whose public frontend-facing interface is:

```sh
chswapctl api METHOD PATH [--json BODY]
```

Implementation requirements:

- return JSON envelopes documented in `../contracts/chswap-api.v1.yaml`;
- return JSON on stdout for both success and API-level failure;
- use exit code `0` for `ok=true` and non-zero for `ok=false`;
- validate profiles before saving or applying;
- serialize apply operations with a lock;
- keep sysfs and cgroup writes behind strict allowlists;
- implement dry-run, runtime apply, full-control apply, reconcile, daemon
  takeover, and diagnostics flows.
- store boot/apply summaries under `data/state/`;
- store logs under `data/logs/`.

Runtime:

- source: `chswapctl/`;
- build: `./backend/build.sh`;
- target: static `linux/arm64` ELF at `module/bin/chswapctl`;
- tests: `./backend/run-tests.sh`.

Management-surface gate:

- `GET /v1/capabilities`, `GET /v1/status`, `GET /v1/apps`,
  `GET /v1/profile`, `POST /v1/profile/validate`, `GET /v1/boot/status`,
  and `POST /v1/diagnostics/export` may run read-only when required stage-3
  Crystal Hybridswap nodes are missing;
- profile save, apply, boot apply, and per-app force-swap return
  `E_KERNEL_NODE_MISSING` when their required kernel nodes are absent;
- frontend must stop at the management-unavailable view when
  `frontend_gate.allowed=false`.

Boot scripts in `../module/` should call this backend instead of duplicating
apply logic.

Backend capabilities are derived from the actual zram and memcg nodes exposed
by the running kernel, not from a separate support flag.
