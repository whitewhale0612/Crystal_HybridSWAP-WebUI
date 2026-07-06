/* pages/apps.js — per-app memory/swap breakdown (memcg total_info_per_app).
 *
 * Read-only view fed by GET /v1/apps. Unlike the editor pages it keeps its own
 * local state and polls its own endpoint (进入即加载 + 页面级 5s 自动刷新) so the
 * potentially long per-app list never bloats the core /v1/status snapshot. */

import { html, useState, useEffect } from "../../vendor/preact.mjs";
import { useStore, toastError } from "../store.js";
import { api } from "../api.js";
import { Banner, Button, Chip, KV, Skeleton, TextField, Segmented } from "../components/ui.js";
import { Icon } from "../icons.js";
import { fmtBytes, fmtInt, fmtUid, isRealUid } from "../format.js";

const PAGE_BYTES = 4096;

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

// 受控卡片：自己管理展开态，配合 CSS grid-template-rows 过渡实现平滑
// 展开/收起（原生 <details> 无高度动画）。只展示真实有意义的字段，内核
// 占位/兼容列（anon/policy/out_trig/in_mb）一律不显示。
function AppCard({ app }) {
  const [open, setOpen] = useState(false);
  const [forceOpen, setForceOpen] = useState(false);
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
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState("zram");
  const [pending, setPending] = useState(0);

  async function load(manual) {
    if (gateAllowed === false) return;
    setLoading(true);
    try {
      const r = await api.getApps();
      const d = r.data || {};
      setApps(Array.isArray(d.apps) ? d.apps : []);
      setSupported(d.supported !== false);
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
      if (document.hidden) return;
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
            ${list.map((app) => html`<${AppCard} app=${app} key=${`${app.name}-${app.uid}`} />`)}
          </div>
        </div>`
      : html`<p class="muted" style="font-size:var(--fs-sm)">没有匹配「${query}」的应用。</p>`;
  }

  return html`<div class="stack fade-in">
    <div>
      <div class="page-title">应用明细</div>
      <div class="page-sub">每个应用（memcg）的 ZRAM / 回写占用与最近一次换入换出统计，只读。</div>
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
