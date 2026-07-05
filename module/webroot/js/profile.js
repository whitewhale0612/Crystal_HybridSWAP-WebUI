/*
 * profile.js — profile editing model + client-side validation (three-state).
 *
 * The profile is a persistent, partially-unset config. A field left null/unset
 * means "follow the system default — the module does not override it". Only
 * fields the user explicitly sets are reconciled by the daemon.
 *
 * The shape here MUST match the backend Profile struct exactly: the backend
 * decodes with DisallowUnknownFields, so any stray or removed key makes the
 * whole PUT fail with E_INVALID_PROFILE.
 * Validation mirrors backend validateProfile(); the backend re-validates.
 */

import { isValidSize, parseSize } from "./format.js";

export const MODES = ["full-control", "diagnostic"]; // diagnostic = 只读逃生舱
export const BACKING_MODES = ["none", "block", "loop"];

export const DEFAULT_LOOP_BYTES = 1073741824; // 1 GiB
export const MAX_LOOP_BYTES = 32 * 1073741824; // 32 GiB

// unset ⇔ null/undefined/"" — the module follows the system value.
export const isUnset = (v) => v === null || v === undefined || v === "";

// Baseline "nothing configured yet": every tunable unset (null). Only
// structural invariants are fixed — device zram0, backing block, swap force-on,
// mode full-control. Boot settings carry fixed defaults.
export function defaultProfile() {
  return {
    schema: "chswap.profile.v1",
    mode: "full-control",
    boot: {
      post_fs_timeout_ms: 8000,
    },
    zram: {
      device: "zram0",
      disksize: null,
      comp_algorithm: null,
    },
    backing: { mode: "block", loop_size_bytes: null },
    swap: { enabled: true, priority: 32767 },
    crystal: {
      enable: null,
      core_enable: null,
      swapd_pause: null,
      loglevel: null,
      dev_life: null,
      quota_day: null,
      zram_increase: null,
    },
    policy: {
      zram_wm_ratio: null,
      avail_buffers: null,
      erm_avail_buffer_enable: null,
    },
  };
}

export function cloneProfile(p) {
  return JSON.parse(JSON.stringify(p));
}

function isInt(v) {
  return Number.isSafeInteger(v);
}

function objectKeys(obj, path, required, optional, errors) {
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) {
    errors[path] = "配置段无效";
    return false;
  }
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(obj)) {
    if (!allowed.has(key)) errors[`${path}.${key}`] = "未知字段";
  }
  for (const key of required) {
    if (!Object.prototype.hasOwnProperty.call(obj, key)) {
      errors[`${path}.${key}`] = "缺少必需字段";
    }
  }
  return true;
}

/**
 * Validate a profile object. Only fields that are SET (non-null) are checked —
 * an unset field is always valid (means "follow system").
 * @returns {Record<string,string>} map of dotted-path -> error message
 */
export function validateProfile(p) {
  const e = {};
  if (!p || typeof p !== "object") return { _: "配置无效" };

  objectKeys(p, "profile", ["schema", "mode", "boot", "zram", "backing", "swap", "crystal", "policy"], [], e);
  if (p.schema !== "chswap.profile.v1") e["schema"] = "schema 不匹配";
  if (!MODES.includes(p.mode)) e["mode"] = "模式非法";

  const boot = p.boot || {};
  objectKeys(boot, "boot", ["post_fs_timeout_ms"], [], e);
  if (!(isInt(boot.post_fs_timeout_ms) && boot.post_fs_timeout_ms >= 1000 && boot.post_fs_timeout_ms <= 30000))
    e["boot.post_fs_timeout_ms"] = "范围 1000–30000 ms";

  // zram — device fixed; disksize/comp_algorithm optional (three-state)
  const z = p.zram || {};
  objectKeys(z, "zram", ["device", "disksize", "comp_algorithm"], [], e);
  if (!/^zram[0-9]+$/.test(z.device || "")) e["zram.device"] = "格式应为 zramN";
  if (!isUnset(z.disksize) && !(isValidSize(z.disksize) && parseSize(z.disksize) > 0))
    e["zram.disksize"] = "非法大小（如 12G）";
  if (!isUnset(z.comp_algorithm) && String(z.comp_algorithm).trim() === "")
    e["zram.comp_algorithm"] = "不能为空";

  // backing — mode + optional loop size
  const bk = p.backing || {};
  objectKeys(bk, "backing", ["mode"], ["loop_size_bytes"], e);
  if (!BACKING_MODES.includes(bk.mode)) e["backing.mode"] = "非法";
  if (
    !isUnset(bk.loop_size_bytes) &&
    !(isInt(bk.loop_size_bytes) && bk.loop_size_bytes > 0 && bk.loop_size_bytes <= MAX_LOOP_BYTES)
  )
    e["backing.loop_size_bytes"] = "LOOP 文件大小须为 1–32 GiB";

  const sw = p.swap || {};
  objectKeys(sw, "swap", ["enabled", "priority"], [], e);
  if (sw.enabled !== true) e["swap.enabled"] = "必须启用";
  if (!(isInt(sw.priority) && sw.priority >= -1 && sw.priority <= 32767))
    e["swap.priority"] = "范围 -1–32767";

  // crystal — unset fields follow the system value.
  const c = p.crystal || {};
  objectKeys(c, "crystal", ["enable", "core_enable", "swapd_pause", "loglevel", "dev_life", "quota_day", "zram_increase"], [], e);
  for (const k of ["enable", "core_enable", "swapd_pause"]) {
    if (!isUnset(c[k]) && ![0, 1].includes(c[k])) e[`crystal.${k}`] = "应为 0 或 1";
  }
  if (!isUnset(c.loglevel) && !(isInt(c.loglevel) && c.loglevel >= 0 && c.loglevel <= 3))
    e["crystal.loglevel"] = "范围 0–3";
  if (!isUnset(c.dev_life) && !(isInt(c.dev_life) && c.dev_life >= 0 && c.dev_life <= 3))
    e["crystal.dev_life"] = "范围 0–3";
  if (!isUnset(c.quota_day) && !(isInt(c.quota_day) && c.quota_day >= 0))
    e["crystal.quota_day"] = "无法解析：纯数字为字节，可带 K/M/G/T 单位（如 10G）";
  if (!isUnset(c.zram_increase) && !(isInt(c.zram_increase) && c.zram_increase >= 0 && c.zram_increase <= 1048576))
    e["crystal.zram_increase"] = "范围 0–1048576 MiB";

  // policy — three-state
  const po = p.policy || {};
  objectKeys(po, "policy", ["zram_wm_ratio", "avail_buffers", "erm_avail_buffer_enable"], [], e);
  if (!isUnset(po.zram_wm_ratio) && !(isInt(po.zram_wm_ratio) && po.zram_wm_ratio >= 0 && po.zram_wm_ratio <= 100))
    e["policy.zram_wm_ratio"] = "范围 0–100";
  if (po.avail_buffers != null) {
    const ab = po.avail_buffers;
    objectKeys(ab, "policy.avail_buffers", ["avail_mb", "min_mb", "high_mb", "free_swap_threshold_mb"], [], e);
    for (const k of ["avail_mb", "min_mb", "high_mb", "free_swap_threshold_mb"]) {
      if (!(isInt(ab[k]) && ab[k] >= 0 && ab[k] <= 4294967295)) e[`policy.avail_buffers.${k}`] = "范围 0–4294967295 MB";
    }
  }
  if (!isUnset(po.erm_avail_buffer_enable) && ![0, 1].includes(po.erm_avail_buffer_enable))
    e["policy.erm_avail_buffer_enable"] = "应为 0 或 1";

  return e;
}

export function isValid(errors) {
  return errors && Object.keys(errors).length === 0;
}

/** Immutable deep set: setPath(obj, "policy.avail_buffers.min_mb", 512). */
export function setPath(obj, path, value) {
  const parts = path.split(".");
  const next = Array.isArray(obj) ? obj.slice() : { ...obj };
  let cur = next;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    cur[k] = Array.isArray(cur[k]) ? cur[k].slice() : { ...cur[k] };
    cur = cur[k];
  }
  cur[parts[parts.length - 1]] = value;
  return next;
}

export function getPath(obj, path) {
  return path.split(".").reduce((o, k) => (o == null ? o : o[k]), obj);
}
