// Browser checks for the game screens, in a real (headless) browser:
//
//  1. TV follow camera (review 4 Nov 2026, #1): the 0.10.0 table never zoomed during a hop, and no
//     unit test noticed. Opens the t-hop-mid demo, samples the #board transform on every animation
//     frame, and fails unless the camera is zoomed in (scale > 1.05) while the token hops, the
//     mini-map shows while zoomed, it rests on the landing square and the board is back to the whole
//     board afterwards, at 1920×1080, 1280×720 and 1440×900. CAMERA = OFF / CLOSE-UPS ONLY and
//     reduced motion never zoom during a hop (and reduced motion says why).
//  2. Text selection: on the main screens (TV lobby and game, poker table, phone game, phone poker,
//     phone join) the body's computed user-select is none, and text inputs stay selectable.
//
// Needs Playwright and Edge or Chrome; nothing else (it serves web/ itself, the demos need no API).
//   node --test web/tests/screens.browser.mjs
//   PLAYWRIGHT=/path/to/node_modules/playwright BROWSER=chrome node --test web/tests/screens.browser.mjs
// Not part of the *.check.mjs suite because it launches a browser (about 40 s).

import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import { after, before, test } from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, "..");
const require = createRequire(import.meta.url);

function loadPlaywright() {
  const candidates = [process.env.PLAYWRIGHT, "playwright"].filter(Boolean);
  for (const name of candidates) {
    try {
      return require(name);
    } catch {
      // try the next one
    }
  }
  return null;
}

const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".json": "application/json; charset=utf-8", ".svg": "image/svg+xml", ".woff2": "font/woff2" };

function serve() {
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/ws")) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end('{"error":"no API in the browser checks"}');
      return;
    }
    let file = decodeURIComponent(url.pathname);
    if (file === "/") file = "/table/index.html";
    else if (file === "/play" || /^\/j\/[A-Za-z0-9]{5}$/.test(file)) file = "/phone/index.html";
    else if (file.endsWith("/")) file += "index.html";
    const full = normalize(join(web, file));
    if (!full.startsWith(normalize(web))) {
      res.writeHead(403);
      res.end();
      return;
    }
    try {
      const body = await readFile(full);
      res.writeHead(200, { "Content-Type": TYPES[extname(full)] || "application/octet-stream", "Cache-Control": "no-store" });
      res.end(body);
    } catch {
      res.writeHead(404);
      res.end("not found");
    }
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

const playwright = loadPlaywright();
const skip = playwright ? false : "Playwright not found (set PLAYWRIGHT=/path/to/node_modules/playwright)";
let server = null;
let browser = null;
let base = "";

before(async () => {
  if (skip) return;
  server = await serve();
  base = `http://127.0.0.1:${server.address().port}`;
  browser = await playwright.chromium.launch({ channel: process.env.BROWSER || "msedge", headless: true });
});

after(async () => {
  await browser?.close();
  server?.close();
});

// Run one table demo and return the frame samples: { t, z, hop, minimap } and the toast text.
async function sample({ w = 1920, h = 1080, demo = "t-hop-mid", reduced = "no-preference", settings = null, ms = 5200 } = {}) {
  const context = await browser.newContext({ viewport: { width: w, height: h }, reducedMotion: reduced });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.addInitScript((saved) => {
    try {
      if (saved) localStorage.setItem("estate.table.settings", saved);
      else localStorage.removeItem("estate.table.settings");
    } catch {
      // no storage: defaults
    }
    window.__cam = [];
    const t0 = performance.now();
    const tick = () => {
      const board = document.querySelector("#board");
      const map = document.querySelector("#minimap");
      if (board) {
        const matrix = (getComputedStyle(board).transform || "").match(/matrix\(([^,]+)/);
        window.__cam.push({ t: Math.round(performance.now() - t0), z: matrix ? Number(matrix[1]) : 1, hop: Boolean(document.querySelector(".is-hopping")), minimap: Boolean(map && !map.hidden) });
      }
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }, settings ? JSON.stringify(settings) : null);
  await page.goto(`${base}/?demo=${demo}`, { waitUntil: "load" });
  await page.waitForTimeout(ms);
  const frames = await page.evaluate(() => window.__cam);
  const toast = await page.evaluate(() => document.querySelector("#toast")?.textContent || "");
  await context.close();
  assert.deepEqual(errors, [], "no page errors");
  return { frames, toast };
}

test("TV camera: follows the hop at every TV and desktop size, then shows the whole board", { skip, timeout: 120_000 }, async () => {
  for (const [w, h] of [[1920, 1080], [1280, 720], [1440, 900]]) {
    const { frames } = await sample({ w, h });
    const hop = frames.filter((frame) => frame.hop);
    assert.ok(hop.length >= 10, `${w}×${h}: the token hopped (${hop.length} frames)`);
    const zoomed = hop.filter((frame) => frame.z > 1.05);
    assert.ok(zoomed.length / hop.length >= 0.9, `${w}×${h}: zoomed in for ${zoomed.length} of ${hop.length} hop frames`);
    assert.ok(Math.max(...hop.map((frame) => frame.z)) >= 1.55, `${w}×${h}: at the default 1.6× zoom`);
    assert.ok(zoomed.every((frame) => frame.minimap), `${w}×${h}: the mini-map shows while zoomed`);
    const last = frames[frames.length - 1];
    assert.ok(Math.abs(last.z - 1) < 0.01, `${w}×${h}: back to the whole board (scale ${last.z})`);
    // It rests on the landing square (about half a second) before easing out.
    const hopEnd = hop[hop.length - 1].t;
    const zoomEnd = frames.filter((frame) => frame.z > 1.05).at(-1).t;
    assert.ok(zoomEnd - hopEnd >= 350, `${w}×${h}: rested ${zoomEnd - hopEnd} ms on the landing square`);
  }
  // CAMERA = OFF and CLOSE-UPS ONLY: the hop plays on the whole board.
  for (const camera of ["off", "closeups"]) {
    const { frames } = await sample({ settings: { camera, zoom: 2 } });
    assert.ok(frames.some((frame) => frame.hop), `${camera}: the token hopped`);
    assert.ok(frames.every((frame) => frame.z < 1.05), `${camera}: never zoomed`);
  }
  // Reduced motion from the system: no camera, and the TV says why.
  const reduced = await sample({ reduced: "reduce" });
  assert.ok(reduced.frames.every((frame) => frame.z < 1.05), "reduced motion: never zoomed");
  assert.match(reduced.toast, /camera off.*reduced motion/i, "reduced motion: the TV says the camera is off and why");
});

test("game screens: no text selection on the main screens; text inputs stay selectable", { skip, timeout: 120_000 }, async () => {
  const screens = [
    ["TV lobby", "/?demo=t-lobby-fresh", 1920, 1080, null],
    ["TV game", "/?demo=t-roll-keyboard", 1920, 1080, null],
    ["TV poker", "/?demo=poker-flop-table", 1920, 1080, null],
    ["phone game", "/play?demo=scene&s=your-turn", 390, 844, null],
    ["phone poker", "/play?demo=poker-flop-phone", 390, 844, null],
    ["tablet game", "/play?demo=scene&s=your-turn", 1180, 820, null],
    ["phone join", "/play?demo=scene&s=join-typed-code", 390, 844, "#p-code, #p-name"],
  ];
  for (const [label, path, w, h, inputs] of screens) {
    const context = await browser.newContext({ viewport: { width: w, height: h }, isMobile: w < 900, hasTouch: w < 1300 });
    const page = await context.newPage();
    await page.goto(`${base}${path}`, { waitUntil: "load" });
    await page.waitForTimeout(900);
    const styles = await page.evaluate((selector) => {
      const css = (node) => getComputedStyle(node);
      const body = css(document.body);
      const sample = [...document.querySelectorAll("button, h1, h2, h3, p, span, b")].find((node) => node.textContent.trim() && node.getBoundingClientRect().width > 0);
      return {
        body: body.userSelect || body.webkitUserSelect,
        text: sample ? css(sample).userSelect || css(sample).webkitUserSelect : null,
        callout: body.webkitTouchCallout ?? null,
        buttons: [...document.querySelectorAll("button")].slice(0, 20).map((node) => css(node).touchAction),
        inputs: selector ? [...document.querySelectorAll(selector)].map((node) => css(node).userSelect || css(node).webkitUserSelect) : [],
      };
    }, inputs);
    assert.equal(styles.body, "none", `${label}: body user-select`);
    assert.equal(styles.text, "none", `${label}: text inside is not selectable`);
    assert.ok(styles.buttons.length && styles.buttons.every((value) => value === "manipulation"), `${label}: buttons don't double-tap zoom (${[...new Set(styles.buttons)]})`);
    if (inputs) {
      assert.ok(styles.inputs.length >= 1, `${label}: has text inputs`);
      assert.ok(styles.inputs.every((value) => value === "text"), `${label}: inputs stay selectable (${styles.inputs})`);
    }
    // Drag across the screen with the mouse: nothing gets selected.
    if (!inputs) {
      await page.mouse.move(20, h / 2);
      await page.mouse.down();
      await page.mouse.move(w - 20, h / 2 + 40, { steps: 8 });
      await page.mouse.up();
      const picked = await page.evaluate(() => String(window.getSelection() || "").trim());
      assert.equal(picked, "", `${label}: a mouse drag selects nothing`);
    }
    await context.close();
  }
});
