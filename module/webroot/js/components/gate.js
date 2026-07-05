/* components/gate.js — unavailable management-surface view. */

import { html } from "../../vendor/preact.mjs";
import { useState } from "../../vendor/preact.mjs";
import { Icon } from "../icons.js";
import { Card, Banner, Button } from "./ui.js";
import { api } from "../api.js";
import { toast, toastError } from "../store.js";

export function GateView({ reason, warnings }) {
  const [busy, setBusy] = useState(false);

  async function exportDiag() {
    setBusy(true);
    try {
      const r = await api.exportDiagnostics();
      toast("ok", "诊断已导出", r.data && r.data.path, 8000);
    } catch (err) {
      toastError(err, "导出诊断失败");
    } finally {
      setBusy(false);
    }
  }

  return html`<div class="gate fade-in">
    <div class="gate__inner">
      <div class="gate__badge">${Icon({ name: "lock", size: 40 })}</div>
      <h2>管理节点不可用</h2>
      <p class="dim">
        当前内核没有暴露完整的 Crystal Hybridswap 管理节点，
        调优与应用功能已停用以确保安全。
      </p>

      ${reason &&
      html`<div style="width:100%">
        ${Banner({
          kind: "degraded",
          title: reason.code || "E_KERNEL_NODE_MISSING",
          children: reason.message,
        })}
      </div>`}

      ${warnings && warnings.length
        ? html`<div class="stack stack--sm" style="width:100%">
            ${warnings.map((w) => Banner({ kind: "warn", title: w.code, children: w.message }))}
          </div>`
        : null}

      ${Card({
        cls: "card--pad-sm",
        children: html`<div class="stack stack--sm">
          <p class="dim" style="font-size:var(--fs-sm)">
            你仍可导出诊断包，随日志一并提交以便排查。
          </p>
          ${Button({
            variant: "primary",
            icon: "download",
            loading: busy,
            onClick: exportDiag,
            block: true,
            children: "导出诊断包",
          })}
        </div>`,
      })}

      <p class="muted" style="font-size:var(--fs-xs)">
        刷入暴露当前 stage-3 zram/memcg ABI 的内核后，重新打开本界面即可解锁全部功能。
      </p>
    </div>
  </div>`;
}
