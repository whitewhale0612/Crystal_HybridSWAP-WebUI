/* pages/aging.js — conservative app-level force-swapout aging policy. */

import { html } from "../../vendor/preact.mjs";
import { useStore, setDraftPath } from "../store.js";
import { Card, NumberField, ToggleRow } from "../components/ui.js";
import { SaveBar, ApplyResult } from "../components/savebar.js";

const DEFAULTS = {
  enabled: false,
  idle_after_sec: 900,
  min_zram_mb: 64,
  pressure_available_below_mb: 4096,
  cooldown_sec: 1800,
  max_apps_per_cycle: 1,
};

export function AgingPolicy() {
  const { draft, errors } = useStore((s) => ({ draft: s.draft, errors: s.draftErrors }));
  if (!draft) return null;
  const ag = { ...DEFAULTS, ...(draft.aging || {}) };
  const err = (p) => (errors ? errors[p] : null);

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">应用老化</div>
      <div class="page-sub">仅在内存压力下回收长时间后台应用；前台数据源不可用时自动停止。</div>
    </div>

    <${SaveBar} />

    <${Card} title="自动回收开关" icon="clock">
      ${ToggleRow({
        label: "启用应用老化换出",
        desc: ag.enabled ? "后台应用的 ZRAM 页会回写到 ZMS/UFS" : "默认关闭，不会自动写入 memory.force_swapout",
        checked: ag.enabled,
        onChange: (v) => setDraftPath("aging.enabled", v),
      })}
    <//>

    <${Card} title="老化与压力门槛" icon="sliders">
      <div class="grid grid--2">
        ${NumberField({ label: "后台时间", value: ag.idle_after_sec, min: 60, max: 604800, suffix: "秒", error: err("aging.idle_after_sec"), onChange: (v) => setDraftPath("aging.idle_after_sec", v) })}
        ${NumberField({ label: "最小 ZRAM", value: ag.min_zram_mb, min: 1, max: 4096, suffix: "MB", error: err("aging.min_zram_mb"), onChange: (v) => setDraftPath("aging.min_zram_mb", v) })}
        ${NumberField({ label: "触发 MemAvailable", value: ag.pressure_available_below_mb, min: 1, max: 65536, suffix: "MB 以下", error: err("aging.pressure_available_below_mb"), onChange: (v) => setDraftPath("aging.pressure_available_below_mb", v) })}
        ${NumberField({ label: "同应用冷却", value: ag.cooldown_sec, min: 60, max: 604800, suffix: "秒", error: err("aging.cooldown_sec"), onChange: (v) => setDraftPath("aging.cooldown_sec", v) })}
        ${NumberField({ label: "每轮最多应用数", value: ag.max_apps_per_cycle, min: 1, max: 8, suffix: "个", error: err("aging.max_apps_per_cycle"), onChange: (v) => setDraftPath("aging.max_apps_per_cycle", v) })}
      </div>
    <//>

    <${Card} title="安全边界" icon="shield">
      <p class="muted">自动策略始终排除 top-app 中的应用 UID、无效/混合 UID、系统关键包，以及由系统 zygote 直接创建的低 oom_score_adj 重要进程。同 UID 派生子进程的陈旧低分不会阻止回收。</p>
    <//>

    <${ApplyResult} />
  </div>`;
}
