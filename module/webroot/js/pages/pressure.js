/* pages/pressure.js — memory pressure policy (root memcg knobs), three-state. */

import { html } from "../../vendor/preact.mjs";
import { useStore, setDraftPath } from "../store.js";
import { Card, NumberField, OptNumberField, TriToggleRow, ToggleRow } from "../components/ui.js";
import { SaveBar, ApplyResult } from "../components/savebar.js";

export function PressurePolicy() {
  const { draft, errors, status } = useStore((s) => ({ draft: s.draft, errors: s.draftErrors, status: s.status }));
  if (!draft) return null;
  const po = draft.policy || {};
  const sys = (status && status.policy) || {};
  const sysAb = sys.avail_buffers || {};
  const ab = po.avail_buffers; // null = 跟随系统
  const abOn = ab != null;
  const err = (p) => (errors ? errors[p] : null);

  const toggleAb = (on) =>
    setDraftPath(
      "policy.avail_buffers",
      on
        ? {
            avail_mb: sysAb.avail_mb ?? 0,
            min_mb: sysAb.min_mb ?? 0,
            high_mb: sysAb.high_mb ?? 0,
            free_swap_threshold_mb: sysAb.free_swap_threshold_mb ?? 0,
          }
        : null
    );

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">压力策略</div>
      <div class="page-sub">留空/关闭即"跟随系统"，模块不接管；设置后由守护进程持续维护。</div>
    </div>

    <${SaveBar} />

    <${Card} title="ZRAM 水位" icon="waves">
      ${OptNumberField({ label: "水位比 (zram_wm_ratio)", value: po.zram_wm_ratio, sysValue: sys.zram_wm_ratio, min: 0, max: 100, suffix: "%", error: err("policy.zram_wm_ratio"), onChange: (v) => setDraftPath("policy.zram_wm_ratio", v) })}
    <//>

    <${Card} title="可用内存缓冲 (avail_buffers)" icon="sliders" note="四个 MB 阈值">
      <div class="stack">
        ${ToggleRow({
          label: "接管可用内存缓冲",
          desc: abOn
            ? "由模块维护以下四个阈值"
            : `跟随系统（当前 ${sysAb.avail_mb ?? "—"} / ${sysAb.min_mb ?? "—"} / ${sysAb.high_mb ?? "—"} / ${sysAb.free_swap_threshold_mb ?? "—"} MB）`,
          checked: abOn,
          onChange: toggleAb,
        })}
        ${abOn &&
        html`<div class="grid grid--2">
          ${NumberField({ label: "avail_mb", value: ab.avail_mb, min: 0, max: 4294967295, suffix: "MB", error: err("policy.avail_buffers.avail_mb"), onChange: (v) => setDraftPath("policy.avail_buffers.avail_mb", v) })}
          ${NumberField({ label: "min_mb", value: ab.min_mb, min: 0, max: 4294967295, suffix: "MB", error: err("policy.avail_buffers.min_mb"), onChange: (v) => setDraftPath("policy.avail_buffers.min_mb", v) })}
          ${NumberField({ label: "high_mb", value: ab.high_mb, min: 0, max: 4294967295, suffix: "MB", error: err("policy.avail_buffers.high_mb"), onChange: (v) => setDraftPath("policy.avail_buffers.high_mb", v) })}
          ${NumberField({ label: "free_swap_threshold_mb", value: ab.free_swap_threshold_mb, min: 0, max: 4294967295, suffix: "MB", error: err("policy.avail_buffers.free_swap_threshold_mb"), onChange: (v) => setDraftPath("policy.avail_buffers.free_swap_threshold_mb", v) })}
        </div>`}
      </div>
    <//>

    <${Card} title="ERM 覆盖策略" icon="sliders">
      ${TriToggleRow({
        label: "允许 ERM 覆盖策略",
        desc: "erm_avail_buffer_enable（min/high 由内核管理，模块不设置）",
        value: po.erm_avail_buffer_enable,
        sysValue: sys.erm_avail_buffer_enable,
        onChange: (v) => setDraftPath("policy.erm_avail_buffer_enable", v),
      })}
    <//>

    <${ApplyResult} />
  </div>`;
}
