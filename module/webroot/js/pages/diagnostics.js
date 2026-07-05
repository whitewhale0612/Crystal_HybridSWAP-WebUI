/* pages/diagnostics.js — diagnostics export, warnings, capability & raw views. */

import { html } from "../../vendor/preact.mjs";
import { useState } from "../../vendor/preact.mjs";
import { useStore, toast, toastError } from "../store.js";
import { api } from "../api.js";
import { Card, Button, Banner, KV, Chip, Expand } from "../components/ui.js";

export function Diagnostics() {
  const { caps, status, profile, warnings } = useStore((s) => ({
    caps: s.caps,
    status: s.status,
    profile: s.profile,
    warnings: s.warnings,
  }));
  const [busy, setBusy] = useState(false);
  const [path, setPath] = useState(null);

  async function exportDiag() {
    setBusy(true);
    try {
      const r = await api.exportDiagnostics();
      setPath(r.data && r.data.path);
      toast("ok", "诊断已导出", r.data && r.data.path, 8000);
    } catch (err) {
      toastError(err, "导出诊断失败");
    } finally {
      setBusy(false);
    }
  }

  const k = (caps && caps.kernel) || {};
  const st = (status && status.storage) || {};

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">诊断</div>
      <div class="page-sub">导出诊断快照、查看后端告警与运行环境信息。</div>
    </div>

    <${Card} title="导出诊断包" icon="download">
      <${Button} variant="primary" icon="download" loading=${busy} onClick=${exportDiag}>导出诊断包<//>
      ${path && html`<div style="margin-top:10px">${Banner({ kind: "ok", title: "导出路径", children: html`<span class="mono">${path}</span>` })}</div>`}
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

    <${Card} title="原始数据" icon="beaker">
      <div class="stack stack--sm">
        ${Expand({ summary: "capabilities", children: html`<pre class="tech">${JSON.stringify(caps, null, 2)}</pre>` })}
        ${Expand({ summary: "status", children: html`<pre class="tech">${JSON.stringify(status, null, 2)}</pre>` })}
        ${Expand({ summary: "profile", children: html`<pre class="tech">${JSON.stringify(profile, null, 2)}</pre>` })}
      </div>
    <//>
  </div>`;
}
