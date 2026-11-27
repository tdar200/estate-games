// Draws the installed-app icons (web/shared/app-icons/) from one SVG: two fanned deed cards on the
// felt. Run after changing the design: node scripts/app-icons.mjs
// Needs Playwright and Chrome or Edge (set PLAYWRIGHT=/path/to/node_modules/playwright if needed).
import { createRequire } from "node:module";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "web", "shared", "app-icons");
const require = createRequire(import.meta.url);
const candidates = [process.env.PLAYWRIGHT, "playwright"].filter(Boolean);
let playwright = null;
for (const name of candidates) {
  try {
    playwright = require(name);
    break;
  } catch {
    // try the next one
  }
}
if (!playwright) throw new Error("Playwright not found: set PLAYWRIGHT=/path/to/node_modules/playwright");

// A deed card: ivory, a coloured title band, a house and two lines of small print.
function card({ x, y, angle, band, house = true }) {
  const w = 196;
  const h = 256;
  return `<g transform="rotate(${angle} ${x} ${y})">
    <rect x="${x - w / 2}" y="${y - h / 2}" width="${w}" height="${h}" rx="18" fill="#FBFAF4" filter="url(#shadow)"/>
    <path d="M${x - w / 2} ${y - h / 2 + 18}a18 18 0 0 1 18-18h${w - 36}a18 18 0 0 1 18 18v46h-${w}z" fill="${band}"/>
    <rect x="${x - w / 2 + 14}" y="${y - h / 2 + 76}" width="${w - 28}" height="${h - 90}" rx="8" fill="none" stroke="#E3DFD2" stroke-width="3"/>
    ${house ? `<path d="M${x} ${y - 18}l-46 38h12v40h68v-40h12z" fill="#124733"/>
    <rect x="${x - 10}" y="${y + 34}" width="20" height="26" rx="3" fill="#FBFAF4"/>` : ""}
    <rect x="${x - 56}" y="${y + 76}" width="112" height="9" rx="4.5" fill="#D9D5C7"/>
    <rect x="${x - 40}" y="${y + 94}" width="80" height="9" rx="4.5" fill="#D9D5C7"/>
  </g>`;
}

// full: true fills the square (maskable and Apple icons, which the system rounds itself);
// false draws a rounded square with clear corners (the "any" icon).
function iconSvg(size, { full }) {
  const inset = full ? 0 : 20;
  const radius = full ? 0 : 108;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 512 512">
  <defs>
    <radialGradient id="felt" cx="40%" cy="32%" r="80%">
      <stop offset="0" stop-color="#1F6B4E"/><stop offset=".5" stop-color="#124733"/><stop offset="1" stop-color="#0A2A1F"/>
    </radialGradient>
    <filter id="shadow" x="-30%" y="-30%" width="160%" height="160%">
      <feDropShadow dx="0" dy="10" stdDeviation="12" flood-color="#000" flood-opacity=".42"/>
    </filter>
  </defs>
  <rect x="${inset}" y="${inset}" width="${512 - inset * 2}" height="${512 - inset * 2}" rx="${radius}" fill="url(#felt)"/>
  ${card({ x: 296, y: 246, angle: 12, band: "#1166B4", house: false })}
  ${card({ x: 226, y: 272, angle: -9, band: "#F2C14E" })}
</svg>`;
}

const icons = [
  { file: "icon-192.png", size: 192, full: false },
  { file: "icon-512.png", size: 512, full: false },
  { file: "icon-maskable-512.png", size: 512, full: true },
  { file: "apple-touch-icon.png", size: 180, full: true },
];

mkdirSync(out, { recursive: true });
writeFileSync(join(out, "icon.svg"), iconSvg(512, { full: false }));
const browser = await playwright.chromium.launch({ channel: process.env.BROWSER || "chrome", headless: true });
const page = await browser.newPage();
for (const icon of icons) {
  await page.setViewportSize({ width: icon.size, height: icon.size });
  await page.setContent(`<!doctype html><html><body style="margin:0;background:transparent">${iconSvg(icon.size, icon)}</body></html>`);
  await page.screenshot({ path: join(out, icon.file), omitBackground: !icon.full, clip: { x: 0, y: 0, width: icon.size, height: icon.size } });
  console.log(icon.file);
}
await browser.close();
