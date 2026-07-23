/* DeliDev service worker: shell and anonymous catalog responses only. */
const SHELL_VERSION = "63af4620df53";
const SHELL_CACHE = `delidev-shell-${SHELL_VERSION}`;
const PUBLIC_CATALOG_CACHE = `delidev-public-catalog-${SHELL_VERSION}`;
const PUBLIC_CATALOG_ORIGIN = "https://delibase.deli.dev";
const SHELL_FILES = ["/","/icons/delidev-192.png","/icons/delidev-512.png","/icons/delidev-maskable-512.png","/icons/delidev.svg","/index.html","/manifest.webmanifest","/static/css/index.494f825e68.css","/static/js/641.0aace7fd21.js","/static/js/641.0aace7fd21.js.LICENSE.txt","/static/js/index.51122ae10b.js","/static/js/lib-react.2f531ee03e.js","/static/js/lib-react.2f531ee03e.js.LICENSE.txt","/static/js/lib-router.a751045bad.js","/static/js/lib-router.a751045bad.js.LICENSE.txt"];
const SHELL_PATHS = new Set(SHELL_FILES.map((path) => new URL(path, self.location.origin).pathname));
const PUBLIC_CATALOG_METHODS = new Set([
  "ListCatalogApps",
  "GetCatalogApp",
  "ListCatalogMeters",
  "GetCatalogMeter",
]);

function publicCatalogMethod(url) {
  const prefix = "/delibase.v1.CatalogService/";
  if (
    url.origin !== PUBLIC_CATALOG_ORIGIN ||
    !url.pathname.startsWith(prefix)
  ) {
    return undefined;
  }
  const method = url.pathname.slice(prefix.length);
  return PUBLIC_CATALOG_METHODS.has(method) ? method : undefined;
}

async function catalogCacheKey(request, method) {
  const body = await request.clone().arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", body);
  const hash = [...new Uint8Array(digest)]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
  return new Request(
    `${self.location.origin}/__delidev_public_catalog__/${method}/${hash}`,
    { method: "GET" },
  );
}

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.addAll(SHELL_FILES)),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter(
              (key) =>
                (key.startsWith("delidev-shell-") ||
                  key.startsWith("delidev-public-catalog-")) &&
                key !== SHELL_CACHE &&
                key !== PUBLIC_CATALOG_CACHE,
            )
            .map((key) => caches.delete(key)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") {
    self.skipWaiting();
  }
});

self.addEventListener("fetch", (event) => {
  const { request } = event;
  const url = new URL(request.url);

  if (
    request.method === "GET" &&
    request.mode === "navigate" &&
    url.origin === self.location.origin
  ) {
    event.respondWith(
      fetch(request).catch(async () => {
        const shell = await caches.match("/index.html");
        return (
          shell ||
          new Response("DeliDev is unavailable offline.", {
            status: 503,
            headers: { "content-type": "text/plain" },
          })
        );
      }),
    );
    return;
  }

  if (
    request.method === "GET" &&
    url.origin === self.location.origin &&
    SHELL_PATHS.has(url.pathname)
  ) {
    event.respondWith(
      caches.match(request).then((cached) => cached || fetch(request)),
    );
    return;
  }

  const method = publicCatalogMethod(url);
  if (
    request.method === "POST" &&
    method &&
    !request.headers.has("authorization")
  ) {
    event.respondWith(
      (async () => {
        const key = await catalogCacheKey(request, method);
        try {
          const response = await fetch(request);
          if (response.ok) {
            const cache = await caches.open(PUBLIC_CATALOG_CACHE);
            await cache.put(key, response.clone());
          }
          return response;
        } catch {
          const cached = await caches.match(key);
          if (cached) {
            return cached;
          }
          return new Response(
            JSON.stringify({
              code: "unavailable",
              message: "Public catalog is unavailable while offline.",
            }),
            {
              status: 503,
              headers: { "content-type": "application/json" },
            },
          );
        }
      })(),
    );
  }
});
