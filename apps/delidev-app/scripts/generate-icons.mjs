import { deflateSync } from "node:zlib";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outputDirectory = join(appRoot, "public", "icons");

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(name, data) {
  const type = Buffer.from(name);
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([type, data])));
  return Buffer.concat([length, type, data, checksum]);
}

function roundedRectangle(x, y, size, inset, radius) {
  const left = inset;
  const top = inset;
  const right = size - inset - 1;
  const bottom = size - inset - 1;
  const innerX = Math.max(left + radius, Math.min(x, right - radius));
  const innerY = Math.max(top + radius, Math.min(y, bottom - radius));
  return (
    x >= left &&
    x <= right &&
    y >= top &&
    y <= bottom &&
    (x - innerX) ** 2 + (y - innerY) ** 2 <= radius ** 2
  );
}

function isLetterD(x, y, size) {
  const unitX = x / size;
  const unitY = y / size;
  const stem = unitX >= 0.285 && unitX <= 0.435 && unitY >= 0.22 && unitY <= 0.78;
  const outer =
    ((unitX - 0.43) / 0.35) ** 2 + ((unitY - 0.5) / 0.28) ** 2 <= 1 &&
    unitX >= 0.36;
  const inner =
    ((unitX - 0.45) / 0.18) ** 2 + ((unitY - 0.5) / 0.15) ** 2 < 1 &&
    unitX >= 0.42;
  return stem || (outer && !inner);
}

function createIcon(size, maskable = false) {
  const bytesPerRow = size * 4 + 1;
  const raw = Buffer.alloc(bytesPerRow * size);
  const inset = maskable ? Math.round(size * 0.1) : 0;
  const radius = Math.round(size * (maskable ? 0.22 : 0.235));

  for (let y = 0; y < size; y += 1) {
    const row = y * bytesPerRow;
    raw[row] = 0;
    for (let x = 0; x < size; x += 1) {
      const offset = row + 1 + x * 4;
      const inBackground = roundedRectangle(x, y, size, inset, radius);
      const normalizedX = maskable
        ? ((x - inset) / (size - inset * 2)) * size
        : x;
      const normalizedY = maskable
        ? ((y - inset) / (size - inset * 2)) * size
        : y;
      const inLetter = inBackground && isLetterD(normalizedX, normalizedY, size);
      const color = inLetter
        ? [255, 255, 255, 255]
        : inBackground
          ? [27, 100, 218, 255]
          : [255, 255, 255, 0];
      raw.set(color, offset);
    }
  }

  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8;
  header[9] = 6;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

await mkdir(outputDirectory, { recursive: true });
await Promise.all([
  writeFile(join(outputDirectory, "delidev-192.png"), createIcon(192)),
  writeFile(join(outputDirectory, "delidev-512.png"), createIcon(512)),
  writeFile(
    join(outputDirectory, "delidev-maskable-512.png"),
    createIcon(512, true),
  ),
]);
