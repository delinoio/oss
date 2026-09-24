import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { encode, sha256, requireValue } from './model.mjs';
import { command } from './release-input.mjs';
import { signRecord, verifyRecord } from './package.mjs';

export const keyringName = 'delino-archive-keyring';
export const rotationOverlapMs = 30 * 24 * 60 * 60 * 1000;

export function keyringPolicy(signing, version) {
  requireValue(Number.isSafeInteger(version) && version > 0, 'INVALID_KEYRING_VERSION');
  return { version, certificate: sha256(signing.publicKey), signer: signing.signer, subkeys: signing.publicSubkeys };
}

export function packageKeyring(signing, directory, version = 1) {
  const policy = keyringPolicy(signing, version);
  mkdirSync(directory, { recursive: true });
  const certificate = path.join(directory, 'delino-packages.gpg');
  command('gpg', ['--batch', '--yes', '--dearmor', '--output', certificate, path.join(signing.directory, 'public.asc')], { env: signing.verifyEnv });
  const config = path.join(directory, 'keyring.json');
  // A version identifies one certificate forever. Fixed timestamps also make a
  // rebuild deterministic before the first complete candidate is persisted.
  writeFileSync(config, encode({ name: keyringName, arch: 'all', platform: 'linux', version: `${version}`, version_schema: 'none', release: '1',
    section: 'misc', priority: 'optional', maintainer: 'Delino', license: 'Apache-2.0',
    description: 'Delino APT repository public certificate', mtime: '2026-09-20T00:00:00Z',
    contents: [
      { src: certificate, dst: '/usr/share/keyrings/delino-packages.gpg', file_info: { mode: 0o644 } },
      { src: path.resolve('packaging/linux/licenses/Apache-2.0.txt'), dst: `/usr/share/doc/${keyringName}/copyright`, file_info: { mode: 0o644 } },
    ], deb: { compression: 'xz' },
  }));
  const name = `${keyringName}_${version}-1_all.deb`;
  const target = path.join(directory, name);
  command('nfpm', ['package', '--config', config, '--packager', 'deb', '--target', target]);
  const bytes = readFileSync(target);
  return { policy, file: { name, architecture: 'all', format: 'deb', sha256: sha256(bytes), size: bytes.length }, bytes };
}

export async function prepareKeyring(state, signing, directory, version) {
  const policy = keyringPolicy(signing, version);
  const key = `keyrings/${version}.json`;
  const existing = await state.get(key);
  if (existing) {
    const record = verifyRecord(JSON.parse(existing.body), signing, directory);
    requireValue(record.version === version && record.certificate === policy.certificate && record.file.name === `${keyringName}_${version}-1_all.deb`, 'KEYRING_IDENTITY_CONFLICT');
    const stored = await state.get(`objects/${record.file.sha256}`);
    requireValue(stored && sha256(stored.body) === record.file.sha256 && stored.body.length === record.file.size, 'KEYRING_STORAGE_CORRUPT');
    return { policy, file: record.file, bytes: stored.body };
  }
  const packaged = packageKeyring(signing, path.join(directory, 'keyring'), version);
  const record = signRecord({ schema_version: 1, version, certificate: policy.certificate, file: packaged.file }, signing, directory);
  await state.put(`objects/${packaged.file.sha256}`, packaged.bytes, { immutable: true });
  await state.put(key, encode(record), { immutable: true });
  return packaged;
}

export async function validateRotation(state, previous, next, now = Date.now()) {
  if (!previous) return;
  requireValue(next.version >= previous.version, 'KEYRING_ROLLBACK');
  requireValue(next.version !== previous.version || next.certificate === previous.certificate, 'KEYRING_VERSION_REQUIRED');
  requireValue(previous.subkeys.every((key) => next.subkeys.includes(key)), 'KEYRING_REMOVAL_FORBIDDEN');
  if (next.signer === previous.signer) return;
  // First publish the expanded certificate with the old signer. A later retry
  // can switch only after that certificate has been publicly available for the
  // overlap period, including to preview-only clients. Never switch in one step.
  requireValue(next.certificate === previous.certificate && previous.subkeys.includes(next.signer), 'SIGNER_NOT_STAGED');
  const receipt = await state.get(`keyring-staged/${previous.certificate}.json`);
  const staged = receipt && JSON.parse(receipt.body);
  requireValue(staged?.signer === previous.signer && Number.isSafeInteger(staged?.timestamp) && now - staged.timestamp >= rotationOverlapMs, 'KEYRING_OVERLAP_REQUIRED');
}
