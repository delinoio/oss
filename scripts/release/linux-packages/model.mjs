import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';

export const Project = Object.freeze({ Binpm: 'binpm', CargoMono: 'cargo-mono', Nodeup: 'nodeup', WithWatch: 'with-watch', Derun: 'derun', Runmoor: 'runmoor' });
export const Channel = Object.freeze({ Stable: 'stable', Preview: 'preview' });
export const Architecture = Object.freeze({ Amd64: 'amd64', Arm64: 'arm64' });
export const Mode = Object.freeze({ DryRun: 'dry-run', Publish: 'publish' });
export const pins = JSON.parse(readFileSync(new URL('../../../packaging/linux/pins.json', import.meta.url), 'utf8'));
export const origin = pins.origin;
export const architectures = Object.values(Architecture);
export const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
export const encode = (value) => Buffer.from(`${JSON.stringify(value)}\n`);
export function requireValue(condition, code) { if (!condition) throw new Error(code); }
export function safeKey(value) {
  requireValue(typeof value === 'string' && value.length <= 1024 && /^[a-zA-Z0-9/_.+@=-]+$/u.test(value) && !value.startsWith('/') && !value.split('/').some((part) => !part || part === '.' || part === '..'), 'INVALID_OBJECT_KEY');
  return value;
}
export function identity({ project, version, revision, mode = Mode.DryRun }) {
  requireValue(Object.values(Project).includes(project), 'INVALID_PROJECT');
  requireValue(typeof version === 'string' && version.length <= 62 && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(version), 'INVALID_VERSION');
  requireValue(/^[a-f0-9]{40}$/u.test(revision ?? ''), 'INVALID_REVISION');
  requireValue(Object.values(Mode).includes(mode), 'INVALID_MODE');
  return { project, version, revision, tag: `${project}@v${version}`, channel: project === Project.Runmoor ? Channel.Preview : Channel.Stable, package_revision: 1 };
}
export function validateIdentity(value) {
  const result = identity(value);
  requireValue(JSON.stringify(result) === JSON.stringify(value), 'INVALID_IDENTITY');
  return result;
}
export function validateRelease(release, plan, resolvedRevision) {
  requireValue(resolvedRevision === plan.revision && release.tag_name === plan.tag && release.draft === false && release.prerelease === (plan.channel === Channel.Preview), 'RELEASE_IDENTITY_MISMATCH');
  const expected = ['SHA256SUMS', 'SHA256SUMS.sigstore.json', ...architectures.flatMap((arch) => [`${plan.project}-linux-${arch}.tar.gz`, `${plan.project}-linux-${arch}.tar.gz.sigstore.json`])];
  for (const name of expected) requireValue(release.assets.filter((asset) => asset.name === name && asset.size > 0).length === 1, 'RELEASE_ASSET_MISSING');
  return expected;
}
export function checksumFor(manifest, name) {
  const entries = manifest.trimEnd().split('\n').map((line) => {
    const match = line.match(/^([a-f0-9]{64}) [ *]([^\r\n]+)$/u);
    requireValue(match, 'INVALID_CHECKSUM_MANIFEST');
    return { hash: match[1], name: match[2] };
  });
  requireValue(new Set(entries.map((entry) => entry.name)).size === entries.length, 'DUPLICATE_CHECKSUM');
  const entry = entries.find((entry) => entry.name === name);
  requireValue(entry, 'MISSING_CHECKSUM');
  return entry.hash;
}
export function extractExecutable(bytes, project) {
  // Decode only regular ustar entries into memory. Never extract archive-controlled paths.
  const tar = gunzipSync(bytes, { maxOutputLength: 256 * 1024 * 1024 });
  const allowed = project === Project.Runmoor ? [project, 'README.md', 'LICENSE'] : [project];
  const entries = new Map();
  let offset = 0;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const string = (start, end) => header.subarray(start, end).toString().split('\0')[0];
    const name = string(0, 100);
    const size = Number.parseInt(string(124, 136).trim(), 8);
    const mode = Number.parseInt(string(100, 108).trim(), 8);
    const storedSum = Number.parseInt(string(148, 156).trim(), 8);
    const sum = header.reduce((total, byte, i) => total + (i >= 148 && i < 156 ? 32 : byte), 0);
    requireValue(sum === storedSum && allowed.includes(name) && !entries.has(name) && !string(345, 500) && (header[156] === 0 || header[156] === 48), 'INVALID_ARCHIVE_ENTRY');
    requireValue(Number.isSafeInteger(size) && size > 0 && offset + 512 + size <= tar.length && (name !== project || (mode & 0o111) !== 0), 'INVALID_ARCHIVE_PAYLOAD');
    entries.set(name, tar.subarray(offset + 512, offset + 512 + size));
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  requireValue(entries.has(project) && offset + 1024 <= tar.length && tar.subarray(offset).every((byte) => byte === 0), 'INVALID_ARCHIVE_END');
  return entries.get(project);
}
export function inspectElf(bytes, architecture, versionText, dynamicText, notesText) {
  requireValue(architectures.includes(architecture) && bytes.subarray(0, 4).equals(Buffer.from([127, 69, 76, 70])) && bytes[4] === 2 && bytes[5] === 1, 'INVALID_ELF');
  requireValue(bytes.readUInt16LE(18) === (architecture === Architecture.Amd64 ? 62 : 183), 'WRONG_ELF_ARCHITECTURE');
  const versions = [...versionText.matchAll(/\bGLIBC_(\d+)\.(\d+)(?:\.(\d+))?\b/gu)];
  requireValue(versions.every(([, major, minor, patch]) => Number(major) < 2 || (Number(major) === 2 && (Number(minor) < 34 || (Number(minor) === 34 && Number(patch ?? 0) === 0)))), 'GLIBC_BASELINE_EXCEEDED');
  requireValue(!/x86-64-v[234]|x86 ISA needed:.*(?:AVX|SSE4)/iu.test(notesText), 'CPU_BASELINE_EXCEEDED');
  const libraries = [...dynamicText.matchAll(/\(NEEDED\).*\[([^\]]+)\]/gu)].map((match) => match[1]).sort();
  const allowed = ['libc.so.6', 'libm.so.6', 'libgcc_s.so.1', 'libpthread.so.0', 'libdl.so.2', 'librt.so.1', 'liblzma.so.5', 'ld-linux-x86-64.so.2', 'ld-linux-aarch64.so.1'];
  requireValue(libraries.every((name) => allowed.includes(name)), 'UNMAPPED_RUNTIME_LIBRARY');
  requireValue(!/\((?:RPATH|RUNPATH)\)/u.test(dynamicText), 'UNEXPECTED_RUNTIME_PATH');
  return libraries;
}
export function dependencies(libraries, format) {
  const values = new Set();
  for (const library of libraries) {
    if (library === 'libgcc_s.so.1') values.add(format === 'deb' ? 'libgcc-s1' : 'libgcc');
    else if (library === 'liblzma.so.5') values.add(format === 'deb' ? 'liblzma5' : 'xz-libs');
    else values.add(format === 'deb' ? 'libc6 (>= 2.34)' : 'glibc >= 2.34');
  }
  return [...values].sort();
}
export function sourceIdentity(archiveHashes, plan) {
  requireValue(architectures.every((arch) => /^[a-f0-9]{64}$/u.test(archiveHashes[arch] ?? '')), 'INVALID_SOURCE_DIGEST');
  return sha256(encode({ identity: plan, archives: archiveHashes }));
}
export function setupFiles() {
  const files = {};
  for (const channel of Object.values(Channel)) {
    const name = channel === Channel.Stable ? 'delino' : 'delino-preview';
    files[`setup/${name}.sources`] = Buffer.from(`Types: deb\nURIs: ${origin}/apt\nSuites: ${channel}\nComponents: main\nArchitectures: amd64 arm64\nSigned-By: /etc/apt/keyrings/delino-packages.gpg\n`);
    files[`setup/${name}.repo`] = Buffer.from(`[${name}]\nname=Delino ${channel}\nmirrorlist=${origin}/rpm/${channel}/$basearch/mirrorlist\nenabled=1\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=${origin}/keys/delino-packages.asc\nmetadata_expire=300\nsslverify=1\n`);
  }
  return files;
}
export function log(phase, fields = {}) { console.error(JSON.stringify({ component: 'release.linux-packages', phase, ...fields })); }
