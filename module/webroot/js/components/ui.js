/* components/ui.js — shared presentational + form components. */

import { html, render, useState, useEffect, useLayoutEffect, useRef } from "../../vendor/preact.mjs";
import { Icon } from "../icons.js";
import { parseSize, isValidSize, fmtBytes, fmtSizeCompact } from "../format.js";

export function cx(...parts) {
  return parts.filter(Boolean).join(" ");
}

/* Minimal portal: renders children into a body-level host so fixed-position
 * layers (dropdown menus) escape card overflow/backdrop-filter clipping.
 * The vendored preact build has no createPortal, hence the manual render(). */
function Portal({ children }) {
  const host = useRef(null);
  if (!host.current) {
    host.current = document.createElement("div");
    document.body.appendChild(host.current);
  }
  useLayoutEffect(
    () => () => {
      render(null, host.current);
      host.current.remove();
    },
    []
  );
  useLayoutEffect(() => {
    render(children, host.current);
  });
  return null;
}

/* ---------------- layout / display ---------------- */

export function Card({ title, icon, note, accent, actions, children, cls }) {
  return html`<section class=${cx("card", accent && "card--accent", cls)}>
    ${(title || icon || actions) &&
    html`<div class="card__head">
      ${icon && html`<div class="card__icon">${Icon({ name: icon, size: 17 })}</div>`}
      <div style="min-width:0;flex:1">
        ${title && html`<div class="card__title">${title}</div>`}
        ${note && html`<div class="card__note">${note}</div>`}
      </div>
      ${actions}
    </div>`}
    ${children}
  </section>`;
}

export function Chip({ kind, dot, children }) {
  return html`<span class=${cx("chip", kind && `chip--${kind}`)}>
    ${dot && html`<span class="chip__dot"></span>`}${children}
  </span>`;
}

export function Stat({ label, value, unit, sub }) {
  return html`<div class="stat">
    <div class="stat__label">${label}</div>
    <div class="stat__value">${value}${unit && html`<span class="unit">${unit}</span>`}</div>
    ${sub && html`<div class="stat__sub">${sub}</div>`}
  </div>`;
}

export function Ring({ value = 0, size = 116, stroke = 10, label, sublabel, tone = "accent" }) {
  const v = Math.max(0, Math.min(100, Number(value) || 0));
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const offset = c * (1 - v / 100);
  const grad = tone === "warn" ? "ringWarn" : tone === "danger" ? "ringDanger" : "ringGrad";
  return html`<div class="ring" style=${`width:${size}px;height:${size}px`}>
    <svg width=${size} height=${size}>
      <circle
        class="ring__track"
        cx=${size / 2}
        cy=${size / 2}
        r=${r}
        stroke-width=${stroke}
      />
      <circle
        class="ring__val"
        cx=${size / 2}
        cy=${size / 2}
        r=${r}
        stroke-width=${stroke}
        stroke=${`url(#${grad})`}
        stroke-dasharray=${c}
        stroke-dashoffset=${offset}
      />
    </svg>
    <div class="ring__center">
      <div>
        <b>${label}</b><br />
        ${sublabel && html`<span>${sublabel}</span>`}
      </div>
    </div>
  </div>`;
}

export function Meter({ label, valueText, ratio, tone }) {
  const pctv = Math.max(0, Math.min(100, Number(ratio) || 0));
  const fillMod = tone === "danger" ? "bar__fill--danger" : tone === "warn" ? "bar__fill--warn" : "";
  return html`<div class="meter-row">
    <div class="meter-row__top"><span class="muted">${label}</span><b>${valueText}</b></div>
    <div class="bar"><div class=${cx("bar__fill", fillMod)} style=${`width:${pctv}%`}></div></div>
  </div>`;
}

const BANNER_ICON = { warn: "alert", danger: "alert", degraded: "alert", info: "info", ok: "check" };

export function Banner({ kind = "info", title, children }) {
  return html`<div class=${cx("banner", `banner--${kind}`)}>
    <span class="banner__icon">${Icon({ name: BANNER_ICON[kind] || "info", size: 18 })}</span>
    <div class="banner__body">
      ${title && html`<b>${title}</b>`}${children && html`<p>${children}</p>`}
    </div>
  </div>`;
}

export function KV({ k, children, mono }) {
  return html`<div class="kv">
    <span class="kv__k">${k}</span>
    <span class=${cx("kv__v", mono && "mono")}>${children}</span>
  </div>`;
}

export function Skeleton({ w = "100%", h = 16, cls }) {
  return html`<div
    class=${cx("sk", cls)}
    style=${`width:${typeof w === "number" ? w + "px" : w};height:${h}px`}
  ></div>`;
}

export function RebootFlag({ children }) {
  return html`<span class="reboot-flag">${Icon({ name: "rotate", size: 13 })}${children || "需重启/reset 生效"}</span>`;
}

export function Expand({ summary, children, open }) {
  return html`<details class="expand" open=${open}>
    <summary>${summary}</summary>
    <div style="margin-top:8px">${children}</div>
  </details>`;
}

/* ---------------- buttons ---------------- */

export function Button({ variant, size, block, loading, icon, onClick, disabled, children, title }) {
  return html`<button
    class=${cx("btn", variant && `btn--${variant}`, size && `btn--${size}`, block && "btn--block")}
    onClick=${onClick}
    disabled=${disabled || loading}
    title=${title}
  >
    ${loading
      ? html`<span class="btn__spin"></span>`
      : icon && Icon({ name: icon, size: 16 })}
    ${children}
  </button>`;
}

/* ---------------- form primitives ---------------- */

export function Field({ label, hint, error, reboot, children, htmlFor }) {
  return html`<div class="field">
    ${label &&
    html`<label class="field__label" for=${htmlFor}>
      ${label}${reboot && RebootFlag({})}
    </label>`}
    ${children}
    ${error
      ? html`<div class="field__error">${error}</div>`
      : hint && html`<div class="field__hint">${hint}</div>`}
  </div>`;
}

export function TextField(props) {
  const { value, onChange, placeholder, disabled, error, ...rest } = props;
  return Field({
    ...rest,
    error,
    children: html`<input
      class=${cx("input", error && "input--invalid")}
      type="text"
      value=${value ?? ""}
      placeholder=${placeholder}
      disabled=${disabled}
      onInput=${(e) => onChange(e.target.value)}
    />`,
  });
}

export function SizeField(props) {
  // free-text size string like "12G" / "512M" / "0"
  return TextField({ placeholder: "如 12G / 512M / 0", ...props });
}

export function NumberField(props) {
  const { value, onChange, min, max, step, disabled, error, suffix, ...rest } = props;
  const parse = (raw) => {
    if (raw === "") return NaN;
    const n = Number(raw);
    return Number.isSafeInteger(n) ? n : NaN;
  };
  const input = html`<input
    class=${cx("input", error && "input--invalid")}
    type="number"
    inputmode="numeric"
    value=${value ?? ""}
    min=${min}
    max=${max}
    step=${step || 1}
    disabled=${disabled}
    onInput=${(e) => onChange(parse(e.target.value))}
  />`;
  return Field({
    ...rest,
    error,
    children: suffix
      ? html`<div class="row" style="gap:8px">${input}<span class="muted" style="font-size:var(--fs-sm)">${suffix}</span></div>`
      : input,
  });
}

/* Themed dropdown replacing the native <select> popup (which ignores the
 * app theme on Android). Same props as before: { value, onChange, options }. */
export function SelectField(props) {
  const { value, onChange, options, disabled, error, ...rest } = props;
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState(null);
  const btnRef = useRef(null);
  const menuRef = useRef(null);

  const current = options.find((o) => o.value === value);

  // The menu is pinned to coordinates captured at open time; if the page
  // still scrolls (keyboard, rotation, programmatic) just close it.
  useEffect(() => {
    if (!open) return;
    const onScroll = (e) => {
      if (menuRef.current && e.target && menuRef.current.contains(e.target)) return;
      setOpen(false);
    };
    const onResize = () => setOpen(false);
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
    };
  }, [open]);

  function openMenu() {
    const r = btnRef.current.getBoundingClientRect();
    const vh = window.innerHeight;
    const below = vh - r.bottom - 16;
    const above = r.top - 16;
    const need = Math.min(options.length * 44 + 12, 320);
    const up = below < need && above > below;
    setPos({
      left: r.left,
      width: r.width,
      top: up ? null : r.bottom + 6,
      bottom: up ? vh - r.top + 6 : null,
      maxH: Math.max(120, Math.min(320, up ? above : below)),
      up,
    });
    setOpen(true);
  }

  function pick(v) {
    setOpen(false);
    if (v !== value) onChange(v);
  }

  const menu =
    open &&
    pos &&
    html`<${Portal}>
      <div class="select__scrim" onClick=${() => setOpen(false)}>
        <div
          ref=${menuRef}
          class=${cx("select__menu", pos.up && "select__menu--up")}
          role="listbox"
          style=${`left:${pos.left}px;width:${pos.width}px;max-height:${pos.maxH}px;` +
          (pos.top != null ? `top:${pos.top}px` : `bottom:${pos.bottom}px`)}
          onClick=${(e) => e.stopPropagation()}
        >
          ${options.map(
            (o) => html`<button
              type="button"
              role="option"
              aria-selected=${o.value === value ? "true" : "false"}
              class=${cx("select__opt", o.value === value && "is-active")}
              onClick=${() => pick(o.value)}
            >
              <span class="select__opt-label">${o.label}</span>
              ${o.value === value && Icon({ name: "check", size: 15 })}
            </button>`
          )}
        </div>
      </div>
    <//>`;

  return Field({
    ...rest,
    error,
    children: html`<button
        type="button"
        ref=${btnRef}
        class=${cx("input", "select-btn", open && "is-open", error && "input--invalid")}
        aria-haspopup="listbox"
        aria-expanded=${open ? "true" : "false"}
        disabled=${disabled}
        onClick=${() => (open ? setOpen(false) : openMenu())}
      >
        <span class="select-btn__label">${current ? current.label : value ?? "—"}</span>
        ${Icon({ name: "chevron", size: 16 })}
      </button>
      ${menu}`,
  });
}

export function Toggle({ checked, onChange, disabled }) {
  return html`<button
    type="button"
    class="toggle"
    role="switch"
    aria-checked=${checked ? "true" : "false"}
    aria-disabled=${disabled ? "true" : "false"}
    onClick=${() => !disabled && onChange(!checked)}
  ></button>`;
}

export function ToggleRow({ label, desc, checked, onChange, disabled }) {
  return html`<div class="toggle-row">
    <div class="toggle-row__text">
      <b>${label}</b>${desc && html`<p>${desc}</p>`}
    </div>
    ${Toggle({ checked, onChange, disabled })}
  </div>`;
}

export function Segmented({ options, value, onChange, disabled }) {
  return html`<div class="seg" role="group">
    ${options.map(
      (o) => html`<button
        type="button"
        class="seg__opt"
        aria-pressed=${o.value === value ? "true" : "false"}
        disabled=${disabled}
        onClick=${() => onChange(o.value)}
      >
        ${o.label}
      </button>`
    )}
  </div>`;
}

/* ---------------- three-state (unset = follow system) ---------------- */

// unset ⇔ null/undefined/"" ; the module does not override system defaults.
export const isUnset = (v) => v === null || v === undefined || v === "";

// Cycling tri-state badge: 系统 → 开启 → 关闭 → 系统.
export function TriToggle({ value, sysValue, onChange, disabled }) {
  // dot pop animation only after a real tap — not on page mount
  const touched = useRef(false);
  const state = isUnset(value) ? "unset" : Number(value) === 1 ? "on" : "off";
  const next = { unset: 1, on: 0, off: null };
  const sysTxt = isUnset(sysValue) ? "—" : Number(sysValue) === 1 ? "开" : "关";
  const label = state === "unset" ? `系统（${sysTxt}）` : state === "on" ? "开启" : "关闭";
  return html`<button
    type="button"
    class=${cx("tri", `tri--${state}`, touched.current && "tri--anim")}
    aria-disabled=${disabled ? "true" : "false"}
    onClick=${() => {
      if (disabled) return;
      touched.current = true;
      onChange(next[state]);
    }}
  ><span class="tri__dot"></span>${label}</button>`;
}

export function TriToggleRow({ label, desc, value, sysValue, onChange, disabled }) {
  return html`<div class="toggle-row">
    <div class="toggle-row__text">
      <b>${label}</b>${desc && html`<p>${desc}</p>`}
    </div>
    ${TriToggle({ value, sysValue, onChange, disabled })}
  </div>`;
}

// Optional number field: empty input = unset (emits null); placeholder shows system value.
export function OptNumberField(props) {
  const { value, onChange, sysValue, min, max, step, disabled, error, suffix, ...rest } = props;
  const unset = isUnset(value);
  const placeholder = isUnset(sysValue) ? "跟随系统" : `系统 ${sysValue}`;
  const parse = (raw) => {
    if (raw === "") return null;
    const n = Number(raw);
    return Number.isSafeInteger(n) ? n : NaN;
  };
  const input = html`<input
    class=${cx("input", "opt", unset && "is-unset", error && "input--invalid")}
    type="number"
    inputmode="numeric"
    value=${value ?? ""}
    placeholder=${placeholder}
    min=${min}
    max=${max}
    step=${step || 1}
    disabled=${disabled}
    onInput=${(e) => onChange(parse(e.target.value))}
  />`;
  return Field({
    ...rest,
    error,
    children: suffix
      ? html`<div class="row" style="gap:8px">${input}<span class="muted" style="font-size:var(--fs-sm)">${suffix}</span></div>`
      : input,
  });
}

/* Optional size field: free text with human units ("10G" / "1024M" / plain
 * bytes); empty = unset (follow system). Emits integer bytes into the draft,
 * or the raw string when unparseable so validation can flag it. */
export function OptSizeField(props) {
  const { value, onChange, sysValue, placeholder, hint, disabled, error, ...rest } = props;
  const unset = isUnset(value);
  const [text, setText] = useState(unset ? "" : fmtSizeCompact(value));
  const lastEmit = useRef(unset ? null : value);

  // Re-sync the text only when the draft changed elsewhere (还原/保存).
  useEffect(() => {
    const cur = isUnset(value) ? null : value;
    if (cur !== lastEmit.current) {
      lastEmit.current = cur;
      setText(cur === null ? "" : fmtSizeCompact(cur));
    }
  }, [value]);

  function onInput(raw) {
    setText(raw);
    const v = raw.trim() === "" ? null : isValidSize(raw) ? parseSize(raw) : raw;
    lastEmit.current = v;
    onChange(v);
  }

  const parsed = text.trim() === "" ? null : parseSize(text);
  const preview = parsed != null && !isNaN(parsed) ? `= ${fmtBytes(parsed)}` : null;
  const ph = placeholder ?? (isUnset(sysValue) ? "跟随系统" : `系统 ${fmtBytes(sysValue)}`);

  return Field({
    ...rest,
    error,
    hint: preview || hint,
    children: html`<input
      class=${cx("input", "opt", unset && "is-unset", error && "input--invalid")}
      type="text"
      autocomplete="off"
      autocapitalize="off"
      spellcheck="false"
      value=${text}
      placeholder=${ph}
      disabled=${disabled}
      onInput=${(e) => onInput(e.target.value)}
    />`,
  });
}

export function RangeField(props) {
  const { value, onChange, min = 0, max = 100, step = 1, disabled, valueText, error, ...rest } = props;
  return Field({
    ...rest,
    error,
    children: html`<div class=${cx("range", disabled && "range--off")}>
      <input
        type="range"
        class="range__input"
        min=${min}
        max=${max}
        step=${step}
        value=${value}
        disabled=${disabled}
        onInput=${(e) => onChange(Number(e.target.value))}
      />
      ${valueText != null && html`<div class="range__val mono">${valueText}</div>`}
    </div>`,
  });
}

/* ---------------- apply steps ---------------- */

const STEP_ICON = {
  changed: "check",
  unchanged: "minus",
  skipped: "arrow",
  failed: "x",
  denied: "lock",
};

export function StepList({ steps }) {
  if (!steps || !steps.length)
    return html`<p class="muted" style="font-size:var(--fs-sm)">无步骤记录。</p>`;
  return html`<div class="steps">
    ${steps.map((s) => {
      const status = s.status || "unchanged";
      const hasDiff = s.old_value !== undefined || s.new_value !== undefined;
      return html`<div class="step">
        <div class=${cx("step__mark", `step__mark--${status}`)}>
          ${Icon({ name: STEP_ICON[status] || "minus", size: 13 })}
        </div>
        <div class="step__body">
          <div class="row row--between" style="gap:8px">
            <span class="step__name">${s.name}</span>
            ${Chip({ kind: stepChipKind(status), children: STEP_LABEL[status] || status })}
          </div>
          ${s.target && html`<div class="step__meta mono">${s.target}</div>`}
          ${s.message && html`<div class="step__meta">${s.message}</div>`}
          ${s.error_code && html`<div class="step__meta" style="color:var(--danger)">${s.error_code}</div>`}
          ${hasDiff &&
          html`<div class="diff">
            <span class="diff__old">${fmtVal(s.old_value)}</span>
            ${Icon({ name: "arrow", size: 12 })}
            <span class="diff__new">${fmtVal(s.new_value)}</span>
          </div>`}
        </div>
      </div>`;
    })}
  </div>`;
}

const STEP_LABEL = {
  changed: "已更改",
  unchanged: "未变",
  skipped: "跳过",
  failed: "失败",
  denied: "被拒",
};

function stepChipKind(status) {
  return (
    { changed: "ok", unchanged: "muted", skipped: "info", failed: "danger", denied: "degraded" }[
      status
    ] || "muted"
  );
}

function fmtVal(v) {
  if (v === null) return "null";
  if (v === undefined) return "—";
  return String(v);
}
