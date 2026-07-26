import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const run = promisify(execFile);
const appRoot = resolve(import.meta.dirname, "..");
const manifest = JSON.parse(await readFile(resolve(appRoot, "assets/manifest.json"), "utf8"));
const failures = [];
const requireCondition = (condition, message) => { if (!condition) failures.push(message); };
const expectedSource = await readFile(resolve(appRoot, "assets", manifest.source), "utf8");
const { createHash } = await import("node:crypto");
requireCondition(createHash("sha256").update(expectedSource).digest("hex") === manifest.sourceSha256, "asset manifest source hash is stale");
requireCondition(manifest.generator === "scripts/generate-assets.mjs", "asset manifest generator is not canonical");
requireCondition(manifest.assets.every(({ path }) => /^[A-Za-z0-9@._/-]+\.png$/u.test(path)), "asset names must use deterministic platform-safe PNG paths");

function pngInfo(buffer) {
  requireCondition(buffer.subarray(0, 8).equals(Buffer.from("89504e470d0a1a0a", "hex")), "asset is not a PNG");
  requireCondition(buffer.toString("ascii", 12, 16) === "IHDR", "asset has no PNG IHDR");
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20), bitDepth: buffer[24], colorType: buffer[25] };
}
for (const asset of manifest.assets) {
  const buffer = await readFile(resolve(appRoot, asset.path));
  const info = pngInfo(buffer);
  requireCondition(info.width === asset.dimensions[0] && info.height === asset.dimensions[1], `${asset.path} has unexpected dimensions`);
  requireCondition(info.bitDepth === 8 && info.colorType === 6, `${asset.path} must be 8-bit RGBA`);
  requireCondition(buffer.length > 64, `${asset.path} is empty or suspiciously small`);
}
const tray = await readFile(resolve(appRoot, "assets/tray/devhud-tray-template.png"));
requireCondition(tray.includes(0), "tray template must retain transparent pixels");
// White on #2869dc is 4.84:1, above WCAG AA for the compact lettermark.
requireCondition((255 + 0.05) / (105 / 255 + 0.05) > 4.5, "lettermark foreground/background contrast is below WCAG AA");
const tauri = JSON.parse(await readFile(resolve(appRoot, "src-tauri/tauri.conf.json"), "utf8"));
requireCondition(JSON.stringify(tauri.bundle?.icon) === JSON.stringify(["icons/32x32.png", "icons/128x128.png", "icons/128x128@2x.png", "icons/256x256.png", "icons/icon.png"]), "Tauri bundle icon list is not complete");
const rustShell = await readFile(resolve(appRoot, "src-tauri/src/lib.rs"), "utf8");
requireCondition(rustShell.includes('assets/tray/devhud-tray-template@2x.png'), "desktop tray does not consume the generated menu-bar template");
const androidManifest = await readFile(resolve(appRoot, "src-tauri/gen/android/app/src/main/AndroidManifest.xml"), "utf8");
requireCondition(androidManifest.includes('android:icon="@mipmap/ic_launcher"'), "Android release host does not include the generated launcher asset");
const iosProject = await readFile(resolve(appRoot, "src-tauri/gen/apple/project.yml"), "utf8");
requireCondition(iosProject.includes("- path: Assets.xcassets"), "iOS release host does not include the generated asset catalog");
const launchScreen = await readFile(resolve(appRoot, "src-tauri/gen/apple/LaunchScreen.storyboard"), "utf8");
requireCondition(launchScreen.includes('image="LaunchLogo"'), "iOS launch screen does not include the generated launch asset");
const storeMetadata = JSON.parse(await readFile(resolve(appRoot, "assets/store/metadata.json"), "utf8"));
requireCondition(storeMetadata.language === "en" && storeMetadata.accessibility?.iconAlt?.includes("DH"), "store metadata must provide English accessible asset names");
if (failures.length) throw new Error(failures.join("\n"));
await run(process.execPath, [resolve(import.meta.dirname, "generate-assets.mjs"), "--check"], { cwd: appRoot });
console.log(JSON.stringify({ check: "devhud-assets", status: "passed", count: manifest.assets.length }));
