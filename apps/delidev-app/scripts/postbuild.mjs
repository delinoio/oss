import { createHash } from "node:crypto";
import { copyFile, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distRoot = join(appRoot, "dist");

async function listFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map(async (entry) => {
      const absolute = join(directory, entry.name);
      return entry.isDirectory() ? listFiles(absolute) : [absolute];
    }),
  );
  return nested.flat();
}

await copyFile(join(distRoot, "index.html"), join(distRoot, "404.html"));
await mkdir(join(distRoot, "icons"), { recursive: true });

const files = await listFiles(distRoot);
const shellFiles = files
  .filter((file) => {
    const path = `/${relative(distRoot, file).replaceAll("\\", "/")}`;
    return (
      path === "/index.html" ||
      path === "/manifest.webmanifest" ||
      path.startsWith("/static/") ||
      path.startsWith("/icons/")
    );
  })
  .map((file) => `/${relative(distRoot, file).replaceAll("\\", "/")}`)
  .sort();

shellFiles.unshift("/");
const shellFingerprint = createHash("sha256")
  .update(shellFiles.join("\n"))
  .digest("hex")
  .slice(0, 12);
const template = await readFile(join(appRoot, "src/pwa/service-worker-template.js"), "utf8");
const serviceWorker = template
  .replace("__SHELL_VERSION__", shellFingerprint)
  .replace("__SHELL_FILES__", JSON.stringify(shellFiles));

await writeFile(join(distRoot, "sw.js"), serviceWorker);
