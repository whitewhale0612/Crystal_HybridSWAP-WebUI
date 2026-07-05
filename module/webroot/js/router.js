/* router.js — minimal hash router with a fixed tab set. */

import { useState, useEffect } from "../vendor/preact.mjs";

export const ROUTES = [
  { id: "overview", label: "概览", icon: "gauge" },
  { id: "zram", label: "ZRAM", icon: "chip" },
  { id: "crystal", label: "策略", icon: "sliders" },
  { id: "pressure", label: "压力", icon: "waves" },
  { id: "safety", label: "安全", icon: "shield" },
  { id: "diagnostics", label: "诊断", icon: "stethoscope" },
];

const IDS = ROUTES.map((r) => r.id);

export function currentRoute() {
  const id = (location.hash || "").replace(/^#\/?/, "");
  return IDS.includes(id) ? id : "overview";
}

export function navigate(id) {
  if (!IDS.includes(id) || location.hash.replace(/^#\/?/, "") === id) return;
  const next = `#/${id}`;
  if (typeof history !== "undefined" && typeof history.replaceState === "function") {
    history.replaceState(null, "", next);
    const event =
      typeof HashChangeEvent === "function" ? new HashChangeEvent("hashchange") : new Event("hashchange");
    window.dispatchEvent(event);
    return;
  }
  location.replace(next);
}

export function useRoute() {
  const [route, setRoute] = useState(currentRoute());
  useEffect(() => {
    const on = () => setRoute(currentRoute());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}
