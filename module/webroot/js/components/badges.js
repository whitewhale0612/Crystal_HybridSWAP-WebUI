/* components/badges.js — semantic status chips shared across pages. */

import { html } from "../../vendor/preact.mjs";
import { Chip } from "./ui.js";

const MODE = {
  "full-control": { kind: "ok", label: "完全接管" },
  diagnostic: { kind: "muted", label: "诊断" },
};

const BOOT = {
  active: { kind: "ok", label: "已激活" },
  degraded: { kind: "warn", label: "降级" },
  failed: { kind: "danger", label: "失败" },
  skipped: { kind: "muted", label: "跳过" },
  unknown: { kind: "muted", label: "未知" },
  none: { kind: "muted", label: "无" },
};

export function ModeChip({ mode }) {
  const m = MODE[mode] || { kind: "muted", label: mode || "—" };
  return Chip({ kind: m.kind, dot: true, children: m.label });
}

export function BootChip({ result }) {
  const b = BOOT[result] || { kind: "muted", label: result || "—" };
  return Chip({ kind: b.kind, dot: true, children: b.label });
}

export function OnOffChip({ on, label }) {
  return Chip({
    kind: on ? "ok" : "muted",
    dot: true,
    children: `${label} · ${on ? "开" : "关"}`,
  });
}
