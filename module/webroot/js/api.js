/*
 * api.js — the ONE and ONLY backend client.
 *
 * Every backend call in the app goes through this module. The frontend never
 * reads or writes /sys, /proc, /dev, cgroups, or KernelSU config
 * directly — it only invokes:
 *
 *     chswapctl api METHOD PATH [--json BODY]
 *
 * through the KernelSU exec bridge (js/exec.js). When the bridge is absent
 * (plain browser during development), calls are served from ./mock/*.json so
 * the whole UI — including the unsupported-kernel gate — can be exercised
 * offline. Toggle the mock kernel state with ?gate=unsupported in the URL.
 */

import { exec, hasBridge } from "./exec.js";
import { validateProfile } from "./profile.js";

const DEFAULT_CTL = "/data/adb/modules/crystal_hybridswap_webui/bin/chswapctl";

/** Structured error thrown when the backend returns ok=false. */
export class ApiError extends Error {
  constructor(code, message, details, warnings) {
    super(message || code || "backend error");
    this.name = "ApiError";
    this.code = code || "E_INTERNAL";
    this.details = details || null;
    this.warnings = warnings || [];
  }
}

export function isMock() {
  return !hasBridge();
}

export function mockGate() {
  try {
    return new URLSearchParams(location.search).get("gate") || "supported";
  } catch (_) {
    return "supported";
  }
}

/* ---------------- real device transport ---------------- */

let ctlPath = null;
let ctlPromise = null;

// Resolve the chswapctl binary once. Keep this pinned to the public module id
// so a stale development module cannot accidentally service the WebUI.
// Memoized so concurrent callers share a single probe.
function resolveCtl() {
  if (ctlPath) return Promise.resolve(ctlPath);
  if (!ctlPromise) ctlPromise = doResolveCtl();
  return ctlPromise;
}

async function doResolveCtl() {
  const quoted = shSingleQuote(DEFAULT_CTL);
  const probe = `[ -x ${quoted} ] && echo ${quoted}`;
  try {
    const r = await exec(probe);
    const first = (r.stdout || "").trim().split(/\r?\n/)[0].trim();
    ctlPath = first || DEFAULT_CTL;
  } catch (e) {
    ctlPath = DEFAULT_CTL;
  }
  return ctlPath;
}

// POSIX single-quote escaping: safe against any body content, including quotes.
function shSingleQuote(s) {
  return "'" + String(s).replace(/'/g, "'\\''") + "'";
}

async function execApi(method, path, body) {
  const ctl = await resolveCtl();
  let cmd = `${shSingleQuote(ctl)} api ${method} ${path}`;
  if (body !== undefined) {
    cmd += ` --json ${shSingleQuote(JSON.stringify(body))}`;
  }
  const { errno, stdout, stderr } = await exec(cmd);

  const text = (stdout || "").trim();
  let env;
  if (text) {
    try {
      env = JSON.parse(text);
    } catch (_) {
      throw new ApiError(
        "E_INTERNAL",
        "后端返回了无法解析的输出",
        { stdout: text.slice(0, 4000), stderr: (stderr || "").slice(0, 2000), errno },
        []
      );
    }
  }
  if (!env) {
    throw new ApiError(
      "E_INTERNAL",
      "后端无输出",
      { errno, stderr: (stderr || "").slice(0, 2000) },
      []
    );
  }
  return env;
}

/* ---------------- mock transport ---------------- */

const MOCK_DELAY = 130;

function delay(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function fetchMock(name) {
  const res = await fetch(`./mock/${name}.json`, { cache: "no-store" });
  if (!res.ok) throw new ApiError("E_INTERNAL", `mock ${name} 缺失`);
  return res.json();
}

function ok(data, warnings) {
  return { ok: true, schema: "chswap.api.v1", data, warnings: warnings || [] };
}

function gateClosed() {
  return {
    ok: false,
    schema: "chswap.api.v1",
    error: {
      code: "E_KERNEL_NODE_MISSING",
      message: "kernel does not expose the required Crystal Hybridswap management nodes",
    },
    warnings: [],
  };
}

async function mockApi(method, path, body) {
  await delay(MOCK_DELAY);
  const unsupported = mockGate() === "unsupported";
  const key = `${method} ${path}`;

  // read-only endpoints: always available
  if (key === "GET /v1/capabilities")
    return fetchMock(unsupported ? "capabilities.unsupported" : "capabilities.default");
  if (key === "GET /v1/status")
    return fetchMock(unsupported ? "status.degraded" : "status.active");
  if (key === "GET /v1/apps")
    return fetchMock(unsupported ? "apps.unsupported" : "apps.list");
  if (key === "GET /v1/profile") return fetchMock("profile.envelope");
  if (key === "GET /v1/boot/status") return fetchMock("boot.status");
  if (key === "POST /v1/diagnostics/export")
    return ok({ path: "/data/adb/modules/crystal_hybridswap_webui/data/diagnostics/diagnostics-1783144800000.txt" });

  if (key === "POST /v1/profile/validate") {
    const errors = validateProfile(body);
    if (Object.keys(errors).length) {
      return {
        ok: false,
        schema: "chswap.api.v1",
        error: {
          code: "E_INVALID_PROFILE",
          message: "profile validation failed",
          details: { errors },
        },
        warnings: [],
      };
    }
    return ok({ valid: true, normalized_profile: body });
  }

  // Mutating endpoints honour the hard gate.
  if (unsupported) return gateClosed();

  if (key === "PUT /v1/profile") return ok(body, []);
  if (key === "POST /v1/apply" || key === "POST /v1/boot/apply")
    return fetchMock("apply.runtime");
  return { ok: false, schema: "chswap.api.v1", error: { code: "E_INTERNAL", message: `unmocked ${key}` }, warnings: [] };
}

/* ---------------- core call ---------------- */

// Returns the full envelope. Throws ApiError on ok=false.
async function call(method, path, body) {
  const env = isMock() ? await mockApi(method, path, body) : await execApi(method, path, body);
  if (!env || env.ok !== true) {
    const e = (env && env.error) || {};
    throw new ApiError(e.code, e.message, e.details, (env && env.warnings) || []);
  }
  return env;
}

/* ---------------- endpoint functions (one per contract path) ---------------- */

export const api = {
  getCapabilities: () => call("GET", "/v1/capabilities"),
  getStatus: () => call("GET", "/v1/status"),
  getApps: () => call("GET", "/v1/apps"),
  getProfile: () => call("GET", "/v1/profile"),
  getBootStatus: () => call("GET", "/v1/boot/status"),

  validateProfile: (profile) => call("POST", "/v1/profile/validate", profile),
  putProfile: (profile) => call("PUT", "/v1/profile", profile),
  apply: (req) => call("POST", "/v1/apply", req),
  bootApply: (req) => call("POST", "/v1/boot/apply", req),

  exportDiagnostics: () => call("POST", "/v1/diagnostics/export"),
};
