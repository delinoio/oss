// Synthetic headers for unit tests only; these are not executable artifacts.
export function nativeHeader(target) {
  if (target === "macos-arm64") {
    const bytes = Buffer.alloc(32);
    bytes.writeUInt32LE(0xfeedfacf, 0);
    bytes.writeUInt32LE(0x0100000c, 4);
    return bytes;
  }
  if (target === "linux-x64") {
    const bytes = Buffer.alloc(64);
    bytes.set([0x7f, 0x45, 0x4c, 0x46, 2, 1, 1]);
    bytes.writeUInt16LE(62, 18);
    return bytes;
  }
  const bytes = Buffer.alloc(154);
  bytes.writeUInt16LE(0x5a4d, 0);
  bytes.writeUInt32LE(128, 0x3c);
  bytes.writeUInt32LE(0x4550, 128);
  bytes.writeUInt16LE(0x8664, 132);
  bytes.writeUInt16LE(0x20b, 152);
  return bytes;
}
