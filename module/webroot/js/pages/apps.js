/* pages/apps.js — per-app memory/swap breakdown (memcg total_info_per_app).
 *
 * Fed by GET /v1/apps; keeps its own local state and polls its own endpoint
 * (进入即加载 + 页面级 5s 自动刷新) so the potentially long per-app list never
 * bloats the core /v1/status snapshot. When the kernel exposes the per-memcg
 * trigger nodes (force_swap_supported), each expanded card offers hold-to-run
 * 强制换出 / 强制换入 actions via POST /v1/apps/force-swap. */

import { html, useState, useEffect, useRef } from "../../vendor/preact.mjs";
import { useStore, toastError } from "../store.js";
import { api } from "../api.js";
import { Banner, Button, Chip, KV, Skeleton, TextField, Segmented, cx } from "../components/ui.js";
import { Icon } from "../icons.js";
import { fmtBytes, fmtInt, fmtUid, isRealUid } from "../format.js";

const PAGE_BYTES = 4096;

// 长按确认阈值：填充动画时长即触发时长，短按/滑动都不会触发。
const HOLD_MS = 850;

const SORTS = [
  { value: "zram", label: "ZRAM" },
  { value: "eswap", label: "回写" },
  { value: "score", label: "分数" },
  { value: "name", label: "名称" },
];

function sortApps(list, key) {
  const arr = list.slice();
  if (key === "name") arr.sort((a, b) => String(a.label || a.name).localeCompare(String(b.label || b.name)));
  else if (key === "score") arr.sort((a, b) => (b.score || 0) - (a.score || 0));
  else if (key === "eswap") arr.sort((a, b) => (b.eswap_compr_bytes || 0) - (a.eswap_compr_bytes || 0));
  else arr.sort((a, b) => (b.zram_compr_bytes || 0) - (a.zram_compr_bytes || 0));
  return arr;
}

function errMsg(err) {
  return err && err.code ? `${err.code}: ${err.message}` : (err && err.message) || String(err);
}

// pages -> "1,024 页 ≈ 4.0 MiB"; keeps the 4KiB page count authoritative while
// surfacing a human-readable size next to it.
function pagesText(pages) {
  const n = Number(pages) || 0;
  if (n === 0) return "0 页";
  return `${fmtInt(n)} 页 ≈ ${fmtBytes(n * PAGE_BYTES)}`;
}

// force_swapin 审计组：仅当某个应用真的发生过强制换入时才有非零值，
// 日常恒为 0。这组始终显示但默认折叠，有记录时标题旁点亮小圆点提示。
function hasForceSwapin(app) {
  return [app.swapin_request, app.swapin_req_pages, app.swapin_eff_pages, app.swapin_moved_pages].some(
    (v) => (Number(v) || 0) !== 0
  );
}

// 长按确认按钮：主指针按下（或按住 Enter/空格）启动填充动画 + 定时器，
// 按满 HOLD_MS 才触发 onFire；提前抬手/移出/失焦/被系统打断都会取消并回退。
// 填充过程本身就是"确认中"的可视反馈，无需再弹二次确认框。
// onHoldChange 向页面上报长按起止，用于暂停轮询防止列表中途重排。
function HoldButton({ icon, label, busy, disabled, onFire, onHoldChange }) {
  const [holding, setHolding] = useState(false);
  const timer = useRef(null);
  // 镜像最新 props：长按期间另一操作开始时（busy/disabled 翻转）武装中的
  // 定时器必须立即失效，闭包里的旧值不可信。
  const blocked = !!(busy || disabled);
  const blockedRef = useRef(blocked);
  blockedRef.current = blocked;

  function cancel() {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
      onHoldChange && onHoldChange(false);
    }
    setHolding(false);
  }

  function begin() {
    if (blockedRef.current || timer.current) return;
    setHolding(true);
    onHoldChange && onHoldChange(true);
    timer.current = setTimeout(() => {
      timer.current = null;
      setHolding(false);
      onHoldChange && onHoldChange(false);
      if (blockedRef.current) return;
      try {
        if (navigator.vibrate) navigator.vibrate(25);
      } catch (_) {}
      onFire();
    }, HOLD_MS);
  }

  function onPointerDown(e) {
    // 仅主指针的主键：排除右键（配合 contextmenu 抑制会静默触发）与多指。
    if (e.button !== 0 || e.isPrimary === false) return;
    // 防止长按触发文本选择/系统菜单；滚动抢占由 CSS touch-action 处理。
    e.preventDefault();
    // 显式捕获让鼠标在按钮外抬起也能派发 pointerup 回来（触摸默认隐式捕获）。
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch (_) {}
    begin();
  }

  function onKeyDown(e) {
    if (e.repeat || (e.key !== "Enter" && e.key !== " ")) return;
    e.preventDefault(); // 拦掉默认 click，键盘走同样的"按住确认"
    begin();
  }

  useEffect(() => {
    if (blocked) cancel();
    // eslint-disable-next-line
  }, [blocked]);
  // 卸载时清掉挂起的定时器，避免对已卸载组件 setState / 幽灵触发。
  useEffect(() => () => cancel(), []);

  const inner = html`${busy ? html`<span class="btn__spin"></span>` : Icon({ name: icon, size: 15 })}${label}`;
  return html`<button
    type="button"
    class=${cx("btn", "btn--sm", "hold-btn", holding && "is-holding")}
    style=${`--hold:${HOLD_MS}ms`}
    disabled=${disabled}
    aria-label=${`${label}（长按执行，中途松手取消）`}
    onPointerDown=${onPointerDown}
    onPointerUp=${cancel}
    onPointerLeave=${cancel}
    onPointerCancel=${cancel}
    onKeyDown=${onKeyDown}
    onKeyUp=${cancel}
    onBlur=${cancel}
    onContextMenu=${(e) => e.preventDefault()}
  >
    <span class="hold-btn__label">${inner}</span>
    <span class="hold-btn__fill" aria-hidden="true">${inner}</span>
  </button>`;
}

// 后端 result 分类 → 展示文案与色调。ret 为内核原始返回（>0 页数，<0 -errno）。
function forceResultLine(action) {
  if (!action) return null;
  const opTxt = action.op === "swapout" ? "换出" : "换入";
  if (action.phase === "running") return { tone: "info", text: `正在${opTxt}…` };
  if (action.phase === "error") {
    const err = action.err || {};
    if (err.code === "E_APPLY_LOCKED")
      return { tone: "warn", text: "守护进程正在校准配置，请稍后重试。" };
    if (err.code === "E_APP_NOT_FOUND")
      return { tone: "warn", text: "未找到该应用的 memcg，应用可能已退出。" };
    return { tone: "danger", text: errMsg(err) };
  }
  const d = action.res || {};
  switch (d.result) {
    case "ok":
      return { tone: "ok", text: `已${opTxt} ${pagesText(d.pages)}` };
    case "pending":
      return { tone: "info", text: "已提交，内核在后台继续执行，统计稍后自动刷新。" };
    case "nodata":
      return {
        tone: "muted",
        text: action.op === "swapout" ? "没有可换出的 ZRAM 页。" : "没有已回写的页可换入。",
      };
    case "busy":
      return {
        tone: "warn",
        text:
          action.op === "swapout"
            ? "内核正忙，本次换出未执行（设备可能正在休眠或有任务排队），请稍后再试。"
            : "内核正忙（已有换入任务在队列中），请稍后再试。",
      };
    case "quota":
      return { tone: "warn", text: "今日回写配额已用尽，换出暂不可用。" };
    case "nodev":
      return { tone: "danger", text: "内核未就绪：没有可用的 hybridswap 设备。" };
    default:
      return { tone: "danger", text: `内核返回错误（${d.ret}）。` };
  }
}

// 受控卡片：自己管理展开态，配合 CSS grid-template-rows 过渡实现平滑
// 展开/收起（原生 <details> 无高度动画）。只展示真实有意义的字段，内核
// 占位/兼容列（anon/policy/out_trig/in_mb）一律不显示。
function AppCard({ app, forceSwap, onChanged, onHoldChange }) {
  const [open, setOpen] = useState(false);
  const [forceOpen, setForceOpen] = useState(false);
  // action: null | {op, phase:"running"} | {op, phase:"done", res} | {op, phase:"error", err}
  const [action, setAction] = useState(null);
  // 重入护栏：state 更新是异步的，双指同时按满两个按钮时两次 onFire 都会
  // 看到旧的 running=null，必须用 ref 同步拦下第二发。
  const inflight = useRef(false);

  async function fire(op) {
    if (inflight.current) return;
    inflight.current = true;
    setAction({ op, phase: "running" });
    try {
      const r = await api.forceSwapApp({ op, name: app.name, uid: app.uid });
      setAction({ op, phase: "done", res: r.data || {} });
      onChanged && onChanged();
    } catch (err) {
      setAction({ op, phase: "error", err });
    } finally {
      inflight.current = false;
    }
  }

  const running = action && action.phase === "running" ? action.op : null;
  const line = forceResultLine(action);
  const compr = fmtBytes(app.zram_compr_bytes);
  const orig = fmtBytes(app.zram_orig_bytes);
  const hasEswap = (app.eswap_compr_bytes || 0) > 0 || (app.eswap_orig_bytes || 0) > 0;
  const title = app.label || app.name || "未知";
  const showPkg = app.label && app.name; // 有真名时把包名放到副行
  const forced = hasForceSwapin(app);
  return html`<section class=${`card app-item${open ? " is-open" : ""}`} key=${`${app.name}-${app.uid}`}>
    <button
      class="app-item__summary"
      type="button"
      aria-expanded=${open ? "true" : "false"}
      onClick=${() => setOpen((v) => !v)}
    >
      <div class="app-item__main">
        <div class="app-item__title">
          <span class="app-item__name">${title}</span>
          ${Chip({ kind: "muted", children: isRealUid(app.uid) ? `uid ${app.uid}` : fmtUid(app.uid) })}
        </div>
        ${showPkg && html`<div class="app-item__pkg">${app.name}</div>`}
        <div class="app-item__metrics">
          <span>ZRAM <b class="n">${compr}</b> › <b class="n">${orig}</b></span>
          ${hasEswap && html`<span>回写 <b class="n">${fmtBytes(app.eswap_compr_bytes)}</b></span>`}
          <span>分数 <b class="n">${app.score}</b></span>
        </div>
      </div>
      <span class="app-item__chev">${Icon({ name: "chevron", size: 18 })}</span>
    </button>
    <div class="app-item__wrap">
      <div class="app-item__detail">
        ${KV({ k: "包名", mono: true, children: app.name || "未知" })}
        ${KV({ k: "UID", children: fmtUid(app.uid) })}
        ${KV({ k: "优先级分数", children: app.score })}
        ${KV({ k: "ZRAM 压缩后", children: fmtBytes(app.zram_compr_bytes) })}
        ${KV({ k: "ZRAM 原始", children: fmtBytes(app.zram_orig_bytes) })}
        ${KV({ k: "ZMS 回写·压缩后", children: fmtBytes(app.eswap_compr_bytes) })}
        ${KV({ k: "ZMS 回写·原始", children: fmtBytes(app.eswap_orig_bytes) })}
        ${forceSwap &&
        html`<div class="app-actions">
          <div class="app-actions__head">
            <span class="app-actions__title">内存操作</span>
            <span class="app-actions__hint">长按执行，中途松手取消</span>
          </div>
          <div class="app-actions__row">
            <${HoldButton}
              icon="swapout"
              label="强制换出"
              busy=${running === "swapout"}
              disabled=${running !== null && running !== "swapout"}
              onFire=${() => fire("swapout")}
              onHoldChange=${onHoldChange}
            />
            <${HoldButton}
              icon="swapin"
              label="强制换入"
              busy=${running === "swapin"}
              disabled=${running !== null && running !== "swapin"}
              onFire=${() => fire("swapin")}
              onHoldChange=${onHoldChange}
            />
          </div>
          ${line &&
          html`<div
            class=${`app-actions__result app-actions__result--${line.tone} fade-in`}
            key=${`${action.op}-${action.phase}`}
          >
            ${line.text}
          </div>`}
          <div class="app-actions__desc">
            换出：将该应用的 ZRAM 压缩页全部回写到 ZMS 后备存储（受每日配额限制）；
            换入：将已回写页拉回 ZRAM。内核异步执行，结果回填到上方统计。
          </div>
        </div>`}
        <div class=${`app-sub${forceOpen ? " is-open" : ""}`}>
          <button
            class="app-sub__summary"
            type="button"
            aria-expanded=${forceOpen ? "true" : "false"}
            onClick=${() => setForceOpen((v) => !v)}
          >
            <span class="app-sub__title">
              强制换入记录${forced && html`<span class="app-sub__dot" title="有记录"></span>`}
            </span>
            <span class="app-sub__chev">${Icon({ name: "chevron", size: 15 })}</span>
          </button>
          <div class="app-sub__wrap">
            <div class="app-sub__detail">
              ${KV({ k: "换入请求", children: fmtInt(app.swapin_request) })}
              ${KV({ k: "换入请求页", children: pagesText(app.swapin_req_pages) })}
              ${KV({ k: "换入有效页", children: pagesText(app.swapin_eff_pages) })}
              ${KV({ k: "换入迁移页", children: pagesText(app.swapin_moved_pages) })}
              ${KV({ k: "换入返回码", children: app.swapin_ret })}
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>`;
}

export function AppsPage() {
  const gateAllowed = useStore((s) => s.gateAllowed);
  const [apps, setApps] = useState(null);
  const [supported, setSupported] = useState(true);
  const [forceSwap, setForceSwap] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState("zram");
  const [pending, setPending] = useState(0);
  // 长按进行中的按钮计数：>0 时跳过本轮自动刷新，避免列表重排把手指
  // 按住的按钮移走（既打断填充动画，又可能丢失取消事件）。
  const holdCount = useRef(0);
  const onHoldChange = (h) => {
    holdCount.current = Math.max(0, holdCount.current + (h ? 1 : -1));
  };

  async function load(manual) {
    if (gateAllowed === false) return;
    setLoading(true);
    try {
      const r = await api.getApps();
      const d = r.data || {};
      setApps(Array.isArray(d.apps) ? d.apps : []);
      setSupported(d.supported !== false);
      setForceSwap(d.force_swap_supported === true);
      setPending(d.labels_pending || 0);
      setError(null);
    } catch (err) {
      setError(err);
      if (manual) toastError(err, "读取应用明细失败");
    } finally {
      setLoading(false);
    }
  }

  // Load on mount + light page-local polling (paused while backgrounded).
  useEffect(() => {
    load(false);
    const t = setInterval(() => {
      if (document.hidden || holdCount.current > 0) return;
      load(false);
    }, 5000);
    return () => clearInterval(t);
    // eslint-disable-next-line
  }, [gateAllowed]);

  let body;
  if (apps == null) {
    body =
      error != null
        ? html`<div class="stack stack--sm">
            ${Banner({ kind: "danger", title: "读取失败", children: errMsg(error) })}
            ${Button({ variant: "primary", icon: "refresh", onClick: () => load(true), children: "重试" })}
          </div>`
        : html`<div class="stack stack--sm">${[0, 1, 2].map(() => Skeleton({ h: 64 }))}</div>`;
  } else if (!supported) {
    body = Banner({
      kind: "info",
      title: "当前内核未提供逐应用统计",
      children: "未找到 memory.total_info_per_app 节点，无法展示逐应用明细。",
    });
  } else if (apps.length === 0) {
    body = html`<p class="muted" style="font-size:var(--fs-sm)">暂无被跟踪的应用。</p>`;
  } else {
    const q = query.trim().toLowerCase();
    const list = sortApps(
      q ? apps.filter((a) => `${a.name} ${a.label || ""}`.toLowerCase().includes(q)) : apps,
      sortKey
    );
    body = list.length
      ? html`<div class="stack stack--sm">
          <div class="muted" style="font-size:var(--fs-xs)">
            ${query.trim() ? `匹配 ${list.length} / 共 ${apps.length} 个` : `共 ${apps.length} 个应用`}
            ${pending > 0 ? html`　·　<span style="color:var(--accent)">应用名解析中 ${pending}…</span>` : ""}
          </div>
          <div class="stack stack--sm fade-in" key=${sortKey}>
            ${list.map(
              (app) =>
                html`<${AppCard}
                  app=${app}
                  forceSwap=${forceSwap}
                  onChanged=${() => load(false)}
                  onHoldChange=${onHoldChange}
                  key=${`${app.name}-${app.uid}`}
                />`
            )}
          </div>
        </div>`
      : html`<p class="muted" style="font-size:var(--fs-sm)">没有匹配「${query}」的应用。</p>`;
  }

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">应用明细</div>
      <div class="page-sub">
        每个应用（memcg）的 ZRAM / 回写占用与换入换出统计；展开单个应用可强制换出 / 换入。
      </div>
    </div>

    <div class="stack stack--sm">
      ${TextField({ value: query, onChange: setQuery, placeholder: "搜索包名…" })}
      <div class="row row--between" style="gap:10px;align-items:center;flex-wrap:wrap">
        ${Segmented({ options: SORTS, value: sortKey, onChange: setSortKey })}
        ${Button({ variant: "ghost", size: "sm", icon: "refresh", loading, onClick: () => load(true), children: "刷新" })}
      </div>
    </div>

    ${body}
  </div>`;
}
