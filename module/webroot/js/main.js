/* main.js — app shell, routing, gate dispatch, bootstrap. */

import { html, render } from "../vendor/preact.mjs";
import { useEffect } from "../vendor/preact.mjs";
import {
  useStore,
  loadAll,
  startPolling,
  stopPolling,
  initTheme,
  cycleTheme,
  isMock,
} from "./store.js";

const AUTHOR = "阿菌•未霜";
import { ROUTES, useRoute, navigate } from "./router.js";
import { Icon } from "./icons.js";
import { Button, Card, Chip } from "./components/ui.js";
import { Toasts } from "./components/toasts.js";
import { GateView } from "./components/gate.js";
import { Overview } from "./pages/overview.js";
import { ZramSetup } from "./pages/zram.js";
import { CrystalPolicy } from "./pages/crystal.js";
import { PressurePolicy } from "./pages/pressure.js";
import { AgingPolicy } from "./pages/aging.js";
import { Maintenance } from "./pages/maintenance.js";
import { AppsPage } from "./pages/apps.js";

const PAGES = {
  overview: Overview,
  zram: ZramSetup,
  crystal: CrystalPolicy,
  pressure: PressurePolicy,
  aging: AgingPolicy,
  maintenance: Maintenance,
  apps: AppsPage,
};

const THEME_ICON = { auto: "contrast", dark: "moon", light: "sun" };
const THEME_LABEL = { auto: "跟随系统", dark: "深色", light: "浅色" };

function Header() {
  const { theme, mock, backend } = useStore((s) => ({
    theme: s.theme,
    mock: isMock(),
    backend: s.caps && s.caps.backend_version,
  }));

  return html`<header class="header">
    <div class="header__mark" title="Crystal Hybridswap">
      ${Icon({ name: "snow", size: 20 })}
    </div>
    <div class="header__title">
      <b>Crystal Hybridswap</b>
    </div>
    <div class="spacer"></div>
    ${mock && Chip({ kind: "info", children: "MOCK" })}
    <button
      class="icon-btn"
      title=${`主题：${THEME_LABEL[theme]}`}
      onClick=${() => cycleTheme()}
    >
      ${Icon({ name: THEME_ICON[theme] || "contrast", size: 18 })}
    </button>
  </header>`;
}

function Footer() {
  const backend = useStore((s) => s.caps && s.caps.backend_version);
  return html`<footer class="app__footer">
    <span>Crystal Hybridswap WebUI</span>
    <span class="app__footer-sep">·</span>
    <b>${AUTHOR}</b>
    ${backend ? html`<span class="app__footer-sep">·</span><span class="mono">${backend}</span>` : ""}
  </footer>`;
}

function TabBar({ route }) {
  return html`<nav class="tabbar">
    ${ROUTES.map(
      (r) => html`<button
        class="tab"
        aria-current=${route === r.id ? "page" : undefined}
        onClick=${() => navigate(r.id)}
      >
        ${Icon({ name: r.icon, size: 20 })}<span>${r.label}</span>
      </button>`
    )}
  </nav>`;
}

function LoadError({ error }) {
  const details = error && error.details ? JSON.stringify(error.details, null, 2) : null;
  return html`<div class="app__main" style="padding-top:24px">
    ${Card({
      accent: true,
      title: "无法连接后端",
      icon: "alert",
      children: html`<div class="stack stack--sm">
        <p class="dim" style="font-size:var(--fs-sm)">
          ${error && (error.code ? `${error.code}: ${error.message}` : error.message)}
        </p>
        ${Button({ variant: "primary", icon: "refresh", onClick: loadAll, children: "重试" })}
        ${details && html`<pre class="tech">${details}</pre>`}
      </div>`,
    })}
  </div>`;
}

function App() {
  const { ready, loadError, gateAllowed, gateReason, warnings } = useStore((s) => ({
    ready: s.ready,
    loadError: s.loadError,
    gateAllowed: s.gateAllowed,
    gateReason: s.gateReason,
    warnings: s.warnings,
  }));
  const route = useRoute();

  useEffect(() => {
    loadAll();
    startPolling(5000);
    return () => stopPolling();
  }, []);

  let body;
  if (!ready) {
    body = html`<div class="app__main" style="min-height:60dvh;padding-top:28px">
      <div style="display:grid;place-items:center;gap:16px">
        <div class="spark" style="width:46px;height:46px;border-radius:12px;background:var(--grad-accent)"></div>
        <div class="dim" style="font-size:var(--fs-sm)">正在连接后端…</div>
      </div>
    </div>`;
  } else if (loadError) {
    body = LoadError({ error: loadError });
  } else if (gateAllowed === false) {
    body = html`<main class="app__main">
      <${GateView} reason=${gateReason} warnings=${warnings} />
    </main>`;
  } else {
    const Page = PAGES[route] || Overview;
    body = html`<main class="app__main">
      <${Page} key=${route} />
    </main>`;
  }

  const showTabs = ready && !loadError && gateAllowed !== false;

  return html`<div class="app">
    <${Header} /> ${body} <${Footer} /> ${showTabs && TabBar({ route })} <${Toasts} />
  </div>`;
}

/* ---- bootstrap ---- */
initTheme();
render(html`<${App} />`, document.getElementById("root"));
