/* components/toasts.js — global toast stack bound to the store. */

import { html } from "../../vendor/preact.mjs";
import { Icon } from "../icons.js";
import { useStore, dismissToast } from "../store.js";

const ICON = { ok: "check", danger: "alert", info: "info" };

export function Toasts() {
  const toasts = useStore((s) => s.toasts);
  if (!toasts.length) return null;
  return html`<div class="toasts">
    ${toasts.map(
      (t) => html`<div class=${`toast toast--${t.kind}`} onClick=${() => dismissToast(t.id)}>
        <span class="toast__icon">${Icon({ name: ICON[t.kind] || "info", size: 18 })}</span>
        <div style="min-width:0;flex:1">
          <b>${t.title}</b>${t.message && html`<p>${t.message}</p>`}
        </div>
      </div>`
    )}
  </div>`;
}
