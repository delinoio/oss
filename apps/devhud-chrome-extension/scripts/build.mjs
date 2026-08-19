import { createHash, createPublicKey } from "node:crypto";
import { cp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { zipSync } from "fflate";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(root, "dist");
const artifacts = join(root, "artifacts");
const fixtureKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA2syUZItdwSJX09ANQq0K/QhpY5ToTHDJv8Ge7FeBkX4XIhzhJ5NXPnW0hDSBD0Jd5iy7J0Y6fol3NdRmPQFeT7H9eXRO2WsKYdOItonGS3PPUOnDT7PoUhVCPzE6lgpOob5LnylmFdYXZMnM1iIsVYEEa1L9LfL6L3C3y6ikzi5Gt0iHa6Ghl/NI7aSwc99B8L/+YzGaRCHYDq3L8FhAQygWDUba7NvYMw2B4yvXCcbp8vdvKLPSHq8fYYHiPGue58iOyFiHiNsX1XEtnksDLwIIWsSHw2QjI6iF279Boyhv5+cZdB7SZyrmi9QDJCBJcblMjA5R8z5fYLqGPDVDkwIDAQAB";
const fixtureId = "lmillpebkoiadcjhfimemdbcdhpafhgg";
const testBuild = process.env.DEVHUD_EXTENSION_TEST_BUILD === "1";
const publicKey = testBuild ? fixtureKey : process.env.DEVHUD_CHROME_EXTENSION_PUBLIC_KEY;
const extensionId = testBuild ? fixtureId : process.env.DEVHUD_CHROME_EXTENSION_ID;
if (!publicKey || !extensionId) throw new Error("release build requires DEVHUD_CHROME_EXTENSION_ID and DEVHUD_CHROME_EXTENSION_PUBLIC_KEY");
if (!/^[a-p]{32}$/u.test(extensionId)) throw new Error("Chrome extension ID must contain 32 lowercase a-p characters");
const der = Buffer.from(publicKey, "base64");
createPublicKey({ key: der, format: "der", type: "spki" });
const derivedId = [...createHash("sha256").update(der).digest().subarray(0, 16)].flatMap((byte) => [byte >> 4, byte & 15]).map((value) => String.fromCharCode(97 + value)).join("");
if (derivedId !== extensionId) throw new Error("Chrome extension ID does not match the configured public key");

await rm(dist, { recursive: true, force: true });
await rm(artifacts, { recursive: true, force: true });
await mkdir(dist, { recursive: true });
await mkdir(artifacts, { recursive: true });
await cp(join(root, "build"), dist, { recursive: true });
await cp(join(root, "public"), dist, { recursive: true });
await cp(resolve(root, "../devhud/src-tauri/icons/icon.png"), join(dist, "icon.png"));

const packageJson = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
const manifest = {
  manifest_version: 3,
  name: "__MSG_extensionName__",
  description: "__MSG_extensionDescription__",
  default_locale: "en",
  version: packageJson.version,
  key: publicKey,
  permissions: ["activeTab", "nativeMessaging", "scripting"],
  optional_host_permissions: ["http://*/*", "https://*/*"],
  incognito: "not_allowed",
  background: { service_worker: "service-worker.js", type: "module" },
  action: { default_popup: "popup.html", default_title: "DevHUD", default_icon: { "128": "icon.png" } },
  icons: { "128": "icon.png" },
};
await writeFile(join(dist, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);

async function files(directory) {
  const output = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) output.push(...await files(path)); else output.push(path);
  }
  return output.sort((left, right) => left.localeCompare(right));
}
const entries = {};
for (const path of await files(dist)) entries[relative(dist, path).replaceAll("\\", "/")] = [new Uint8Array(await readFile(path)), { mtime: new Date("1980-01-01T00:00:00.000Z") }];
await writeFile(join(artifacts, "devhud-chrome-extension.zip"), zipSync(entries, { level: 9 }));
await writeFile(join(artifacts, "release-identity.json"), `${JSON.stringify({ extensionId, origin: `chrome-extension://${extensionId}/`, hostName: "io.delino.devhud.native_messaging" }, null, 2)}\n`);
