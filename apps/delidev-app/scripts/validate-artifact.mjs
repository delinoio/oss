import { access, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(appRoot, "dist");
const requiredFiles = [
  "index.html",
  "404.html",
  "_redirects",
  "_headers",
  "manifest.webmanifest",
  "sw.js",
  "icons/delidev-192.png",
  "icons/delidev-512.png",
  "icons/delidev-maskable-512.png",
];

await Promise.all(requiredFiles.map((path) => access(join(dist, path))));

const [index, fallback, redirects, manifestText, serviceWorker] =
  await Promise.all([
    readFile(join(dist, "index.html"), "utf8"),
    readFile(join(dist, "404.html"), "utf8"),
    readFile(join(dist, "_redirects"), "utf8"),
    readFile(join(dist, "manifest.webmanifest"), "utf8"),
    readFile(join(dist, "sw.js"), "utf8"),
  ]);
const manifest = JSON.parse(manifestText);

if (!index.includes('href="https://deli.dev/"')) {
  throw new Error("Production artifact is missing canonical deli.dev metadata.");
}
if (fallback !== index || !redirects.includes("/* /index.html 200")) {
  throw new Error("SPA fallback artifacts are missing or inconsistent.");
}
if (
  manifest.display !== "standalone" ||
  !manifest.icons.some((icon) => icon.sizes === "192x192") ||
  !manifest.icons.some((icon) => icon.sizes === "512x512")
) {
  throw new Error("Manifest is not installable.");
}
if (
  serviceWorker.includes("__SHELL_VERSION__") ||
  serviceWorker.includes("__SHELL_FILES__")
) {
  throw new Error("Service worker placeholders were not replaced.");
}
if (
  !serviceWorker.includes("CatalogService") ||
  !serviceWorker.includes('caches.match("/index.html")') ||
  serviceWorker.includes("BillingService") ||
  serviceWorker.includes("UsageService")
) {
  throw new Error("Service worker cache boundary is invalid.");
}

console.log(`Validated ${requiredFiles.length} DeliDev production artifacts.`);
