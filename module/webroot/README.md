# WebUI Frontend

This is the shipping WebUI interface for `crystal_hybridswap_webui`. KernelSU
loads `index.html` from this directory.

## Stack

- No build step: plain HTML/CSS + native ES modules.
- Preact + hooks + htm are vendored offline at `vendor/preact.mjs`.
- The WebView never fetches JavaScript, CSS, or data from the network.

## Layout

```text
index.html            # WebUI entrypoint loaded by KernelSU/SukiSU
config.json           # WebUI X title/icon config for shortcut creation
launcher.png          # shortcut icon referenced by config.json
vendor/preact.mjs     # vendored Preact + hooks + htm
css/                  # tokens, base layout, components
js/
  exec.js             # KernelSU ksu.exec() promise wrapper
  api.js              # single backend client
  store.js            # reactive state, polling, profile actions
  profile.js          # default profile and client validation
  format.js           # display and parsing helpers
  router.js           # hash router and tab set
  icons.js            # inline stroke icons
  components/         # shared UI primitives
  pages/              # overview, zram, crystal, pressure, safety, diagnostics
mock/                 # local browser fixtures, excluded from release zips
```

## Backend Boundary

All backend access goes through `js/api.js` only, via:

```sh
chswapctl api METHOD PATH [--json BODY]
```

The frontend never touches `/sys`, `/proc`, `/dev`, cgroups, or KernelSU
configuration directly. Request bodies are POSIX single-quote escaped before
they reach the shell bridge.

Hard gate: when `frontend_gate.allowed` is `false` with
`E_KERNEL_NODE_MISSING`, the UI shows only the management-unavailable view and
does not call profile save, apply, or boot apply; diagnostics export remains
available.

## Local Mock Mode

When `window.ksu` is absent, `js/api.js` serves the fixtures in `mock/`:

```sh
cd module/webroot
python3 -m http.server 8099
```

Open `http://127.0.0.1:8099`. Append `?gate=unsupported` to preview the
management-unavailable view.
