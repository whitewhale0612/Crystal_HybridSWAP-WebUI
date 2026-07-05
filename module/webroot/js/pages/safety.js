/* pages/safety.js — takeover mode + takeover behavior + last boot result. */

import { html } from "../../vendor/preact.mjs";
import { useStore, setDraftPath, refreshBoot } from "../store.js";
import { Card, ToggleRow, Banner, KV, Button, Chip, Expand } from "../components/ui.js";
import { SaveBar, ApplyResult } from "../components/savebar.js";
import { BootChip } from "../components/badges.js";
import { fmtDurationMs, fmtTimeMs } from "../format.js";

export function SafetyPage() {
  const { draft, boot, status } = useStore((s) => ({
    draft: s.draft,
    boot: s.boot,
    status: s.status,
  }));
  if (!draft) return null;
  const diagnostic = draft.mode === "diagnostic";
  const z = (status && status.zram) || {};

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">安全</div>
      <div class="page-sub">模块完全接管 ZRAM 控制；需要只读观察时可切换诊断模式。</div>
    </div>

    <${SaveBar} />

    <${Card} title="接管模式" icon="shield"
      actions=${Chip({ kind: diagnostic ? "muted" : "ok", dot: true, children: diagnostic ? "诊断（只读）" : "完全接管" })}>
      <div class="stack">
        ${ToggleRow({
          label: "诊断模式（只读，不接管）",
          desc: "开启后模块仅展示状态，不写入任何内核参数，也不由守护进程维护。",
          checked: diagnostic,
          onChange: (on) => setDraftPath("mode", on ? "diagnostic" : "full-control"),
        })}
        ${!diagnostic &&
        Banner({ kind: "info", title: "完全接管中", children: "守护进程持续监控并抵御第三方对 ZRAM/策略的修改，被改动会自动还原。" })}
      </div>
    <//>

    <${Card} title="强接管与重置" icon="alert">
      <div class="stack">
        ${diagnostic
          ? Banner({ kind: "info", title: "诊断模式", children: "当前不会写入内核参数，也不会由守护进程接管 ZRAM。" })
          : Banner({ kind: "warn", title: "完全接管", children: "当 ZRAM 未初始化或容量与配置不一致时，守护进程会 swapoff 后重建并重新启用 swap。" })}
        ${KV({ k: "当前 ZRAM", children: z.initstate === 1 ? "已初始化" : "未初始化" })}
        ${KV({ k: "swap 激活", children: z.active_swap ? "是" : "否" })}
      </div>
    <//>

    <${Card} title="上次开机结果" icon="clock"
      note=${boot ? boot.phase : ""}
      actions=${boot ? BootChip({ result: boot.result }) : null}>
      ${boot
        ? html`<div>
            ${KV({ k: "阶段", children: boot.phase || "—" })}
            ${KV({ k: "结果", children: boot.result || "—" })}
            ${boot.duration_ms != null && KV({ k: "耗时", children: fmtDurationMs(boot.duration_ms) })}
            ${boot.finished_at_ms ? KV({ k: "完成时间", children: fmtTimeMs(boot.finished_at_ms) }) : null}
            ${boot.last_error_code && KV({ k: "错误码", children: boot.last_error_code })}
            ${boot.degraded_reasons && boot.degraded_reasons.length
              ? html`<div class="stack stack--sm" style="margin-top:10px">
                  ${boot.degraded_reasons.map((r) => Banner({ kind: "degraded", title: "降级原因", children: r }))}
                </div>`
              : null}
            ${boot.log_tail && boot.log_tail.length
              ? html`<div style="margin-top:10px">
                  ${Expand({ summary: "日志末尾", children: html`<pre class="tech">${boot.log_tail.join("\n")}</pre>` })}
                </div>`
              : null}
            ${boot.log_path && html`<p class="path-line" style="margin-top:8px">${boot.log_path}</p>`}
          </div>`
        : html`<p class="muted" style="font-size:var(--fs-sm)">暂无开机记录。</p>`}
      <div class="row" style="margin-top:10px">
        <${Button} size="sm" variant="ghost" icon="refresh" onClick=${refreshBoot}>刷新开机状态<//>
      </div>
    <//>

    <${ApplyResult} />
  </div>`;
}
