/* format.js — pure display formatting helpers (no side effects). */

const KI = 1024;
const UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

/** Human-readable binary bytes, e.g. 12884901888 -> "12.0 GiB". */
export function fmtBytes(n, digits = 1) {
  if (n == null || isNaN(n)) return "—";
  let v = Math.abs(Number(n));
  const sign = Number(n) < 0 ? "-" : "";
  let i = 0;
  while (v >= KI && i < UNITS.length - 1) {
    v /= KI;
    i += 1;
  }
  const d = i === 0 ? 0 : digits;
  return `${sign}${v.toFixed(d)} ${UNITS[i]}`;
}

/** Split bytes into { num, unit } for large stat displays. */
export function fmtBytesParts(n, digits = 1) {
  const s = fmtBytes(n, digits);
  if (s === "—") return { num: "—", unit: "" };
  const [num, unit] = s.split(" ");
  return { num, unit };
}

/** Parse a size string like "12G", "512M", "0" into bytes (binary units). */
export function parseSize(str) {
  if (str == null) return NaN;
  const m = String(str).trim().match(/^(\d+)([kKmMgGtT])?$/);
  if (!m) return NaN;
  const val = Number(m[1]);
  const unit = (m[2] || "").toLowerCase();
  const pow = { "": 0, k: 1, m: 2, g: 3, t: 4 }[unit];
  const bytes = val * Math.pow(KI, pow);
  return Number.isSafeInteger(bytes) ? bytes : NaN;
}

/** True when a size string is well-formed (used by profile validation). */
export function isValidSize(str) {
  return !isNaN(parseSize(str));
}

/**
 * Bytes -> shortest exact size string for editing: 10737418240 -> "10G",
 * 1536 -> "1536" (falls back to plain bytes when not unit-aligned).
 */
export function fmtSizeCompact(n) {
  const v = Number(n);
  if (n == null || isNaN(v) || v < 0 || !Number.isInteger(v)) return String(n ?? "");
  if (v === 0) return "0";
  const units = [
    ["T", KI ** 4],
    ["G", KI ** 3],
    ["M", KI ** 2],
    ["K", KI],
  ];
  for (const [u, size] of units) {
    if (v % size === 0) return `${v / size}${u}`;
  }
  return String(v);
}

export function pct(part, total) {
  if (!total || total <= 0) return 0;
  return Math.max(0, Math.min(100, (Number(part) / Number(total)) * 100));
}

export function fmtPct(n, digits = 0) {
  if (n == null || isNaN(n)) return "—";
  return `${Number(n).toFixed(digits)}%`;
}

/** Group digits with thin separators, e.g. 262144 -> "262,144". */
export function fmtInt(n) {
  if (n == null || isNaN(n)) return "—";
  return Number(n).toLocaleString("en-US");
}

export function fmtDurationMs(ms) {
  if (ms == null || isNaN(ms)) return "—";
  const s = Number(ms) / 1000;
  if (s < 1) return `${Math.round(Number(ms))} ms`;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} s`;
  const m = Math.floor(s / 60);
  const rem = Math.round(s % 60);
  return `${m}m ${rem}s`;
}

export function fmtTimeMs(ms) {
  if (!ms || isNaN(ms)) return "—";
  try {
    const d = new Date(Number(ms));
    return d.toLocaleString();
  } catch (_) {
    return String(ms);
  }
}

/** Quota in bytes/day -> "10.0 GiB/day". */
export function fmtQuota(bytes) {
  if (bytes == null || isNaN(bytes)) return "—";
  if (Number(bytes) === 0) return "无限制";
  return `${fmtBytes(bytes)}/日`;
}

/**
 * Kernel-derived memcg app UID (crystal a591549 made memory.app_uid read-only):
 * -1 means unknown/empty cgroup, -2 means the cgroup mixes several UIDs, and
 * any non-negative value is a real Linux UID. Special values render as text so
 * they are never mistaken for an actual UID.
 */
export function fmtUid(uid) {
  const n = Number(uid);
  if (n === -1) return "未知";
  if (n === -2) return "多 UID";
  return String(uid);
}

/** True for a real, resolvable Linux UID (non-negative). */
export function isRealUid(uid) {
  return Number(uid) >= 0;
}

export function yesNo(v) {
  return v ? "是" : "否";
}

export function onOff(v) {
  return Number(v) === 1 || v === true ? "开" : "关";
}
