// Estate's service worker, served at /sw.js so it covers /play, /j/CODE and the table at /.
//
// It only adds an offline screen. Game files are never served from this cache: every page and
// file still comes from the server, which answers unchanged files with a 304, so an installed app
// can never run old code against a newer server after a deploy. When a page can't be fetched at
// all (no connection, or the host's computer is off) the app shows /phone/offline.html instead
// of the browser's error page.
//
// Bump CACHE when the offline screen changes; old caches are deleted on activate.
const CACHE = "estate-offline-1";
const OFFLINE = "/phone/offline.html";
const PRECACHE = [OFFLINE, "/phone/offline.js", "/shared/app-icons/apple-touch-icon.png"];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(PRECACHE)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter((key) => key.startsWith("estate-") && key !== CACHE).map((key) => caches.delete(key)));
    if (self.registration.navigationPreload) await self.registration.navigationPreload.enable().catch(() => {});
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (url.origin === self.location.origin && PRECACHE.includes(url.pathname) && request.method === "GET") {
    // The offline screen's own files: from the network, or from the cache when there is none.
    event.respondWith(fetch(request).catch(() => caches.match(url.pathname)));
    return;
  }
  // Everything else except page loads goes straight to the network, untouched.
  if (request.mode !== "navigate" || request.method !== "GET" || url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/classic")) return;
  event.respondWith((async () => {
    try {
      const preloaded = await event.preloadResponse;
      if (preloaded) return preloaded;
      return await fetch(request);
    } catch {
      const offline = await caches.match(OFFLINE);
      return offline || new Response("Estate can't reach the table. Check your connection and try again.", {
        status: 503,
        headers: { "Content-Type": "text/plain; charset=utf-8" },
      });
    }
  })());
});
