# Crystal Hybridswap WebUI

This project is the KernelSU/WebUI companion module for the
`crystal_hybridswap` kernel driver.

The module is designed as a full-control ZRAM owner:

- initialize ZRAM before vendor scripts can take over;
- configure Crystal Hybridswap runtime policy;
- persist the selected profile across reboots;
- expose a WebUI frontend for diagnostics and tuning;
- communicate with the backend through a stable local API contract;
- run a persistent daemon that detects and reverts third-party drift after
  ownership is established.

Start here:

- [Design](docs/DESIGN.md)
- [API Contract](contracts/chswap-api.v1.yaml)
- [Example Profile](contracts/examples/profile.default.json)
- [Capabilities Fixture](contracts/examples/capabilities.default.json)
- [Unsupported-Kernel Fixture](contracts/examples/capabilities.unsupported.json)
- [Active Status Fixture](contracts/examples/status.active.json)
- [Degraded Status Fixture](contracts/examples/status.degraded.json)
- [Runtime Apply Fixture](contracts/examples/apply.runtime.json)
- [Boot Status Fixture](contracts/examples/boot.status.json)

Repository layout:

- `backend/`: `chswapctl` implementation and backend tests.
- `module/`: KernelSU module packaging, boot scripts, and the shipping
  no-build WebUI under `module/webroot/`.
- `scripts/`: release and packaging helpers.
- `contracts/`: API schemas and fixtures.
- `docs/`: architecture and kernel ABI notes.

Create a release KernelSU/WebUI module zip:

```sh
scripts/release.sh
```

The release script runs syntax checks, backend tests, `go vet`, frontend
module syntax checks, JSON/YAML parsing, an arm64 backend rebuild, module
packaging, and writes a `.sha256` file next to the zip under `dist/`.

For packaging-only local work after a manual backend build, use
`scripts/package-module.sh`.

Current planning status:

- WebUI/backend communication is API-first through `chswapctl api`;
- old score-level memcg policy is intentionally out of scope;
- full-control mode is designed around bounded boot apply plus a persistent
  daemon takeover loop;
- current API support is derived from real node probing, not a separate
  ownership or support-flag interface.

Current implementation status:

- backend is implemented in Go and built as a static `linux/arm64` ELF;
- KernelSU scripts are thin wrappers around `module/bin/chswapctl`;
- current kernels are detected by the presence of the stage-3
  Crystal Hybridswap zram and memcg management nodes;
- when required nodes are missing, `frontend_gate.allowed=false` and mutating
  API calls fail with `E_KERNEL_NODE_MISSING`.

## License

This project is licensed under GPL-2.0-only. See [LICENSE](LICENSE).
