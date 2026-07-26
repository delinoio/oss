import { createHash } from "node:crypto";
import { deflateSync } from "node:zlib";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const sourcePath = join(appRoot, "assets/source/devhud-lettermark.svg");
const blue = [40, 105, 220, 255];
const white = [255, 255, 255, 255];
const transparent = [0, 0, 0, 0];

const files = new Map([
  ["src-tauri/icons/icon.png", 512],
  ["src-tauri/icons/32x32.png", 32],
  ["src-tauri/icons/128x128.png", 128],
  ["src-tauri/icons/128x128@2x.png", 256],
  ["src-tauri/icons/256x256.png", 256],
  ["assets/application/devhud-application-1024.png", 1024],
  ["assets/installer/devhud-installer-256.png", 256],
  ["assets/launch/devhud-launch-2048.png", 2048],
  ["src-tauri/gen/apple/Assets.xcassets/LaunchLogo.imageset/devhud-launch.png", 512],
  ["assets/store/devhud-store-icon-1024.png", 1024],
  ["assets/store/devhud-store-feature-1024x500.png", [1024, 500]],
  ["assets/tray/devhud-tray-template.png", 18],
  ["assets/tray/devhud-tray-template@2x.png", 36],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-20x20@1x.png", 20],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-20x20@2x-1.png", 40],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-20x20@2x.png", 40],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-20x20@3x.png", 60],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-29x29@1x.png", 29],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-29x29@2x-1.png", 58],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-29x29@2x.png", 58],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-29x29@3x.png", 87],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-40x40@1x.png", 40],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-40x40@2x-1.png", 80],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-40x40@2x.png", 80],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-40x40@3x.png", 120],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-60x60@2x.png", 120],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-60x60@3x.png", 180],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-76x76@1x.png", 76],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-76x76@2x.png", 152],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-83.5x83.5@2x.png", 167],
  ["src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png", 1024],
]);

function chunk(type, data) {
  const length = Buffer.alloc(4); length.writeUInt32BE(data.length);
  const name = Buffer.from(type);
  let crcValue = 0xffffffff;
  for (const byte of Buffer.concat([name, data])) {
    crcValue ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crcValue = (crcValue >>> 1) ^ (0xedb88320 & -(crcValue & 1));
  }
  const crc = Buffer.alloc(4); crc.writeUInt32BE((crcValue ^ 0xffffffff) >>> 0);
  return Buffer.concat([length, name, data, crc]);
}

// PNG output is intentionally produced here instead of by a platform-specific
// editor so regeneration is byte-stable on every supported build host.
function png(width, height, tray = false) {
  const pixels = Buffer.alloc(width * height * 4);
  const scale = Math.max(width, height) / 512;
  const radius = 112 * scale;
  const paint = (x, y, color) => {
    const offset = (y * width + x) * 4;
    for (let i = 0; i < 4; i += 1) pixels[offset + i] = color[i];
  };
  const insideRoundRect = (x, y) => {
    const left = Math.min(x, width - 1 - x); const top = Math.min(y, height - 1 - y);
    if (left >= radius || top >= radius) return true;
    const dx = radius - left - 0.5; const dy = radius - top - 0.5;
    return dx * dx + dy * dy <= radius * radius;
  };
  const box = (x, y) => x >= 88 * scale && y >= 128 * scale && x < 424 * scale && y < 384 * scale;
  for (let y = 0; y < height; y += 1) for (let x = 0; x < width; x += 1) {
    const sx = (x + 0.5) / scale; const sy = (y + 0.5) / scale;
    let color = transparent;
    if (!tray && insideRoundRect(x, y)) color = blue;
    if (tray && box(sx, sy)) color = [0, 0, 0, 255];
    // The two letterforms use the same simple block geometry as the source SVG.
    const d = sx >= 88 && sx < 268 && sy >= 128 && sy < 384;
    const dHole = sx >= 144 && sx < 232 && sy >= 180 && sy < 332 && !(sx < 204 && sy >= 180 && sy < 332);
    const h = sx >= 296 && sx < 440 && sy >= 128 && sy < 384 && (sx < 352 || sx >= 384 || (sy >= 228 && sy < 284));
    if (!tray && ((d && !dHole) || h)) color = white;
    if (tray && !box(sx, sy)) color = transparent;
    paint(x, y, color);
  }
  const rows = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y += 1) { rows[y * (width * 4 + 1)] = 0; pixels.copy(rows, y * (width * 4 + 1) + 1, y * width * 4, (y + 1) * width * 4); }
  const header = Buffer.alloc(13); header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 6;
  return Buffer.concat([Buffer.from("\x89PNG\r\n\x1a\n", "binary"), chunk("IHDR", header), chunk("IDAT", deflateSync(rows, { level: 9 }),), chunk("IEND", Buffer.alloc(0))]);
}

function dimensions(value) { return Array.isArray(value) ? value : [value, value]; }
async function outputs() {
  const source = await readFile(sourcePath, "utf8");
  const sourceHash = createHash("sha256").update(source).digest("hex");
  return { sourceHash, entries: [...files].map(([relativePath, size]) => ({ relativePath, size, data: png(...dimensions(size), relativePath.includes("tray")) })) };
}

const check = process.argv.includes("--check");
const { sourceHash, entries } = await outputs();
for (const { relativePath, data } of entries) {
  const destination = join(appRoot, relativePath);
  if (check) {
    const existing = await readFile(destination).catch(() => null);
    if (!existing || !existing.equals(data)) throw new Error(`asset is stale or missing: ${relativePath}`);
  } else {
    await mkdir(dirname(destination), { recursive: true });
    await writeFile(destination, data);
  }
}
if (!check) {
  await writeFile(join(appRoot, "assets/manifest.json"), `${JSON.stringify({ source: "source/devhud-lettermark.svg", sourceSha256: sourceHash, generator: "scripts/generate-assets.mjs", assets: entries.map(({ relativePath, size }) => ({ path: relativePath, dimensions: dimensions(size) })) }, null, 2)}\n`);
}
console.log(JSON.stringify({ check: "devhud-assets", status: "passed", mode: check ? "verify" : "generated", sourceSha256: sourceHash, count: entries.length }));
