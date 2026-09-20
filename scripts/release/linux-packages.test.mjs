import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { identity, Channel, checksumFor, inspectElf, extractExecutable, dependencies, encode, sha256, safeKey, setupFiles } from './linux-packages/model.mjs';
import { archive } from './runmoor.mjs';
import { FileStore } from './linux-packages/store.mjs';
import { addToCatalog, saveCandidate, snapshotFor, promote } from './linux-packages/publish.mjs';
import { validateRotation, rotationOverlapMs } from './linux-packages/keyring.mjs';

const revision = 'a'.repeat(40);
const fingerprint = 'A'.repeat(40);
function fixture(project = 'binpm', version = '1.2.3') {
  const plan = identity({ project, version, revision });
  const files = ['amd64', 'arm64'].flatMap((architecture) => ['deb', 'rpm'].map((format) => {
    const name = format === 'deb' ? `${project}_${version}-1_${architecture}.deb` : `${project}-${version}-1.${architecture === 'amd64' ? 'x86_64' : 'aarch64'}.rpm`;
    const bytes = Buffer.from(name);
    return { name, architecture, format, sha256: sha256(bytes), size: bytes.length, bytes };
  }));
  return { files, record: { schema_version: 1, identity: plan, source: sha256(encode(plan)), fingerprint, files: files.map(({ bytes: _bytes, ...value }) => value) } };
}
function stores(t) {
  const directory = mkdtempSync(path.join(tmpdir(), 'delino-publication-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return { state: new FileStore(path.join(directory, 'state')), publicStore: new FileStore(path.join(directory, 'public')) };
}
const build = async (records, _load, generation) => {
  const bytes = encode(records.map((entry) => entry.identity));
  return [
    { key: `apt/pool/${generation}.deb`, bytes, sha256: sha256(bytes), mutable: false },
    { key: 'apt/dists/stable/InRelease', bytes, sha256: sha256(bytes), mutable: true },
    { key: 'rpm/stable/x86_64/mirrorlist', bytes, sha256: sha256(bytes), mutable: true },
  ];
};

test('project, version, revision and channels are closed', () => {
  assert.equal(identity({ project: 'runmoor', version: '1.0.0', revision }).channel, Channel.Stable);
  assert.equal(identity({ project: 'clibox', version: '1.0.0', revision }).channel, Channel.Stable);
  for (const project of ['ttl', 'devhud', '../binpm', 'binpm;true']) assert.throws(() => identity({ project, version: '1.0.0', revision }));
  for (const version of ['01.0.0', 'v1.0.0', '1.0.0-rc.1', '1.0\n0']) assert.throws(() => identity({ project: 'binpm', version, revision }));
  assert.throws(() => safeKey('../keys'));
  assert.throws(() => safeKey('/keys'));
  assert.throws(() => safeKey('objects//a'));
});
test('native setup isolates stable and preview with signature checks', () => {
  const files = setupFiles();
  assert.match(files['setup/delino.sources'].toString(), /Suites: stable\n/u);
  assert.doesNotMatch(files['setup/delino.sources'].toString(), /preview/u);
  assert.match(files['setup/delino-preview.repo'].toString(), /repo_gpgcheck=1\n/u);
  assert.match(files['setup/delino.repo'].toString(), /gpgcheck=1\n/u);
  assert.match(files['setup/delino.sources'].toString(), /Signed-By: \/usr\/share\/keyrings\//u);
});
test('checksums reject duplicates, missing entries and malformed digests', () => {
  const line = `${'a'.repeat(64)}  binpm.tar.gz\n`;
  assert.equal(checksumFor(line, 'binpm.tar.gz'), 'a'.repeat(64));
  assert.throws(() => checksumFor(line + line, 'binpm.tar.gz'));
  assert.throws(() => checksumFor(line, 'missing'));
  assert.throws(() => checksumFor('broken  binpm.tar.gz', 'binpm.tar.gz'));
});
test('archive reader rejects non-executable and corrupted payloads', () => {
  const entries = [{ name: 'runmoor', data: Buffer.from('ELF fixture'), mode: 0o755 }, { name: 'README.md', data: Buffer.from('readme'), mode: 0o644 }, { name: 'LICENSE', data: Buffer.from('license'), mode: 0o644 }];
  assert.equal(extractExecutable(archive(entries), 'runmoor').toString(), 'ELF fixture');
  assert.throws(() => extractExecutable(archive(entries), 'binpm'));
  assert.throws(() => extractExecutable(archive([{ ...entries[0], mode: 0o644 }, ...entries.slice(1)]), 'runmoor'));
  const corrupt = archive(entries); corrupt[corrupt.length - 10] ^= 255;
  assert.throws(() => extractExecutable(corrupt, 'runmoor'));
});
test('ELF checks reject wrong architecture, newer libc, runtime paths and CPU requirements', () => {
  const elf = Buffer.alloc(64); Buffer.from([127, 69, 76, 70, 2, 1]).copy(elf); elf.writeUInt16LE(62, 18);
  const dynamic = '(NEEDED) Shared library: [libc.so.6]\n(NEEDED) Shared library: [liblzma.so.5]';
  assert.deepEqual(inspectElf(elf, 'amd64', 'GLIBC_2.34 GLIBC_2.2.5', dynamic, ''), ['libc.so.6', 'liblzma.so.5']);
  assert.throws(() => inspectElf(elf, 'arm64', '', '', ''));
  assert.throws(() => inspectElf(elf, 'amd64', 'GLIBC_2.35', '', ''));
  assert.throws(() => inspectElf(elf, 'amd64', '', '(RPATH) /tmp/lib', ''));
  assert.throws(() => inspectElf(elf, 'amd64', '', '(NEEDED) [libssl.so.3]', ''));
  assert.throws(() => inspectElf(elf, 'amd64', '', '', 'x86 ISA needed: x86-64-v3'));
  assert.deepEqual(dependencies(['libc.so.6', 'liblzma.so.5'], 'deb'), ['libc6 (>= 2.34)', 'liblzma5']);
  for (const format of ['deb', 'rpm']) assert.ok(dependencies(['libc.so.6'], format, 'clibox').includes('ca-certificates'));
  assert.ok(!dependencies(['libc.so.6'], 'deb', 'binpm').includes('ca-certificates'));
});
test('candidate recovery preserves the first complete signed bytes', async (t) => {
  const { state } = stores(t); const first = fixture();
  await saveCandidate(state, first.record, first.files);
  const repeated = fixture(); repeated.record.files[0].sha256 = 'b'.repeat(64);
  assert.deepEqual(await saveCandidate(state, repeated.record, repeated.files), first.record);
  const changed = fixture(); changed.record.source = 'c'.repeat(64);
  await assert.rejects(saveCandidate(state, changed.record, changed.files), /PACKAGE_IDENTITY_CONFLICT/u);
});
test('interrupted promotion retries exact snapshot and retains other releases', async (t) => {
  const { state, publicStore } = stores(t); const first = fixture();
  await saveCandidate(state, first.record, first.files);
  const catalog = await addToCatalog(state, first.record);
  const snapshot = await snapshotFor(state, catalog, build);
  let count = 0;
  await assert.rejects(promote(state, { put: async (...args) => { if (++count === 3) throw new Error('interrupted'); await publicStore.put(...args); } }, snapshot), /interrupted/u);
  const resumed = await snapshotFor(state, catalog, () => { throw new Error('must not re-sign'); });
  assert.deepEqual(resumed, snapshot);
  await promote(state, publicStore, resumed);
  const next = fixture('nodeup', '2.0.0'); await saveCandidate(state, next.record, next.files);
  const updated = await addToCatalog(state, next.record);
  assert.equal(updated.records.length, 2);
  const newest = await snapshotFor(state, updated, build);
  await promote(state, publicStore, newest);
  assert.ok(await publicStore.get(`apt/pool/${catalog.generation}.deb`));
  await assert.rejects(promote(state, publicStore, snapshot), /STALE_PUBLICATION/u);
});
test('catalog compare-and-swap refuses overlapping writers', async (t) => {
  const { state } = stores(t); const first = fixture(); await saveCandidate(state, first.record, first.files);
  const conflict = { get: (...args) => state.get(...args), put: async (key, bytes, options) => {
    if (key === 'catalog.json') await state.put(key, encode({ schema_version: 1, candidates: [] }));
    return state.put(key, bytes, options);
  } };
  await assert.rejects(addToCatalog(conflict, first.record), /CONCURRENT_PUBLICATION/u);
});
test('public digest verification is part of publication success', async (t) => {
  const { state, publicStore } = stores(t); const first = fixture(); await saveCandidate(state, first.record, first.files);
  const catalog = await addToCatalog(state, first.record); const snapshot = await snapshotFor(state, catalog, build);
  await assert.rejects(promote(state, publicStore, snapshot, async () => { throw new Error('PUBLIC_READBACK_MISMATCH'); }), /PUBLIC_READBACK_MISMATCH/u);
  assert.equal(await state.get(`published/${catalog.generation}.json`), null);
});

test('keyring updates force a new snapshot and signer rotation requires a completed overlap', async (t) => {
  const { state, publicStore } = stores(t); const first = fixture();
  await saveCandidate(state, first.record, first.files);
  const initial = { version: 1, certificate: '1'.repeat(64), signer: 'A'.repeat(40), subkeys: ['A'.repeat(40)] };
  const one = await addToCatalog(state, first.record, initial);
  const expanded = { ...initial, version: 2, certificate: '2'.repeat(64), subkeys: [...initial.subkeys, 'B'.repeat(40)] };
  await assert.rejects(addToCatalog(state, first.record, { ...expanded, signer: 'B'.repeat(40) }), /SIGNER_NOT_STAGED/u);
  const two = await addToCatalog(state, first.record, expanded);
  assert.notEqual(one.generation, two.generation);
  assert.equal(two.records.length, 1, 'certificate updates must not repackage a release');
  const switched = { ...expanded, signer: 'B'.repeat(40) };
  await assert.rejects(addToCatalog(state, first.record, switched), /KEYRING_OVERLAP_REQUIRED/u);
  const snapshot = await snapshotFor(state, two, build);
  await assert.rejects(promote(state, publicStore, snapshot, () => { throw new Error('interrupted'); }));
  assert.equal(await state.get(`keyring-staged/${expanded.certificate}.json`), null);
  await promote(state, publicStore, snapshot);
  const receipt = JSON.parse((await state.get(`keyring-staged/${expanded.certificate}.json`)).body);
  await assert.rejects(validateRotation(state, expanded, switched, receipt.timestamp + rotationOverlapMs - 1), /KEYRING_OVERLAP_REQUIRED/u);
  await validateRotation(state, expanded, switched, receipt.timestamp + rotationOverlapMs);
  await assert.rejects(validateRotation(state, expanded, { ...expanded, certificate: '3'.repeat(64) }), /KEYRING_VERSION_REQUIRED/u);
  await assert.rejects(validateRotation(state, expanded, initial), /KEYRING_ROLLBACK/u);
  await assert.rejects(validateRotation(state, expanded, { ...expanded, subkeys: [expanded.signer] }), /KEYRING_REMOVAL_FORBIDDEN/u);
  await promote(state, publicStore, snapshot);
  assert.deepEqual(JSON.parse((await state.get(`keyring-staged/${expanded.certificate}.json`)).body), receipt);
});

test('release metadata rejects a wrong tag, commit, channel or missing signature', async () => {
  const { validateRelease } = await import('./linux-packages/model.mjs');
  const plan = identity({ project: 'binpm', version: '1.2.3', revision });
  const names = ['SHA256SUMS', 'SHA256SUMS.sigstore.json', ...['amd64', 'arm64'].flatMap((arch) => [`binpm-linux-${arch}.tar.gz`, `binpm-linux-${arch}.tar.gz.sigstore.json`])];
  const release = { tag_name: plan.tag, draft: false, prerelease: false, assets: names.map((name) => ({ name, size: 100 })) };
  assert.equal(validateRelease(release, plan, revision).length, 6);
  assert.throws(() => validateRelease({ ...release, tag_name: 'nodeup@v1.2.3' }, plan, revision));
  assert.throws(() => validateRelease(release, plan, 'b'.repeat(40)));
  assert.throws(() => validateRelease({ ...release, prerelease: true }, plan, revision));
  assert.throws(() => validateRelease({ ...release, assets: release.assets.slice(1) }, plan, revision));
  const runmoor = identity({ project: 'runmoor', version: '1.2.3', revision });
  const runmoorRelease = { ...release, tag_name: runmoor.tag, assets: release.assets.map((asset) => ({ ...asset, name: asset.name.replace('binpm', 'runmoor') })) };
  assert.equal(runmoor.channel, Channel.Stable);
  assert.equal(validateRelease(runmoorRelease, runmoor, revision).length, 6);
  assert.throws(() => validateRelease({ ...runmoorRelease, prerelease: true }, runmoor, revision));
});

test('signed recovery records reject tampering and an untrusted fingerprint', async (t) => {
  const { temporarySigningKey, signCandidate, verifyCandidate, signRecord, verifyRecord } = await import('./linux-packages/package.mjs');
  const directory = mkdtempSync(path.join(tmpdir(), 'dl-sig-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const signing = temporarySigningKey(path.join(directory, 'gnupg'));
  const { command } = await import('./linux-packages/release-input.mjs');
  const secretKeys = command('gpg', ['--batch', '--with-colons', '--list-secret-keys'], { env: signing.env });
  assert.equal(secretKeys.split('\n').find((line) => line.startsWith('sec:')).split(':')[14], '#', 'fixture publisher must not retain the certification secret key');
  assert.equal(secretKeys.split('\n').find((line) => line.startsWith('ssb:')).split(':')[14], '+', 'fixture publisher must retain the signing subkey');
  const { record } = fixture();
  const signed = signCandidate({ ...record, fingerprint: signing.fingerprint }, signing, directory);
  assert.equal(verifyCandidate(signed, signing, directory), signed);
  assert.throws(() => verifyCandidate({ ...signed, source: 'f'.repeat(64) }, signing, directory));
  assert.throws(() => verifyCandidate({ ...signed, fingerprint: 'B'.repeat(40) }, signing, directory));
  const snapshot = signRecord({ generation: 'a'.repeat(64), files: [] }, signing, directory);
  assert.equal(verifyRecord(snapshot, signing, directory), snapshot);
  assert.throws(() => verifyRecord({ ...snapshot, generation: 'b'.repeat(64) }, signing, directory));
});

test('signing-key import rejects a stale public certificate with the same primary fingerprint', async (t) => {
  const { importSigningKey } = await import('./linux-packages/package.mjs');
  const { command } = await import('./linux-packages/release-input.mjs');
  const directory = mkdtempSync(path.join(tmpdir(), 'dl-rotate-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const primary = path.join(directory, 'primary');
  mkdirSync(primary, { mode: 0o700 });
  const env = { ...process.env, GNUPGHOME: primary };
  const passphrase = 'temporary-rotation-fixture';
  const passFile = path.join(primary, 'passphrase');
  writeFileSync(passFile, passphrase, { mode: 0o600 });
  const unlock = ['--pinentry-mode', 'loopback', '--passphrase-file', passFile];
  command('gpg', ['--batch', ...unlock, '--quick-generate-key', 'Rotation Fixture', 'rsa2048', 'cert', '1d'], { env });
  const fingerprint = command('gpg', ['--batch', '--with-colons', '--list-secret-keys'], { env }).split('\n').find((line) => line.startsWith('fpr:')).split(':')[9];
  const stale = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
  command('gpg', ['--batch', ...unlock, '--quick-add-key', fingerprint, 'rsa2048', 'sign', '1d'], { env });
  const secretKey = command('gpg', ['--batch', ...unlock, '--armor', '--export-secret-subkeys', fingerprint], { env });
  const publicKey = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
  assert.throws(() => importSigningKey(path.join(directory, 'stale'), { secretKey, passphrase, publicKey: stale, fingerprint }), /SIGNING_CERTIFICATE_MISMATCH/u);
  assert.doesNotThrow(() => importSigningKey(path.join(directory, 'current'), { secretKey, passphrase, publicKey, fingerprint }));
});
