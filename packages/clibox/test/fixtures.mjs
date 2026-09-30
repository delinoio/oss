export function machO(arch) {
  const bytes = Buffer.alloc(64);
  bytes.writeUInt32LE(0xfeedfacf, 0);
  bytes.writeUInt32LE(arch === 'amd64' ? 0x1000007 : 0x100000c, 4);
  bytes.writeUInt32LE(2, 12);
  return bytes;
}
