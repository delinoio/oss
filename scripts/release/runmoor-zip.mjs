import { crc32 } from "node:zlib";

const names = ["runmoor.exe", "README.md", "LICENSE"];

// Store fixed entries with a fixed DOS timestamp. No filesystem metadata or
// external ZIP implementation influences the signed release bytes.
export function windowsArchive(entries) {
  if (
    JSON.stringify(entries.map((entry) => entry.name)) !== JSON.stringify(names)
  )
    throw new Error("Unexpected Windows archive inventory");
  const local = [];
  const central = [];
  let offset = 0;
  for (const { name, data } of entries) {
    const filename = Buffer.from(name);
    const header = Buffer.alloc(30);
    header.writeUInt32LE(0x04034b50);
    header.writeUInt16LE(20, 4);
    header.writeUInt16LE(0x21, 12);
    header.writeUInt32LE(crc32(data), 14);
    header.writeUInt32LE(data.length, 18);
    header.writeUInt32LE(data.length, 22);
    header.writeUInt16LE(filename.length, 26);
    const directory = Buffer.alloc(46);
    directory.writeUInt32LE(0x02014b50);
    directory.writeUInt16LE(20, 4);
    directory.writeUInt16LE(20, 6);
    directory.writeUInt16LE(0x21, 14);
    directory.writeUInt32LE(crc32(data), 16);
    directory.writeUInt32LE(data.length, 20);
    directory.writeUInt32LE(data.length, 24);
    directory.writeUInt16LE(filename.length, 28);
    directory.writeUInt32LE(offset, 42);
    local.push(header, filename, data);
    central.push(directory, filename);
    offset += header.length + filename.length + data.length;
  }
  const directory = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...local, directory, end]);
}

export function inspectWindowsArchive(bytes) {
  let offset = 0;
  const entries = [];
  for (const name of names) {
    if (offset + 30 > bytes.length || bytes.readUInt32LE(offset) !== 0x04034b50)
      throw new Error("Invalid Windows archive");
    const length = bytes.readUInt32LE(offset + 18);
    const nameLength = bytes.readUInt16LE(offset + 26);
    const start = offset + 30 + nameLength;
    if (
      length === 0 ||
      start + length > bytes.length ||
      bytes.subarray(offset + 30, start).toString() !== name
    )
      throw new Error("Invalid Windows archive payload");
    entries.push({ name, data: bytes.subarray(start, start + length) });
    offset = start + length;
  }
  if (!windowsArchive(entries).equals(bytes))
    throw new Error("Windows archive metadata or checksum mismatch");
  return names;
}
