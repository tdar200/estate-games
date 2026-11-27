// Checks for the installable app (PWA): the two manifests and their icons, the service worker's
// offline files, the pages linking them, shared/pwa.js (install offer, registration) and the
// phone's HOME SCREEN row. Pure Node. Run: node web/tests/pwa.check.mjs
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { canOfferInstall, installState, installSteps, promptInstall, startApp } from "../shared/pwa.js";
import { installCtaHtml, settingsHtml } from "../phone/screens.js";

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, "..");
const read = (...parts) => readFileSync(join(web, ...parts), "utf8");
// URL path served by internal/server/static.go -> file under web/.
const fileOf = (url) => join(web, url === "/sw.js" ? "sw.js" : url.replace(/^\//, ""));

function pngSize(path) {
  const data = readFileSync(path);
  assert.equal(data.toString("latin1", 1, 4), "PNG", `${path} is a PNG`);
  return `${data.readUInt32BE(16)}x${data.readUInt32BE(20)}`;
}

for (const [page, manifestUrl, startUrl] of [["phone", "/phone/app.webmanifest", "/play"], ["table", "/table/app.webmanifest", "/"]]) {
  test(`pwa: the ${page} manifest is installable and its icons exist at their stated sizes`, () => {
    const manifest = JSON.parse(readFileSync(fileOf(manifestUrl), "utf8"));
    assert.equal(manifest.start_url, startUrl);
    assert.equal(manifest.scope, "/", "covers /play, /j/CODE and the table");
    assert.ok(["standalone", "fullscreen"].includes(manifest.display));
    assert.ok(manifest.name && manifest.short_name && manifest.short_name.length <= 12, "short_name fits under a home-screen icon");
    assert.match(manifest.theme_color, /^#[0-9A-F]{6}$/i);
    assert.match(manifest.background_color, /^#[0-9A-F]{6}$/i);
    const sizes = new Set();
    for (const icon of manifest.icons) {
      assert.ok(existsSync(fileOf(icon.src)), `${icon.src} exists`);
      assert.equal(pngSize(fileOf(icon.src)), icon.sizes, `${icon.src} is ${icon.sizes}`);
      sizes.add(`${icon.sizes} ${icon.purpose}`);
    }
    for (const need of ["192x192 any", "512x512 any", "512x512 maskable"]) assert.ok(sizes.has(need), `has a ${need} icon`);
    const html = read(page, "index.html");
    assert.ok(html.includes(`<link rel="manifest" href="${manifestUrl}">`), `${page} page links its manifest`);
    const touch = html.match(/<link rel="apple-touch-icon" href="([^"]+)">/);
    assert.ok(touch && existsSync(fileOf(touch[1])), "apple-touch-icon exists");
    assert.equal(pngSize(fileOf(touch[1])), "180x180");
  });
}

test("pwa: the manifests have different ids, so phone and table install as two apps", () => {
  const phone = JSON.parse(read("phone", "app.webmanifest"));
  const table = JSON.parse(read("table", "app.webmanifest"));
  assert.notEqual(phone.id, table.id);
});

test("pwa: the service worker precaches only files that exist and never answers for the API", () => {
  const sw = read("sw.js");
  const list = sw.match(/const PRECACHE = \[([^\]]+)\]/);
  assert.ok(list, "PRECACHE list");
  const urls = [...list[1].matchAll(/"([^"]+)"|OFFLINE/g)].map((m) => m[1] || sw.match(/const OFFLINE = "([^"]+)"/)[1]);
  assert.ok(urls.includes("/phone/offline.html"));
  for (const url of urls) assert.ok(existsSync(fileOf(url)), `${url} exists`);
  assert.match(sw, /startsWith\("\/api\/"\)/, "API requests pass straight through");
  assert.doesNotMatch(sw, /cache\.put|caches\.open\([^)]*\)\.then\(\(cache\) => cache\.put/, "game files are never cached");
});

test("pwa: the offline screen works with nothing but the worker's cache (and the page CSP)", () => {
  const html = read("phone", "offline.html");
  assert.doesNotMatch(html, /https?:\/\//, "no web fonts or other outside files");
  assert.doesNotMatch(html, /<script(?![^>]*\ssrc=)[^>]*>/, "no inline script (script-src 'self')");
  const sw = read("sw.js");
  for (const src of [...html.matchAll(/(?:src|href)="(\/[^"]+)"/g)].map((m) => m[1])) {
    assert.ok(sw.includes(`"${src}"`), `${src} is precached`);
  }
});

function fakeWindow({ secure = true, standalone = false, ua = "Mozilla/5.0 (Linux; Android 14) Chrome/130", touch = 5, worker = true } = {}) {
  const handlers = {};
  const registered = [];
  return {
    registered,
    isSecureContext: secure,
    navigator: { userAgent: ua, maxTouchPoints: touch, ...(worker ? { serviceWorker: { register: (url, opts) => { registered.push([url, opts.scope]); return Promise.resolve(); } } } : {}) },
    document: { readyState: "complete" },
    matchMedia: (query) => ({ matches: standalone && query === "(display-mode: standalone)" }),
    addEventListener: (type, fn) => { (handlers[type] ||= []).push(fn); },
    fire: (type, event) => (handlers[type] || []).forEach((fn) => fn(event)),
  };
}

const UA = {
  iphone: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
  ipad: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
  chromeAndroid: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
  samsung: "Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36",
  firefoxAndroid: "Mozilla/5.0 (Android 14; Mobile; rv:132.0) Gecko/132.0 Firefox/132.0",
  chromeDesktop: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
  edgeDesktop: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
  firefoxDesktop: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0",
};

test("pwa: installState knows every browser, so the button can always say what to do", () => {
  assert.equal(installState(fakeWindow({ standalone: true })), "installed");
  assert.equal(installState(fakeWindow({ secure: false })), "insecure", "home Wi-Fi address: send them to the https link");
  assert.equal(installState(fakeWindow({ ua: UA.chromeAndroid })), "android", "Chrome before it offers an install (needs a tap and ~30 s)");
  assert.equal(installState(fakeWindow({ ua: UA.samsung })), "samsung");
  assert.equal(installState(fakeWindow({ ua: UA.firefoxAndroid })), "firefox");
  assert.equal(installState(fakeWindow({ ua: UA.iphone })), "ios");
  assert.equal(installState(fakeWindow({ ua: UA.ipad, touch: 5 })), "ios", "iPadOS reports a Mac");
  assert.equal(installState(fakeWindow({ ua: UA.ipad, touch: 0 })), null, "Safari on a real Mac");
  assert.equal(installState(fakeWindow({ ua: UA.chromeDesktop, touch: 0 })), "desktop");
  assert.equal(installState(fakeWindow({ ua: UA.edgeDesktop, touch: 0 })), "desktop");
  assert.equal(installState(fakeWindow({ ua: UA.firefoxDesktop, touch: 0 })), null, "Firefox on a computer can't install");
  assert.equal(installState({}), null);
  for (const state of ["prompt", "ios", "android", "samsung", "firefox", "desktop", "insecure"]) {
    assert.ok(canOfferInstall(state), state);
    assert.ok(installSteps(state).length > 20, `${state} has steps`);
  }
  for (const state of ["installed", null]) assert.equal(canOfferInstall(state), false);
  assert.ok(installSteps("insecure", { publicUrl: "https://estate.example.net/" }).includes("https://estate.example.net/play"), "points at the https link");
  assert.match(installSteps("ios"), /Share.*Add to Home Screen/);
  assert.match(installSteps("android"), /⋮.*Add to Home screen/);
});

test("pwa: startApp registers /sw.js over HTTPS only and keeps the browser's install offer", async () => {
  const insecure = fakeWindow({ secure: false });
  startApp({ win: insecure });
  assert.deepEqual(insecure.registered, []);

  const win = fakeWindow();
  let changes = 0;
  startApp({ win, onChange: () => { changes += 1; } });
  assert.deepEqual(win.registered, [["/sw.js", "/"]]);

  let prevented = false;
  let prompted = false;
  win.fire("beforeinstallprompt", { preventDefault: () => { prevented = true; }, prompt: async () => { prompted = true; }, userChoice: Promise.resolve({ outcome: "accepted" }) });
  assert.ok(prevented, "the browser's own pop-up is held back (it would cover the action buttons)");
  assert.equal(changes, 1);
  assert.equal(installState(win), "prompt");
  assert.equal(await promptInstall(), true);
  assert.ok(prompted);
  assert.equal(installState(win), "android", "the offer is used once (back to the menu steps)");
  assert.equal(await promptInstall(), false, "nothing to prompt");

  win.fire("beforeinstallprompt", { preventDefault() {}, prompt: async () => {}, userChoice: Promise.resolve({ outcome: "dismissed" }) });
  win.fire("appinstalled", {});
  assert.equal(installState(win), "android", "installed from the browser menu clears the offer");
  assert.equal(changes, 3);
});

test("pwa: SETTINGS always has HOME SCREEN until the app is installed", () => {
  const prompt = settingsHtml({ app: "prompt" });
  assert.match(prompt, /HOME SCREEN/);
  assert.match(prompt, /data-cmd="install-app"/);
  const ios = settingsHtml({ app: "ios" });
  assert.match(ios, /Add to Home Screen/);
  assert.doesNotMatch(ios, /install-app/, "iPhones have no install button to press");
  assert.match(settingsHtml({ app: "android" }), /⋮ menu/, "Chrome before its offer: the menu steps");
  assert.ok(settingsHtml({ app: "insecure", publicUrl: "https://x.example" }).includes("https://x.example/play"), "home Wi-Fi: the https link");
  for (const app of [null, "installed", undefined]) assert.doesNotMatch(settingsHtml({ app }), /HOME SCREEN/, `nothing for ${app}`);
});

test("pwa: the phone home screen shows ADD ESTATE TO YOUR HOME SCREEN until installed or hidden", () => {
  for (const install of ["prompt", "ios", "android", "insecure"]) {
    const html = installCtaHtml({ install });
    assert.match(html, /ADD ESTATE TO YOUR HOME SCREEN/, install);
    assert.match(html, /data-cmd="install-app"/);
    assert.match(html, /data-cmd="install-hide"/);
  }
  assert.equal(installCtaHtml({ install: "installed" }), "");
  assert.equal(installCtaHtml({ install: null }), "");
  assert.equal(installCtaHtml({ install: "android", installHidden: true }), "", "hidden with ×");
});
