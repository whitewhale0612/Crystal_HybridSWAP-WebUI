/* pages/maintenance.js — 「维护」页：合并原「安全」与「诊断」。
 *
 * 分两区：运行模式（接管模式 / 强接管 / 上次开机，含 SaveBar 可写）与排查
 * 工具（后端告警 / 运行环境 / 诊断导出 / 原始数据，纯只读）。 */

import { html, useState } from "../../vendor/preact.mjs";
import { useStore, setDraftPath, refreshBoot, toast, toastError } from "../store.js";
import { api } from "../api.js";
import { Card, ToggleRow, Banner, KV, Button, Chip, Expand, IOSummary } from "../components/ui.js";
import { SaveBar, ApplyResult } from "../components/savebar.js";
import { BootChip } from "../components/badges.js";
import { fmtDurationMs, fmtTimeMs, fmtBytes, fmtInt } from "../format.js";

export function Maintenance() {
  const { draft, boot, status, caps, profile, warnings } = useStore((s) => ({
    draft: s.draft,
    boot: s.boot,
    status: s.status,
    caps: s.caps,
    profile: s.profile,
    warnings: s.warnings,
  }));
  const [busy, setBusy] = useState(false);
  const [dpath, setDpath] = useState(null);

  if (!draft) return null;
  const diagnostic = draft.mode === "diagnostic";
  const z = (status && status.zram) || {};
  const c = (status && status.crystal) || {};
  const zms = (status && status.zms) || {};
  const k = (caps && caps.kernel) || {};
  const st = (status && status.storage) || {};
  const bd = c.bd_stat_io || {};
  const zmsPhysical = zms.physical_io || {};
  const zmsPhysicalFailed = (zmsPhysical.write_failed_pages || 0) + (zmsPhysical.read_failed_pages || 0);
  const bdMeta = bd.present
    ? html`<span>当前已回写 ${fmtBytes(bd.current_bytes)}</span><br />
        <span>仅统计实际换出/读回，不含存储整理</span>`
    : "暂无回写路径统计";
  const zmsPhysicalMeta = zmsPhysical.present
    ? html`<span>写 I/O ${fmtInt(zmsPhysical.write_ios)} 次 · 读 ${fmtInt(zmsPhysical.read_ios)} 次 ·
        ${zmsPhysicalFailed > 0
          ? html`<span style="color:var(--danger)">${fmtInt(zmsPhysicalFailed)} 页失败</span>`
          : "失败 0 页"}</span><br />
        <span>包含存储整理产生的额外读写</span>`
    : "暂无全量物理 I/O 统计";

  async function exportDiag() {
    setBusy(true);
    try {
      const r = await api.exportDiagnostics();
      setDpath(r.data && r.data.path);
      toast("ok", "诊断已导出", r.data && r.data.path, 8000);
    } catch (err) {
      toastError(err, "导出诊断失败");
    } finally {
      setBusy(false);
    }
  }

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">维护</div>
      <div class="page-sub">接管模式与开机结果，以及诊断导出、后端告警与运行环境。</div>
    </div>

    <${SaveBar} />

    <div class="page-section">运行模式</div>

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

    <div class="page-section">排查工具</div>

    <${Card} title="导出诊断包" icon="download">
      <${Button} variant="primary" icon="download" loading=${busy} onClick=${exportDiag}>导出诊断包<//>
      ${dpath && html`<div style="margin-top:10px">${Banner({ kind: "ok", title: "导出路径", children: html`<span class="mono">${dpath}</span>` })}</div>`}
      <p class="muted" style="font-size:var(--fs-xs);margin-top:8px">导出后请自行确认内容，再提交给排查人员。</p>
    <//>

    <${Card} title="后端告警" icon="info"
      actions=${Chip({ kind: warnings && warnings.length ? "warn" : "ok", children: `${(warnings || []).length} 条` })}>
      ${warnings && warnings.length
        ? html`<div class="stack stack--sm">
            ${warnings.map((w) => Banner({ kind: "warn", title: w.code, children: w.message }))}
          </div>`
        : html`<p class="muted" style="font-size:var(--fs-sm)">当前无告警。</p>`}
    <//>

    <${Card} title="运行环境" icon="stethoscope">
      <div>
        ${caps && KV({ k: "后端版本", children: caps.backend_version })}
        ${caps && KV({ k: "API 版本", children: caps.api_version })}
        ${KV({ k: "Crystal 存在", children: k.crystal_present ? "是" : "否" })}
        ${KV({ k: "zram-control", children: k.zram_control_present ? "是" : "否" })}
        ${caps && KV({ k: "管理面就绪", children: caps.frontend_gate && caps.frontend_gate.allowed ? "是" : "否" })}
        ${st.profile_backend && KV({ k: "配置后端", children: st.profile_backend })}
        ${st.profile_path && KV({ k: "配置路径", mono: true, children: st.profile_path })}
        ${st.data_dir && KV({ k: "数据目录", mono: true, children: st.data_dir })}
      </div>
    <//>

    <${Card} title="闪存 I/O" icon="database"
      note="回写路径 / 全量物理">
      <div class="stack">
        ${IOSummary({
          writeLabel: "数据路径写入",
          readLabel: "数据路径读回",
          writeBytes: bd.write_bytes,
          writeCount: bd.write_blocks,
          readBytes: bd.read_bytes,
          readCount: bd.read_blocks,
          countUnit: "4K 块",
          meta: bdMeta,
        })}
        <div class="divider"></div>
        ${IOSummary({
          writeLabel: "全量物理写入",
          readLabel: "全量物理读回",
          writeBytes: zmsPhysical.write_bytes,
          writeCount: zmsPhysical.write_pages,
          readBytes: zmsPhysical.read_bytes,
          readCount: zmsPhysical.read_pages,
          countUnit: "页",
          meta: zmsPhysicalMeta,
        })}
        ${KV({ k: "配额计费口径", children: "ZMS 全量物理写字节" })}
      </div>
    <//>

    <${Card} title="原始数据" icon="beaker">
      <div class="stack stack--sm">
        ${Expand({ summary: "capabilities", children: html`<pre class="tech">${JSON.stringify(caps, null, 2)}</pre>` })}
        ${Expand({ summary: "status", children: html`<pre class="tech">${JSON.stringify(status, null, 2)}</pre>` })}
        ${Expand({ summary: "profile", children: html`<pre class="tech">${JSON.stringify(profile, null, 2)}</pre>` })}
      </div>
    <//>

    <${ApplyResult} />
  </div>`;
}
