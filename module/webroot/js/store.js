/*
 * store.js — tiny global reactive store + backend-backed mutations.
 *
 * A single external store drives the whole app. Components read slices via
 * useStore() and trigger loads/mutations through the exported helpers. Every
 * backend touch goes through js/api.js.
 */

import { useState, useEffect } from "../vendor/preact.mjs";
import { api, ApiError, isMock } from "./api.js";
import { defaultProfile, cloneProfile, validateProfile, setPath } from "./profile.js";

let state = {
  ready: false, // first load finished
  gateAllowed: null, // null=unknown, true/false after caps/status
  gateReason: null, // { code, message }
  caps: null,
  status: null,
  boot: null,
  profile: null, // saved profile (envelope data)
  loadError: null, // fatal load error (ApiError)
  warnings: [], // aggregated backend warnings from last loads
  toasts: [],
  theme: "auto", // auto | dark | light
  statusLoading: false,

  // profile editor (shared across ZRAM/Crystal/Pressure/Safety pages)
  draft: null, // editable copy of the profile
  draftErrors: {}, // dotted-path -> message
  saving: false,
  applying: null, // current apply mode string while in flight, else null
  lastApply: null, // { mode, data, ts }
};

const listeners = new Set();

function emit() {
  for (const l of listeners) l();
}

export function getState() {
  return state;
}

export function setState(patch) {
  state = { ...state, ...(typeof patch === "function" ? patch(state) : patch) };
  emit();
}

export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Subscribe a component to the store; re-renders on any change. */
export function useStore(selector) {
  const sel = selector || ((s) => s);
  const [, force] = useState(0);
  useEffect(() => subscribe(() => force((n) => n + 1)), []);
  return sel(state);
}

/* ---------------- toasts ---------------- */

let toastSeq = 0;

export function toast(kind, title, message, ttl = 4200) {
  toastSeq += 1;
  const id = toastSeq;
  setState((s) => ({ toasts: [...s.toasts, { id, kind, title, message }] }));
  if (ttl > 0) setTimeout(() => dismissToast(id), ttl);
  return id;
}

export function dismissToast(id) {
  setState((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
}

export function toastError(err, fallback = "操作失败") {
  const msg =
    err instanceof ApiError
      ? `${err.code}: ${err.message}`
      : (err && err.message) || String(err);
  toast("danger", fallback, msg, 6500);
}

/* ---------------- theme ---------------- */

export function initTheme() {
  let t = "auto";
  try {
    t = localStorage.getItem("chswap-theme") || "auto";
  } catch (_) {}
  applyTheme(t);
}

export function applyTheme(t) {
  const root = document.documentElement;
  if (t === "auto") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", t);
  try {
    localStorage.setItem("chswap-theme", t);
  } catch (_) {}
  setState({ theme: t });
}

export function cycleTheme() {
  const order = ["auto", "dark", "light"];
  const next = order[(order.indexOf(state.theme) + 1) % order.length];
  applyTheme(next);
  return next;
}

/* ---------------- loads ---------------- */

function collectWarnings(...envs) {
  const out = [];
  for (const env of envs) {
    if (env && Array.isArray(env.warnings)) out.push(...env.warnings);
  }
  // dedupe by code+message
  const seen = new Set();
  return out.filter((w) => {
    const k = `${w.code}|${w.message}`;
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

/** Initial load: capabilities + status + profile + boot in parallel. */
export async function loadAll() {
  setState({ statusLoading: true });
  try {
    const [caps, status, boot] = await Promise.all([
      api.getCapabilities(),
      api.getStatus(),
      api.getBootStatus(),
    ]);

    const gate =
      (status.data && status.data.frontend_gate) ||
      (caps.data && caps.data.frontend_gate) ||
      {};

    // Profile read is always allowed; fetch it but don't fail the whole load.
    let profile = null;
    try {
      const pr = await api.getProfile();
      profile = pr.data;
    } catch (_) {
      profile = null;
    }

    setState({
      ready: true,
      caps: caps.data,
      status: status.data,
      boot: boot.data,
      profile,
      gateAllowed: gate.allowed !== false,
      gateReason: gate.allowed === false ? { code: gate.reason_code, message: gate.message } : null,
      warnings: collectWarnings(caps, status, boot),
      loadError: null,
      statusLoading: false,
    });
    ensureDraft();
  } catch (err) {
    setState({ ready: true, loadError: err, statusLoading: false });
  }
}

/* ---------------- profile editor ---------------- */

// Seed the editable draft from the saved profile (or defaults) once.
export function ensureDraft() {
  if (state.draft) return;
  const base = state.profile ? cloneProfile(state.profile) : defaultProfile();
  setState({ draft: base, draftErrors: validateProfile(base) });
}

export function setDraftPath(path, value) {
  if (!state.draft) ensureDraft();
  const next = setPath(state.draft, path, value);
  setState({ draft: next, draftErrors: validateProfile(next) });
}

export function resetDraft() {
  const base = state.profile ? cloneProfile(state.profile) : defaultProfile();
  setState({ draft: base, draftErrors: validateProfile(base) });
  toast("info", "已重置", "草稿已还原为已保存的配置");
}

export function draftDirty() {
  if (!state.draft || !state.profile) return !!state.draft && !state.profile;
  return JSON.stringify(state.draft) !== JSON.stringify(state.profile);
}

function mutationAllowed() {
  if (state.gateAllowed === false) {
    toast("danger", "已被门控拦截", "当前管理节点不可用，写操作被禁用");
    return false;
  }
  return true;
}

export async function saveProfile() {
  if (!mutationAllowed()) return false;
  const errs = validateProfile(state.draft);
  if (Object.keys(errs).length) {
    setState({ draftErrors: errs });
    toast("danger", "无法保存", "请先修正表单中的错误项");
    return false;
  }
  setState({ saving: true });
  try {
    const r = await api.putProfile(state.draft);
    const saved = r.data;
    setState({ profile: saved, draft: cloneProfile(saved), draftErrors: validateProfile(saved), saving: false });
    toast("ok", "已保存", "配置已持久化");
    if (r.warnings && r.warnings.length) r.warnings.forEach((w) => toast("info", w.code, w.message, 6000));
    return true;
  } catch (err) {
    setState({ saving: false });
    toastError(err, "保存配置失败");
    return false;
  }
}

export async function validateDraftRemote() {
  if (!mutationAllowed()) return;
  // local pre-check: don't ship a draft with type errors (e.g. an unparsed
  // size string) to the backend just to get a raw decode error back
  const errs = validateProfile(state.draft);
  if (Object.keys(errs).length) {
    setState({ draftErrors: errs });
    toast("warn", "校验未通过", "请先修正表单中的错误项");
    return;
  }
  try {
    const r = await api.validateProfile(state.draft);
    if (r.data && r.data.valid) toast("ok", "校验通过", "后端确认配置有效");
    else toast("warn", "校验未通过", "后端认为配置无效，请检查");
  } catch (err) {
    toastError(err, "校验失败");
  }
}

// mode: 'dry-run' | 'full-control' | 'reconcile'
export async function applyProfile(mode) {
  if (!mutationAllowed()) return;
  if (mode !== "dry-run") {
    const errs = validateProfile(state.draft);
    if (Object.keys(errs).length) {
      setState({ draftErrors: errs });
      toast("danger", "无法应用", "请先修正表单中的错误项");
      return;
    }
  }
  setState({ applying: mode });
  try {
    const r = await api.apply({ mode, profile: state.draft });
    setState({ applying: null, lastApply: { mode, data: r.data, ts: Date.now() } });
    const d = r.data || {};
    const kind = d.degraded ? "warn" : "ok";
    const label =
      mode === "dry-run"
        ? "试运行完成"
        : d.changed
        ? "应用完成"
        : "应用完成（无变化）";
    toast(kind, label, d.degraded ? "存在降级项，见步骤详情" : `${(d.steps || []).length} 个步骤`);
    if (r.warnings && r.warnings.length) r.warnings.forEach((w) => toast("info", w.code, w.message, 6000));
    refreshStatus();
    refreshBoot();
  } catch (err) {
    setState({ applying: null });
    toastError(err, mode === "dry-run" ? "试运行失败" : "应用失败");
  }
}

/** Refresh only the live status snapshot (used by polling + manual refresh). */
export async function refreshStatus() {
  setState({ statusLoading: true });
  try {
    const status = await api.getStatus();
    const gate = (status.data && status.data.frontend_gate) || {};
    setState({
      status: status.data,
      gateAllowed: gate.allowed !== false,
      gateReason: gate.allowed === false ? { code: gate.reason_code, message: gate.message } : null,
      warnings: collectWarnings({ warnings: state.warnings }, status),
      statusLoading: false,
    });
  } catch (err) {
    setState({ statusLoading: false });
    toastError(err, "刷新状态失败");
  }
}

export async function refreshBoot() {
  try {
    const boot = await api.getBootStatus();
    setState({ boot: boot.data });
  } catch (err) {
    toastError(err, "读取开机状态失败");
  }
}

export async function reloadProfile() {
  try {
    const pr = await api.getProfile();
    setState({ profile: pr.data });
    return pr.data;
  } catch (err) {
    toastError(err, "读取配置失败");
    return null;
  }
}

/* ---------------- polling ---------------- */

let pollTimer = null;

export function startPolling(intervalMs = 5000) {
  stopPolling();
  pollTimer = setInterval(() => {
    if (document.hidden) return;
    if (state.gateAllowed === false) return; // nothing live to poll behind the gate
    refreshStatus();
  }, intervalMs);
}

export function stopPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

export { isMock };
