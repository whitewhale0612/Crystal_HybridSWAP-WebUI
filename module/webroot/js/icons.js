/* icons.js — inline stroke icons (lucide-derived), no external assets. */

import { html } from "../vendor/preact.mjs";

const D = {
  gauge: html`<path d="M12 14l3-3" /><path d="M3.34 19a10 10 0 1 1 17.32 0" /><circle cx="12" cy="14" r="1.4" fill="currentColor" stroke="none" />`,
  chip: html`<rect x="6" y="6" width="12" height="12" rx="2" /><path d="M9 2v2M15 2v2M9 20v2M15 20v2M2 9h2M2 15h2M20 9h2M20 15h2" /><rect x="9.5" y="9.5" width="5" height="5" rx="1" />`,
  snow: html`<path d="M12 2v20M4.2 7l15.6 10M19.8 7L4.2 17" /><path d="M12 6l2 -2M12 6l-2 -2M12 18l2 2M12 18l-2 2M6.5 9l-2.7 -.3M6.5 9l-.3 -2.7M17.5 15l2.7 .3M17.5 15l.3 2.7M17.5 9l.3 -2.7M17.5 9l2.7 -.3M6.5 15l-.3 2.7M6.5 15l-2.7 .3" />`,
  waves: html`<path d="M2 8c2 -1.6 4 -1.6 6 0s4 1.6 6 0 4 -1.6 6 0" /><path d="M2 13c2 -1.6 4 -1.6 6 0s4 1.6 6 0 4 -1.6 6 0" /><path d="M2 18c2 -1.6 4 -1.6 6 0s4 1.6 6 0 4 -1.6 6 0" />`,
  bolt: html`<path d="M13 2L4.5 13.5H11l-1 8.5L19.5 10H13z" />`,
  shield: html`<path d="M12 2l8 3v6c0 5 -3.5 8.5 -8 10 -4.5 -1.5 -8 -5 -8 -10V5z" /><path d="M9 12l2 2 4 -4" />`,
  stethoscope: html`<path d="M4 3v5a4 4 0 0 0 8 0V3" /><path d="M8 15a5 5 0 0 0 10 0v-2" /><circle cx="19" cy="10" r="2.4" />`,
  refresh: html`<path d="M21 12a9 9 0 1 1 -2.64 -6.36" /><path d="M21 3v5h-5" />`,
  sun: html`<circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4 -1.4M17.7 6.3l1.4 -1.4" />`,
  moon: html`<path d="M20 14.5A8 8 0 0 1 9.5 4a7 7 0 1 0 10.5 10.5z" />`,
  contrast: html`<circle cx="12" cy="12" r="9" /><path d="M12 3v18" /><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor" stroke="none" />`,
  check: html`<path d="M20 6L9 17l-5 -5" />`,
  x: html`<path d="M18 6L6 18M6 6l12 12" />`,
  minus: html`<path d="M5 12h14" />`,
  dot: html`<circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />`,
  alert: html`<path d="M10.3 3.3L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7 -3L13.7 3.3a2 2 0 0 0 -3.4 0z" /><path d="M12 9v4M12 17h.01" />`,
  info: html`<circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" />`,
  arrow: html`<path d="M5 12h14M13 6l6 6 -6 6" />`,
  download: html`<path d="M12 3v12M7 10l5 5 5 -5" /><path d="M4 19h16" />`,
  power: html`<path d="M12 3v9" /><path d="M6.4 6.4a8 8 0 1 0 11.2 0" />`,
  clock: html`<circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" />`,
  drive: html`<rect x="3" y="4" width="18" height="10" rx="2" /><path d="M3 14l3 6h12l3 -6" /><path d="M7 9h.01M11 9h6" />`,
  database: html`<ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v6c0 1.7 3.6 3 8 3s8 -1.3 8 -3V5" /><path d="M4 11v6c0 1.7 3.6 3 8 3s8 -1.3 8 -3v-6" />`,
  layers: html`<path d="M12 2l9 5 -9 5 -9 -5z" /><path d="M3 12l9 5 9 -5" /><path d="M3 17l9 5 9 -5" />`,
  sliders: html`<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3" /><path d="M2 14h4M10 8h4M18 16h4" />`,
  trash: html`<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1 -13" />`,
  play: html`<path d="M7 4l13 8 -13 8z" />`,
  rotate: html`<path d="M3 12a9 9 0 1 0 2.64 -6.36" /><path d="M3 3v5h5" />`,
  lock: html`<rect x="4.5" y="11" width="15" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" />`,
  unlock: html`<rect x="4.5" y="11" width="15" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 7.5 -1.9" />`,
  chevron: html`<path d="M9 6l6 6 -6 6" />`,
  save: html`<path d="M5 3h11l3 3v15H5z" /><path d="M8 3v5h7M8 21v-7h8v7" />`,
  beaker: html`<path d="M9 3h6M10 3v6l-5 9a2 2 0 0 0 1.8 3h10.4a2 2 0 0 0 1.8 -3l-5 -9V3" /><path d="M7 15h10" />`,
  compress: html`<path d="M8 3v4H4M16 3v4h4M8 21v-4H4M16 21v-4h4" /><path d="M4 12h16" />`,
  snowflakeSmall: html`<path d="M12 4v16M6 7l12 10M18 7L6 17" />`,
  cpu: html`<rect x="6" y="6" width="12" height="12" rx="2" /><rect x="9.5" y="9.5" width="5" height="5" rx="1" /><path d="M9 2v2M15 2v2M9 20v2M15 20v2M2 9h2M2 15h2M20 9h2M20 15h2" />`,
  hourglass: html`<path d="M6 3h12M6 21h12M8 3c0 4 8 5 8 9s-8 5 -8 9M16 3c0 4 -8 5 -8 9s8 5 8 9" />`,
};

export function Icon({ name, size = 20, cls = "", stroke = 2 }) {
  return html`<svg
    class=${cls}
    width=${size}
    height=${size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width=${stroke}
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    ${D[name] || D.dot}
  </svg>`;
}
