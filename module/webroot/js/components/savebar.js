/* components/savebar.js — shared profile editor action bar + apply result. */

import { html } from "../../vendor/preact.mjs";
import { Card, Button, Chip, StepList, Banner, Expand } from "./ui.js";
import {
  useStore,
  saveProfile,
  validateDraftRemote,
  applyProfile,
  resetDraft,
  draftDirty,
} from "../store.js";
import { fmtTimeMs } from "../format.js";

const APPLY_MODE = { "full-control": "full-control" };

export function SaveBar() {
  const { draft, draftErrors, saving, applying, gateAllowed } = useStore((s) => ({
    draft: s.draft,
    draftErrors: s.draftErrors,
    saving: s.saving,
    applying: s.applying,
    gateAllowed: s.gateAllowed,
  }));
  if (!draft) return null;

  const errCount = Object.keys(draftErrors || {}).length;
  const hasErr = errCount > 0;
  const dirty = draftDirty();
  const locked = gateAllowed === false;
  const applyMode = APPLY_MODE[draft.mode]; // undefined for diagnostic
  const applyLabel = "应用（完全接管）";

  return html`<${Card} cls="card--accent">
    <div class="row row--between row--wrap" style="gap:10px">
      <div class="row row--wrap" style="gap:8px">
        ${dirty
          ? html`<${Chip} kind="warn" dot=${true}>未保存的改动<//>`
          : html`<${Chip} kind="ok" dot=${true}>已同步<//>`}
        ${hasErr && html`<${Chip} kind="danger">${errCount} 项错误<//>`}
        ${locked && html`<${Chip} kind="degraded">写操作已锁定<//>`}
      </div>
      <div class="row row--wrap" style="gap:8px">
        <${Button} size="sm" variant="ghost" icon="rotate" disabled=${!dirty} onClick=${resetDraft}>还原<//>
        <${Button} size="sm" variant="ghost" icon="check" disabled=${locked} onClick=${validateDraftRemote}>验证<//>
        <${Button}
          size="sm"
          icon="beaker"
          disabled=${locked || hasErr}
          loading=${applying === "dry-run"}
          onClick=${() => applyProfile("dry-run")}
        >试运行<//>
        <${Button}
          size="sm"
          variant="primary"
          icon="save"
          disabled=${locked || hasErr || !dirty}
          loading=${saving}
          onClick=${saveProfile}
        >保存<//>
        ${applyMode &&
        html`<${Button}
          size="sm"
          variant="primary"
          icon="play"
          disabled=${locked || hasErr}
          loading=${applying === applyMode}
          onClick=${() => applyProfile(applyMode)}
        >${applyLabel}<//>`}
      </div>
    </div>
    ${draft.mode === "diagnostic" &&
    html`<div style="margin-top:12px">
      ${Banner({ kind: "info", title: "诊断模式", children: "当前为只读诊断模式，保存只会更新配置；切换到完全接管后可应用。" })}
    </div>`}
  <//>`;
}

export function ApplyResult() {
  const last = useStore((s) => s.lastApply);
  if (!last) return null;
  const d = last.data || {};
  return html`<${Card}
    title=${last.mode === "dry-run" ? "试运行结果" : "上次应用结果"}
    icon="beaker"
    note=${fmtTimeMs(last.ts)}
    actions=${d.degraded
      ? html`<${Chip} kind="warn" dot=${true}>降级<//>`
      : d.changed
      ? html`<${Chip} kind="ok" dot=${true}>有变更<//>`
      : html`<${Chip} kind="muted">无变化<//>`}
  >
    ${d.degraded_reasons && d.degraded_reasons.length
      ? html`<div class="stack stack--sm" style="margin-bottom:12px">
          ${d.degraded_reasons.map((r) => Banner({ kind: "degraded", title: "降级原因", children: r }))}
        </div>`
      : null}
    ${StepList({ steps: d.steps })}
    ${d.boot &&
    html`<div style="margin-top:12px">
      ${Expand({
        summary: "开机阶段状态",
        children: html`<pre class="tech">${JSON.stringify(d.boot, null, 2)}</pre>`,
      })}
    </div>`}
  <//>`;
}
