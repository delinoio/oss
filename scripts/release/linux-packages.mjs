import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { identity, Mode, requireValue, log, origin, encode, sha256 } from './linux-packages/model.mjs';
import { downloadRelease } from './linux-packages/release-input.mjs';
import { importSigningKey, temporarySigningKey, packageFiles, candidateRecord, signCandidate, verifyCandidate, signRecord, verifyRecord } from './linux-packages/package.mjs';
import { buildRepositories } from './linux-packages/repository.mjs';
import { R2Store } from './linux-packages/store.mjs';
import { saveCandidate, loadCandidate, addToCatalog, snapshotFor, promote } from './linux-packages/publish.mjs';
import { prepareKeyring } from './linux-packages/keyring.mjs';

export async function main(args = process.argv.slice(2)) {
  const { values } = parseArgs({ args, options: { project: { type: 'string' }, version: { type: 'string' }, revision: { type: 'string' }, mode: { type: 'string', default: Mode.DryRun }, output: { type: 'string' } } });
  const plan = identity(values);
  const production = values.mode === Mode.Publish;
  if (production) {
    requireValue(process.env.GITHUB_REPOSITORY === 'delinoio/oss' && (process.env.GITHUB_REF === 'refs/heads/main' || process.env.GITHUB_REF === `refs/tags/${plan.tag}`) && process.env.GITHUB_ACTIONS === 'true', 'UNAUTHORIZED_PUBLICATION_CONTEXT');
    requireValue(process.env.GITHUB_EVENT_NAME === 'push' || process.env.GITHUB_EVENT_NAME === 'workflow_dispatch', 'UNAUTHORIZED_PUBLICATION_EVENT');
  }
  const work = mkdtempSync(path.join(tmpdir(), 'delino-packages-'));
  let state;
  let phase = 'verify-input';
  const event = async (next, code = null) => {
    phase = next;
    log(phase, { ...plan, ...(code ? { code } : {}) });
    if (state) {
      const record = { schema_version: 1, ...plan, phase, code, timestamp: new Date().toISOString() };
      await state.put(`history/${plan.project}/${plan.version}/${sha256(encode(record))}.json`, encode(record), { immutable: true });
    }
  };
  try {
    log('verify-input', plan);
    const input = downloadRelease(plan, path.join(work, 'input'));
    const signing = production ? importSigningKey(path.join(work, 'gnupg'), {
      secretKey: Buffer.from(process.env.LINUX_PACKAGES_SIGNING_SUBKEY ?? '', 'base64'),
      passphrase: process.env.LINUX_PACKAGES_SIGNING_PASSPHRASE ?? '',
      fingerprint: process.env.LINUX_PACKAGES_SIGNING_FINGERPRINT,
      publicKey: readFileSync('packaging/linux/public-key.asc'),
    }) : temporarySigningKey(path.join(work, 'gnupg'));
    if (!production) {
      requireValue(values.output, 'OUTPUT_REQUIRED');
      const output = path.resolve(values.output);
      mkdirSync(output, { recursive: true });
      const packages = packageFiles(plan, input, path.join(work, 'packages'), signing);
      const record = candidateRecord(plan, input, packages, signing);
      const files = await buildRepositories([record], async (file) => packages.find((entry) => entry.name === file.name).bytes, path.join(work, 'repository'), signing, sha256(encode(record)));
      for (const file of files) { const name = path.join(output, file.key); mkdirSync(path.dirname(name), { recursive: true }); writeFileSync(name, file.bytes); }
      writeFileSync(path.join(output, 'fixture.json'), encode({ identity: plan, fingerprint: signing.fingerprint, generation: sha256(encode(record)) }));
      // An older package version is fixture-only, never included in a repository index.
      const previous = packageFiles({ ...plan, version: '0.0.0' }, input, path.join(work, 'previous'), signing);
      mkdirSync(path.join(output, 'previous'));
      for (const file of previous) writeFileSync(path.join(output, 'previous', file.name), file.bytes);
      log('dry-run-complete', plan);
      return;
    }
    state = await R2Store.create('state');
    await event('input-verified');
    const publicStore = await R2Store.create('public');
    let record = await loadCandidate(state, plan, input.source);
    if (!record) {
      await event('package');
      const packages = packageFiles(plan, input, path.join(work, 'packages'), signing);
      record = await saveCandidate(state, signCandidate(candidateRecord(plan, input, packages, signing), signing, work), packages);
    }
    verifyCandidate(record, signing, work);
    await event('catalog');
    const keyring = await prepareKeyring(state, signing, work, JSON.parse(readFileSync('packaging/linux/signing.json')).keyring_version);
    const catalog = await addToCatalog(state, record, keyring.policy);
    for (const candidate of catalog.records) verifyCandidate(candidate, signing, work);
    await event('snapshot');
    const snapshot = await snapshotFor(state, catalog, (records, load, generation) => buildRepositories(records, load, path.join(work, 'repository'), signing, generation, keyring), (value) => signRecord(value, signing, work));
    verifyRecord(snapshot, signing, work);
    await event('promote');
    await promote(state, publicStore, snapshot, async (key, expected) => {
      const response = await fetch(`${origin}/${key}`, { redirect: 'error', signal: AbortSignal.timeout(120000), cache: 'no-store' });
      requireValue(response.ok && Buffer.from(await response.arrayBuffer()).equals(expected), 'PUBLIC_READBACK_MISMATCH');
    });
    await event('complete');
  } catch (error) {
    const code = /^[A-Z0-9_]+$/u.test(error.message) ? error.message : 'PACKAGE_OPERATION_FAILED';
    try { await event(`${phase}-failed`, code); } catch { log('history-write-failed', { ...plan, code }); }
    throw error;
  } finally { rmSync(work, { recursive: true, force: true }); }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { log('failed', { code: /^[A-Z0-9_]+$/u.test(error.message) ? error.message : 'PACKAGE_OPERATION_FAILED' }); process.exitCode = 1; });
}
