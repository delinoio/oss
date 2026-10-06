import { gzipSync, gunzipSync } from "node:zlib";

const blockSize = 512;
const maximumBytes = 512 * 1024 * 1024;

function safePath(name) {
  if (!/^[a-zA-Z0-9_./-]+$/.test(name) || name.startsWith("/") || name.split("/").some(part => !part || part === "." || part === "..")) {
    throw new Error("Archive contains an unsafe path.");
  }
  return name;
}

function checksum(header) {
  let sum = 0;
  for (let index = 0; index < blockSize; index += 1) sum += index >= 148 && index < 156 ? 32 : header[index];
  return sum;
}

function octal(header, offset, length, value) {
  const number = value.toString(8);
  if (number.length >= length) throw new Error("Archive field overflow.");
  header.write(number.padStart(length - 1, "0") + "\0", offset, length, "ascii");
}

export function packArchive(entries) {
  const names = new Set();
  const blocks = [];
  for (const entry of entries) {
    const name = safePath(entry.name);
    if (name.length > 100 || names.has(name)) throw new Error("Archive contains a duplicate or long path.");
    names.add(name);
    const data = Buffer.from(entry.data);
    if (data.length > maximumBytes) throw new Error("Archive entry is too large.");
    const header = Buffer.alloc(blockSize);
    header.write(name, 0, 100, "ascii");
    octal(header, 100, 8, entry.mode ?? 0o644);
    octal(header, 108, 8, 0);
    octal(header, 116, 8, 0);
    octal(header, 124, 12, data.length);
    octal(header, 136, 12, 0);
    header[156] = 48;
    header.write("ustar\0", 257, 6, "ascii");
    header.write("00", 263, 2, "ascii");
    octal(header, 148, 8, checksum(header));
    blocks.push(header, data, Buffer.alloc((blockSize - data.length % blockSize) % blockSize));
  }
  blocks.push(Buffer.alloc(blockSize * 2));
  return gzipSync(Buffer.concat(blocks), { level: 9 });
}

export function unpackArchive(bytes) {
  const tar = gunzipSync(bytes, { maxOutputLength: maximumBytes });
  if (tar.length % blockSize !== 0) throw new Error("Archive has a truncated block.");
  const entries = [];
  const names = new Set();
  let offset = 0;
  while (offset < tar.length) {
    const header = tar.subarray(offset, offset + blockSize);
    if (header.every(byte => byte === 0)) {
      if (tar.length - offset < blockSize * 2 || !tar.subarray(offset).every(byte => byte === 0)) throw new Error("Archive has invalid trailing data.");
      return entries;
    }
    const field = (start, length) => header.subarray(start, start + length).toString("ascii").replace(/\0.*$/s, "").trim();
    const number = (start, length) => {
      const value = field(start, length);
      if (!/^[0-7]+$/.test(value)) throw new Error("Archive has an invalid numeric field.");
      return Number.parseInt(value, 8);
    };
    const name = safePath(field(0, 100));
    if (names.has(name) || field(257, 6) !== "ustar" || field(345, 155) || ![0, 48].includes(header[156]) || field(157, 100)) throw new Error("Archive contains an unsupported entry.");
    if (number(148, 8) !== checksum(header)) throw new Error("Archive header checksum mismatch.");
    names.add(name);
    const size = number(124, 12);
    const mode = number(100, 8);
    if (size > maximumBytes || mode & 0o7000 || offset + blockSize + size > tar.length) throw new Error("Archive contains an invalid entry size or mode.");
    const data = tar.subarray(offset + blockSize, offset + blockSize + size);
    entries.push({ name, data, mode });
    offset += blockSize + Math.ceil(size / blockSize) * blockSize;
  }
  throw new Error("Archive is missing its end marker.");
}
