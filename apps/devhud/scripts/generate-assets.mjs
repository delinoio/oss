import { createHash } from "node:crypto";
import { deflateSync } from "node:zlib";
import { mkdir, readFile, unlink, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const sourcePath = join(appRoot, "assets/source/devhud-lettermark.svg");
const manifestPath = join(appRoot, "assets/manifest.json");
const blue = [40, 105, 220, 255];
const white = [255, 255, 255, 255];
const transparent = [0, 0, 0, 0];
let canonicalPolygons;

const files = new Map([
  ["src-tauri/icons/icon.ico", { format: "ico", size: 256 }],
  ["src-tauri/icons/icon.icns", { format: "icns", size: 512 }],
  ["src-tauri/icons/icon.png", 512],
  ["src-tauri/icons/32x32.png", 32],
  ["src-tauri/icons/128x128.png", 128],
  ["src-tauri/icons/128x128@2x.png", 256],
  ["src-tauri/icons/256x256.png", 256],
  ["assets/application/devhud-application-1024.png", 1024],
  ["assets/installer/devhud-installer-256.png", 256],
  ["assets/launch/devhud-launch-2048.png", 2048],
  ["src-tauri/gen/apple/Assets.xcassets/LaunchLogo.imageset/devhud-launch.png", 512],
  ["assets/store/devhud-store-icon-1024.png", { size: 1024, opaque: true }],
  ["assets/store/devhud-play-icon-512.png", { size: 512, opaque: true }],
  ["assets/store/devhud-store-feature-1024x500.png", { size: [1024, 500], opaque: true }],
  ["assets/tray/devhud-tray-template.png", 18],
  ["assets/tray/devhud-tray-template@2x.png", 36],
  ["assets/tray/devhud-tray.png", { size: 18, opaque: true, tray: false }],
  ["assets/tray/devhud-tray@2x.png", { size: 36, opaque: true, tray: false }],
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
  ["src-tauri/gen/android/app/src/main/res/mipmap-mdpi/ic_launcher.png", { size: 48, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-mdpi/ic_launcher_round.png", { size: 48, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-hdpi/ic_launcher.png", { size: 72, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-hdpi/ic_launcher_round.png", { size: 72, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xhdpi/ic_launcher.png", { size: 96, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xhdpi/ic_launcher_round.png", { size: 96, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xxhdpi/ic_launcher.png", { size: 144, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xxhdpi/ic_launcher_round.png", { size: 144, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png", { size: 192, opaque: true }],
  ["src-tauri/gen/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher_round.png", { size: 192, opaque: true }],
]);

const generatedFiles = new Map([
  [
    "src-tauri/gen/apple/Assets.xcassets/AppIcon.appiconset/Contents.json",
    `${JSON.stringify({
      images: [
        { size: "20x20", idiom: "iphone", filename: "AppIcon-20x20@2x.png", scale: "2x" },
        { size: "20x20", idiom: "iphone", filename: "AppIcon-20x20@3x.png", scale: "3x" },
        { size: "29x29", idiom: "iphone", filename: "AppIcon-29x29@2x-1.png", scale: "2x" },
        { size: "29x29", idiom: "iphone", filename: "AppIcon-29x29@3x.png", scale: "3x" },
        { size: "40x40", idiom: "iphone", filename: "AppIcon-40x40@2x.png", scale: "2x" },
        { size: "40x40", idiom: "iphone", filename: "AppIcon-40x40@3x.png", scale: "3x" },
        { size: "60x60", idiom: "iphone", filename: "AppIcon-60x60@2x.png", scale: "2x" },
        { size: "60x60", idiom: "iphone", filename: "AppIcon-60x60@3x.png", scale: "3x" },
        { size: "20x20", idiom: "ipad", filename: "AppIcon-20x20@1x.png", scale: "1x" },
        { size: "20x20", idiom: "ipad", filename: "AppIcon-20x20@2x-1.png", scale: "2x" },
        { size: "29x29", idiom: "ipad", filename: "AppIcon-29x29@1x.png", scale: "1x" },
        { size: "29x29", idiom: "ipad", filename: "AppIcon-29x29@2x.png", scale: "2x" },
        { size: "40x40", idiom: "ipad", filename: "AppIcon-40x40@1x.png", scale: "1x" },
        { size: "40x40", idiom: "ipad", filename: "AppIcon-40x40@2x-1.png", scale: "2x" },
        { size: "76x76", idiom: "ipad", filename: "AppIcon-76x76@1x.png", scale: "1x" },
        { size: "76x76", idiom: "ipad", filename: "AppIcon-76x76@2x.png", scale: "2x" },
        { size: "83.5x83.5", idiom: "ipad", filename: "AppIcon-83.5x83.5@2x.png", scale: "2x" },
        { size: "1024x1024", idiom: "ios-marketing", filename: "AppIcon-512@2x.png", scale: "1x" },
      ],
      info: { version: 1, author: "xcode" },
    })}\n`,
  ],
  [
    "src-tauri/gen/apple/Assets.xcassets/LaunchLogo.imageset/Contents.json",
    `${JSON.stringify({
      images: [{ idiom: "universal", filename: "devhud-launch.png", scale: "1x" }],
      info: { version: 1, author: "devhud-assets" },
    })}\n`,
  ],
]);

// Cleanup is restricted to stable generated namespaces. The current output map
// alone cannot remove a generated file after its directory is renamed.
const approvedGeneratedRoots = [
  "assets",
  "src-tauri/icons",
  "src-tauri/gen/apple/Assets.xcassets",
  "src-tauri/gen/android/app/src/main/res",
].map((path) => resolve(appRoot, path));

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
function parseColor(source, name, fallback) {
  const values = [...source.matchAll(/fill=["']#([0-9a-f]{3}|[0-9a-f]{6})["']/gi)].map((match) => match[1]);
  const value = values[name === "white" ? 1 : 0];
  if (!value) return fallback;
  const expanded = value.length === 3 ? value.split("").map((digit) => `${digit}${digit}`).join("") : value;
  return [Number.parseInt(expanded.slice(0, 2), 16), Number.parseInt(expanded.slice(2, 4), 16), Number.parseInt(expanded.slice(4), 16), 255];
}

function parseRect(source) {
  const match = source.match(/<rect\s+([^>]+)>/i);
  if (!match) throw new Error("canonical SVG must contain a background rect");
  const attributes = Object.fromEntries([...match[1].matchAll(/([a-z]+)=["']([^"']+)["']/gi)].map((entry) => [entry[1], Number(entry[2])]));
  const { x = 0, y = 0, width, height, rx, ry = rx } = attributes;
  if (![width, height, rx].every(Number.isFinite)) throw new Error("canonical SVG background rect is incomplete");
  return { x, y, width, height, rx, ry };
}

function pathPolygons(source) {
  const paths = [...source.matchAll(/<path[^>]+d=["']([^"']+)["'][^>]*>/g)].map((match) => match[1]);
  const token = /([a-z])|(-?\d*\.?\d+(?:e[-+]?\d+)?)/gi;
  return paths.map((path) => {
    const tokens = [...path.matchAll(token)].map((match) => match[1] ?? Number(match[2]));
    let index = 0; let command = ""; let x = 0; let y = 0; let startX = 0; let startY = 0; let previous = null;
    const polygons = []; let polygon = [];
    const next = () => tokens[index++];
    const point = (px, py) => { polygon.push([px, py]); x = px; y = py; };
    const cubic = (x1, y1, x2, y2, x3, y3) => {
      const [sx, sy] = [x, y];
      for (let step = 1; step <= 12; step += 1) { const t = step / 12; const u = 1 - t; point(u ** 3 * sx + 3 * u ** 2 * t * x1 + 3 * u * t ** 2 * x2 + t ** 3 * x3, u ** 3 * sy + 3 * u ** 2 * t * y1 + 3 * u * t ** 2 * y2 + t ** 3 * y3); }
      previous = [x2, y2];
    };
    while (index < tokens.length) {
      if (typeof tokens[index] === "string") command = next();
      const relative = command === command.toLowerCase(); const upper = command.toUpperCase();
      const read = () => { const value = next(); return relative ? value : value; };
      if (upper === "M" || upper === "L") { const px = read(); const py = next(); const nx = relative ? x + px : px; const ny = relative ? y + py : py; if (upper === "M") { if (polygon.length) polygons.push(polygon); polygon = []; startX = nx; startY = ny; } point(nx, ny); command = relative ? "l" : "L"; }
      else if (upper === "H") point(relative ? x + read() : read(), y);
      else if (upper === "V") point(x, relative ? y + read() : read());
      else if (upper === "C") { const a = read(); const b = next(); const c = next(); const d = next(); const e = next(); const f = next(); cubic(relative ? x + a : a, relative ? y + b : b, relative ? x + c : c, relative ? y + d : d, relative ? x + e : e, relative ? y + f : f); }
      else if (upper === "S") { const a = read(); const b = next(); const c = next(); const d = next(); const reflected = previous ? [2 * x - previous[0], 2 * y - previous[1]] : [x, y]; cubic(reflected[0], reflected[1], relative ? x + a : a, relative ? y + b : b, relative ? x + c : c, relative ? y + d : d); }
      else if (upper === "Z") { point(startX, startY); if (polygon.length) polygons.push(polygon); polygon = []; command = ""; }
      else throw new Error(`unsupported SVG path command: ${command}`);
    }
    if (polygon.length) polygons.push(polygon);
    return polygons;
  }).flat();
}

function contains(point, polygon) {
  let inside = false;
  for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) { const [xi, yi] = polygon[i]; const [xj, yj] = polygon[j]; if ((yi > point[1]) !== (yj > point[1]) && point[0] < ((xj - xi) * (point[1] - yi)) / (yj - yi) + xi) inside = !inside; }
  return inside;
}

function png(width, height, source, { tray = false, opaque = false } = {}) {
  const channels = opaque ? 3 : 4;
  const pixels = Buffer.alloc(width * height * channels);
  const fit = Math.min(width, height) / 512;
  const offsetX = (width - 512 * fit) / 2; const offsetY = (height - 512 * fit) / 2;
  const rect = parseRect(source);
  const blueColor = parseColor(source, "blue", blue); const whiteColor = parseColor(source, "white", white);
  const paint = (x, y, color) => {
    const offset = (y * width + x) * channels;
    for (let i = 0; i < channels; i += 1) pixels[offset + i] = color[i];
  };
  const insideRoundRect = (x, y) => {
    const localX = x - rect.x; const localY = y - rect.y;
    if (localX < 0 || localY < 0 || localX >= rect.width || localY >= rect.height) return false;
    const left = Math.min(localX, rect.width - localX); const top = Math.min(localY, rect.height - localY);
    if (left >= rect.rx || top >= rect.ry) return true;
    const dx = rect.rx - left; const dy = rect.ry - top;
    return (dx * dx) / (rect.rx * rect.rx) + (dy * dy) / (rect.ry * rect.ry) <= 1;
  };
  for (let y = 0; y < height; y += 1) for (let x = 0; x < width; x += 1) {
    let color = transparent;
    const sourcePoint = [(x + 0.5 - offsetX) / fit, (y + 0.5 - offsetY) / fit];
    const glyph = canonicalPolygons.reduce(
      (count, polygon) => count + (contains(sourcePoint, polygon) ? 1 : 0),
      0,
    ) % 2;
    if (tray) {
      if (glyph) color = whiteColor;
    } else {
      if (opaque || insideRoundRect(sourcePoint[0], sourcePoint[1])) color = blueColor;
      if (glyph) color = whiteColor;
    }
    paint(x, y, color);
  }
  const rowSize = width * channels;
  const rows = Buffer.alloc((rowSize + 1) * height);
  for (let y = 0; y < height; y += 1) { rows[y * (rowSize + 1)] = 0; pixels.copy(rows, y * (rowSize + 1) + 1, y * rowSize, (y + 1) * rowSize); }
  const header = Buffer.alloc(13); header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = opaque ? 2 : 6;
  return Buffer.concat([Buffer.from("\x89PNG\r\n\x1a\n", "binary"), chunk("IHDR", header), chunk("IDAT", deflateSync(rows, { level: 9 }),), chunk("IEND", Buffer.alloc(0))]);
}

function dimensions(value) { return Array.isArray(value) ? value : [value, value]; }
function ico(data, width, height) {
  const directory = Buffer.alloc(22);
  directory.writeUInt16LE(0, 0);
  directory.writeUInt16LE(1, 2);
  directory.writeUInt16LE(1, 4);
  directory[6] = width === 256 ? 0 : width;
  directory[7] = height === 256 ? 0 : height;
  directory[8] = 0;
  directory[9] = 0;
  directory.writeUInt16LE(1, 10);
  directory.writeUInt16LE(32, 12);
  directory.writeUInt32LE(data.length, 14);
  directory.writeUInt32LE(22, 18);
  return Buffer.concat([directory, data]);
}
function icns(data) {
  const chunk = Buffer.alloc(8);
  chunk.write("ic09", 0, "ascii");
  chunk.writeUInt32BE(data.length + 8, 4);
  const header = Buffer.alloc(8);
  header.write("icns", 0, "ascii");
  header.writeUInt32BE(data.length + 16, 4);
  return Buffer.concat([header, chunk, data]);
}
async function outputs() {
  const source = await readFile(sourcePath, "utf8");
  const sourceHash = createHash("sha256").update(source.replace(/\r\n?/gu, "\n")).digest("hex");
  const polygons = pathPolygons(source);
  canonicalPolygons = polygons;
  return { sourceHash, entries: [...files].map(([relativePath, value]) => {
    const options = typeof value === "object" ? value : {};
    const size = options.size ?? value;
    const [width, height] = dimensions(size);
    const raster = png(width, height, source, { tray: options.tray ?? relativePath.includes("tray"), opaque: options.opaque || relativePath.includes("AppIcon") });
    const data = options.format === "ico" ? ico(raster, width, height) : options.format === "icns" ? icns(raster) : raster;
    return { relativePath, size, format: options.format, data };
  }) };
}

const check = process.argv.includes("--check");
const { sourceHash, entries } = await outputs();
const generatedManifest = { source: "source/devhud-lettermark.svg", sourceSha256: sourceHash, generator: "scripts/generate-assets.mjs", assets: entries.map(({ relativePath, size, format }) => ({ path: relativePath, dimensions: dimensions(size), ...(format ? { format } : {}) })) };
generatedManifest.generatedFiles = [...generatedFiles].map(([path, contents]) => ({
  path,
  sha256: createHash("sha256").update(contents).digest("hex"),
}));
const previousManifest = await readFile(manifestPath, "utf8").then(JSON.parse).catch(() => null);
const expectedPaths = new Set(generatedManifest.assets.map(({ path }) => path));
const expectedGeneratedFiles = new Set(generatedManifest.generatedFiles.map(({ path }) => path));
const isWithin = (root, target) => {
  const relativePath = relative(root, target);
  return relativePath === "" || (relativePath !== ".." && !relativePath.startsWith(`..${sep}`) && !isAbsolute(relativePath));
};
if (check && previousManifest) {
  const manifestMatches = JSON.stringify(previousManifest.assets) === JSON.stringify(generatedManifest.assets);
  if (!manifestMatches) throw new Error("asset manifest does not match generator outputs");
  const generatedFilesMatch = JSON.stringify(previousManifest.generatedFiles ?? []) === JSON.stringify(generatedManifest.generatedFiles);
  if (!generatedFilesMatch) throw new Error("generated-file manifest does not match generator outputs");
}
if (!check && previousManifest) {
  for (const asset of previousManifest.assets ?? []) {
    if (typeof asset.path !== "string" || expectedPaths.has(asset.path)) continue;
    const obsoletePath = resolve(appRoot, asset.path);
    if (approvedGeneratedRoots.some((directory) => isWithin(directory, obsoletePath))) await unlink(obsoletePath).catch(() => {});
  }
  for (const file of previousManifest.generatedFiles ?? []) {
    if (typeof file.path !== "string" || expectedGeneratedFiles.has(file.path)) continue;
    const obsoletePath = resolve(appRoot, file.path);
    if (approvedGeneratedRoots.some((directory) => isWithin(directory, obsoletePath))) await unlink(obsoletePath).catch(() => {});
  }
}
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
for (const [relativePath, contents] of generatedFiles) {
  const destination = join(appRoot, relativePath);
  if (check) {
    const existing = await readFile(destination, "utf8").catch(() => null);
    if (existing !== contents) throw new Error(`generated file is stale or missing: ${relativePath}`);
  } else {
    await mkdir(dirname(destination), { recursive: true });
    await writeFile(destination, contents);
  }
}
if (!check) {
  await writeFile(manifestPath, `${JSON.stringify(generatedManifest, null, 2)}\n`);
}
console.log(JSON.stringify({ check: "devhud-assets", status: "passed", mode: check ? "verify" : "generated", sourceSha256: sourceHash, count: entries.length }));
