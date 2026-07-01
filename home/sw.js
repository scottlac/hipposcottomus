// hipposcottomus service worker
//
// Strategy:
//   - Pre-cache the dashboard shells + manifest/icon on install.
//   - Network-only for /api/* and /metrics — these are time-sensitive
//     and shouldn't be served stale.
//   - Cache-first for other same-origin GETs, with runtime caching of
//     successful responses so subsequent navigations work offline.
//
// Bump CACHE_VERSION on any frontend change you want to force-refresh.

const CACHE_VERSION = "hippo-v9";

const SHELL = [
  "/",
  "/manifest.json",
  "/icon.svg",
  "/apple-touch-icon.png",
  "/icon-512.png",
  "/og.png",
  "/lakedashboard/",
  "/gaston/",
  "/minneola/",
  "/conway/",
  "/ftlauderdale/",
  "/poker/",
  "/qr/",
  "/astronomy/",
  "/analytics/",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE_VERSION)
      .then((cache) => cache.addAll(SHELL))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys.filter((k) => k !== CACHE_VERSION).map((k) => caches.delete(k))
        )
      )
      .then(() => self.clients.claim())
  );
});

function isApiPath(pathname) {
  return (
    pathname.includes("/api/") ||
    pathname === "/metrics" ||
    pathname === "/healthz"
  );
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;

  const url = new URL(req.url);

  // Only handle same-origin requests. Let CDN traffic (Pico, Chart.js,
  // Leaflet, unpkg) follow its own caching rules.
  if (url.origin !== self.location.origin) return;

  if (isApiPath(url.pathname)) return;

  event.respondWith(
    caches.match(req).then((cached) => {
      if (cached) return cached;
      return fetch(req)
        .then((res) => {
          // Cache successful, basic, opaque-allowed same-origin responses.
          if (res && res.ok && res.type === "basic") {
            const clone = res.clone();
            caches
              .open(CACHE_VERSION)
              .then((cache) => cache.put(req, clone))
              .catch(() => {});
          }
          return res;
        })
        .catch(() => caches.match("/"));
    })
  );
});
