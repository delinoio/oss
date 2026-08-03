import { createHash } from "node:crypto";
import { copyFile, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distRoot = join(appRoot, "dist");
const devHudApplicationId = "dev.deli.devhud";
const devHudCallbackPath = "/auth/devhud/callback";

function appleTeamId() {
  const value = process.env.DELIDEV_DEVHUD_APPLE_TEAM_ID?.trim() ?? "";
  if (!/^[A-Z0-9]{10}$/u.test(value)) {
    throw new Error(
      "DELIDEV_DEVHUD_APPLE_TEAM_ID must be an externally supplied 10-character Apple Team ID.",
    );
  }
  return value;
}

function androidCertificateFingerprints() {
  const values = (process.env.DELIDEV_DEVHUD_ANDROID_CERTIFICATE_SHA256 ?? "")
    .split(",")
    .map((value) => value.trim().replaceAll(":", "").toUpperCase())
    .filter((value) => value.length > 0);
  if (values.length === 0 || values.some((value) => !/^[A-F0-9]{64}$/u.test(value))) {
    throw new Error(
      "DELIDEV_DEVHUD_ANDROID_CERTIFICATE_SHA256 must contain one or more externally supplied SHA-256 certificate fingerprints.",
    );
  }
  return [...new Set(values)].map((value) => value.match(/.{2}/gu)?.join(":") ?? value);
}

async function writeDevHudAssociationFiles() {
  const teamId = appleTeamId();
  const certificateFingerprints = androidCertificateFingerprints();
  const associationRoot = join(distRoot, ".well-known");
  await mkdir(associationRoot, { recursive: true });
  await Promise.all([
    writeFile(
      join(associationRoot, "apple-app-site-association"),
      `${JSON.stringify({
        applinks: {
          details: [{
            appIDs: [`${teamId}.${devHudApplicationId}`],
            components: [{ "/": devHudCallbackPath }],
          }],
        },
      }, null, 2)}\n`,
    ),
    writeFile(
      join(associationRoot, "assetlinks.json"),
      `${JSON.stringify([{
        relation: ["delegate_permission/common.handle_all_urls"],
        target: {
          namespace: "android_app",
          package_name: devHudApplicationId,
          sha256_cert_fingerprints: certificateFingerprints,
        },
      }], null, 2)}\n`,
    ),
  ]);
}

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
await writeDevHudAssociationFiles();

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
const shellHash = createHash("sha256");
for (const path of shellFiles) {
  shellHash.update(path);
  if (path !== "/") {
    shellHash.update(await readFile(join(distRoot, path.slice(1))));
  }
}
const shellFingerprint = shellHash.digest("hex").slice(0, 12);
const template = await readFile(join(appRoot, "src/pwa/service-worker-template.js"), "utf8");
const serviceWorker = template
  .replace("__SHELL_VERSION__", shellFingerprint)
  .replace("__SHELL_FILES__", JSON.stringify(shellFiles));

await writeFile(join(distRoot, "sw.js"), serviceWorker);
