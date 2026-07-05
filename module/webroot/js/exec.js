/*
 * exec.js — thin Promise wrapper around the KernelSU WebUI exec bridge.
 *
 * KernelSU / WebUI injects `window.ksu.exec(command, optionsJson, callbackName)`.
 * The bridge invokes a global callback `callbackName(errno, stdout, stderr)`.
 * We turn that callback style into a Promise.
 *
 * This module performs NO app logic and knows nothing about the API contract —
 * it only runs a shell command as root and returns its raw result. All command
 * strings are built by js/api.js (the single API client), never by UI code.
 */

let seq = 0;

export function hasBridge() {
  return typeof window !== "undefined" && window.ksu && typeof window.ksu.exec === "function";
}

/**
 * Run a shell command through the KernelSU bridge.
 * @param {string} command
 * @param {{cwd?:string, env?:Record<string,string>}} [options]
 * @returns {Promise<{errno:number, stdout:string, stderr:string}>}
 */
export const EXEC_TIMEOUT_MS = 45000;

// The KernelSU/SukiSU exec bridge dispatches @JavascriptInterface calls on a
// single thread and spawns a fresh root shell per call. Firing several at once
// serializes on that thread and can wedge it, so we run execs one at a time
// through this queue.
let chain = Promise.resolve();

export function exec(command, options = {}) {
  const run = () => execOnce(command, options);
  const result = chain.then(run, run);
  chain = result.then(
    () => undefined,
    () => undefined
  );
  return result;
}

function execOnce(command, options = {}) {
  return new Promise((resolve, reject) => {
    if (!hasBridge()) {
      reject(new Error("KernelSU exec bridge unavailable"));
      return;
    }
    seq += 1;
    const cb = `__chswap_exec_cb_${Date.now()}_${seq}`;
    let timer = null;

    const cleanup = () => {
      if (timer) clearTimeout(timer);
      timer = null;
      try {
        delete window[cb];
      } catch (_) {
        window[cb] = undefined;
      }
    };

    window[cb] = (errno, stdout, stderr) => {
      cleanup();
      resolve({
        errno: typeof errno === "number" ? errno : Number(errno) || 0,
        stdout: stdout == null ? "" : String(stdout),
        stderr: stderr == null ? "" : String(stderr),
      });
    };

    // Prevent a callback that never fires (e.g. a hung shell), so the UI
    // shows an actionable error instead of an infinite spinner.
    timer = setTimeout(() => {
      cleanup();
      reject(new Error("exec 超时（45s 无响应）: " + command.slice(0, 80)));
    }, EXEC_TIMEOUT_MS);

    try {
      window.ksu.exec(command, JSON.stringify(options || {}), cb);
    } catch (err) {
      cleanup();
      reject(err);
    }
  });
}
