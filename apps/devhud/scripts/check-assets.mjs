import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const run = promisify(execFile);
const appRoot = resolve(import.meta.dirname, "..");
const manifest = JSON.parse(await readFile(resolve(appRoot, "assets/manifest.json"), "utf8"));
const failures = [];
const requireCondition = (condition, message) => { if (!condition) failures.push(message); };
const normalizeText = (value) => value.replace(/\r\n?/gu, "\n");
const canonicalSource = "source/devhud-lettermark.svg";
const expectedSource = await readFile(resolve(appRoot, "assets", canonicalSource), "utf8");
requireCondition(manifest.source === canonicalSource, "asset manifest source is not canonical");
const { createHash } = await import("node:crypto");
const { inflateSync } = await import("node:zlib");
requireCondition(createHash("sha256").update(normalizeText(expectedSource)).digest("hex") === manifest.sourceSha256, "asset manifest source hash is stale");
requireCondition(manifest.generator === "scripts/generate-assets.mjs", "asset manifest generator is not canonical");
requireCondition(manifest.assets.every(({ path }) => /^[A-Za-z0-9@._/-]+\.(?:png|ico|icns)$/u.test(path)), "asset names must use deterministic platform-safe image paths");
for (const file of manifest.generatedFiles ?? []) {
  const contents = await readFile(resolve(appRoot, file.path), "utf8").catch(() => null);
  requireCondition(contents !== null, `${file.path} is missing`);
  if (contents !== null) requireCondition(createHash("sha256").update(normalizeText(contents)).digest("hex") === file.sha256, `${file.path} is stale`);
}

function sourceColor(source, selector) {
  const match = source.match(new RegExp(`<${selector}[^>]*\\bfill=["']#([0-9a-f]{3}|[0-9a-f]{6})["']`, "i"));
  if (!match) return null;
  const value = match[1].length === 3 ? match[1].split("").map((digit) => `${digit}${digit}`).join("") : match[1];
  return [Number.parseInt(value.slice(0, 2), 16), Number.parseInt(value.slice(2, 4), 16), Number.parseInt(value.slice(4), 16)];
}
function luminance([red, green, blue]) {
  return [red, green, blue].map((channel) => channel / 255).map((channel) => (channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4)).reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
}
const background = sourceColor(expectedSource, "rect");
const foreground = sourceColor(expectedSource, "path");
if (background && foreground) {
  const contrast = (Math.max(luminance(background), luminance(foreground)) + 0.05) / (Math.min(luminance(background), luminance(foreground)) + 0.05);
  requireCondition(contrast >= 4.5, "lettermark foreground/background contrast is below WCAG AA");
} else {
  requireCondition(false, "canonical SVG colors are missing or unsupported");
}

function pngInfo(buffer) {
  requireCondition(buffer.subarray(0, 8).equals(Buffer.from("89504e470d0a1a0a", "hex")), "asset is not a PNG");
  requireCondition(buffer.toString("ascii", 12, 16) === "IHDR", "asset has no PNG IHDR");
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20), bitDepth: buffer[24], colorType: buffer[25] };
}
function alphaCoverage(buffer, { width, height, colorType }) {
  if (colorType === 2) return 1;
  const idat = []; let offset = 8;
  while (offset < buffer.length) { const length = buffer.readUInt32BE(offset); const type = buffer.toString("ascii", offset + 4, offset + 8); if (type === "IDAT") idat.push(buffer.subarray(offset + 8, offset + 8 + length)); offset += 12 + length; }
  const rows = inflateSync(Buffer.concat(idat)); let opaque = 0;
  for (let y = 0; y < height; y += 1) for (let x = 0; x < width; x += 1) if (rows[y * (width * 4 + 1) + 1 + x * 4 + 3] !== 0) opaque += 1;
  return opaque / (width * height);
}
for (const asset of manifest.assets) {
  const buffer = await readFile(resolve(appRoot, asset.path));
  if (asset.format === "ico" || asset.format === "icns") {
    if (asset.format === "ico") {
      requireCondition(buffer.readUInt16LE(0) === 0 && buffer.readUInt16LE(2) === 1 && buffer.readUInt16LE(4) >= 1, `${asset.path} is not a valid ICO container`);
      requireCondition(buffer.subarray(22, 30).equals(Buffer.from("89504e470d0a1a0a", "hex")), `${asset.path} must contain a PNG image payload`);
    } else {
      requireCondition(buffer.toString("ascii", 0, 4) === "icns" && buffer.readUInt32BE(4) === buffer.length, `${asset.path} is not a valid ICNS container`);
      requireCondition(buffer.toString("ascii", 8, 12) === "ic09", `${asset.path} must contain a 512px PNG icon payload`);
    }
    continue;
  }
  const info = pngInfo(buffer);
  requireCondition(info.width === asset.dimensions[0] && info.height === asset.dimensions[1], `${asset.path} has unexpected dimensions`);
  const requiresOpaque = asset.path.includes("AppIcon") || asset.path.includes("mipmap-") || asset.path.includes("assets/store/") || asset.path.endsWith("assets/tray/devhud-tray.png") || asset.path.endsWith("assets/tray/devhud-tray@2x.png");
  requireCondition(info.bitDepth === 8 && info.colorType === (requiresOpaque ? 2 : 6), `${asset.path} must be 8-bit ${requiresOpaque ? "RGB" : "RGBA"}`);
  requireCondition(buffer.length > 64, `${asset.path} is empty or suspiciously small`);
  const coverage = alphaCoverage(buffer, info);
  requireCondition(coverage > 0, `${asset.path} has no opaque pixels`);
  if (asset.path.includes("AppIcon") || asset.path.includes("mipmap-")) requireCondition(coverage === 1, `${asset.path} must be fully opaque`);
  if (asset.path.includes("assets/tray/")) requireCondition(coverage > 0.1, `${asset.path} has insufficient opaque tray coverage`);
}
const tray = await readFile(resolve(appRoot, "assets/tray/devhud-tray-template.png"));
requireCondition(alphaCoverage(tray, pngInfo(tray)) < 1, "tray template must retain transparent pixels");
const tauri = JSON.parse(await readFile(resolve(appRoot, "src-tauri/tauri.conf.json"), "utf8"));
requireCondition(JSON.stringify(tauri.bundle?.icon) === JSON.stringify(["icons/32x32.png", "icons/128x128.png", "icons/128x128@2x.png", "icons/256x256.png", "icons/icon.png", "icons/icon.ico", "icons/icon.icns"]), "Tauri bundle icon list is not complete");
const rustShell = await readFile(resolve(appRoot, "src-tauri/src/lib.rs"), "utf8");
requireCondition(rustShell.includes('assets/tray/devhud-tray-template@2x.png'), "desktop tray does not consume the generated menu-bar template");
requireCondition(rustShell.includes('assets/tray/devhud-tray@2x.png'), "desktop tray does not consume the generated non-macOS asset");
const androidManifest = await readFile(resolve(appRoot, "src-tauri/gen/android/app/src/main/AndroidManifest.xml"), "utf8");
requireCondition(androidManifest.includes('android:icon="@mipmap/ic_launcher"'), "Android release host does not include the generated launcher asset");
requireCondition(androidManifest.includes('android:roundIcon="@mipmap/ic_launcher_round"'), "Android release host does not include the generated round launcher asset");
const iosProject = await readFile(resolve(appRoot, "src-tauri/gen/apple/project.yml"), "utf8");
requireCondition(iosProject.includes("- path: Assets.xcassets"), "iOS release host does not include the generated asset catalog");
requireCondition(iosProject.includes("ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon"), "iOS release host does not declare AppIcon as its primary icon set");
const launchScreen = await readFile(resolve(appRoot, "src-tauri/gen/apple/LaunchScreen.storyboard"), "utf8");
requireCondition(launchScreen.includes('image="LaunchLogo"'), "iOS launch screen does not include the generated launch asset");
requireCondition(/<subviews>\s*<imageView[\s\S]*image="LaunchLogo"[\s\S]*<\/subviews>/u.test(launchScreen), "iOS launch screen must nest LaunchLogo inside view subviews");
const storeMetadata = JSON.parse(await readFile(resolve(appRoot, "assets/store/metadata.json"), "utf8"));
requireCondition(
  storeMetadata.language === "en" &&
    storeMetadata.icon === "devhud-play-icon-512.png" &&
    storeMetadata.featureGraphic === "devhud-store-feature-1024x500.png" &&
    storeMetadata.accessibility?.iconAlt?.includes("DH") &&
    typeof storeMetadata.accessibility?.featureGraphicAlt === "string" &&
    storeMetadata.accessibility.featureGraphicAlt.trim().length > 0,
  "store metadata must provide the 512px English Play icon, feature graphic, and accessible asset names",
);
if (failures.length) throw new Error(failures.join("\n"));
await run(process.execPath, [resolve(import.meta.dirname, "generate-assets.mjs"), "--check"], { cwd: appRoot });
console.log(JSON.stringify({ check: "devhud-assets", status: "passed", count: manifest.assets.length }));
