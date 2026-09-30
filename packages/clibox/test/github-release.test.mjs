import assert from 'node:assert/strict';
import test from 'node:test';
import { gzipSync, gunzipSync } from 'node:zlib';
import { archive, archiveNames, darwinNotices, publish, readDarwinArchive } from '../scripts/github-release.mjs';
import { machO } from './fixtures.mjs';
import { extractExecutable, identity } from '../../../scripts/release/linux-packages/model.mjs';

const plan = identity({ project: 'clibox', version: '1.2.3', revision: 'a'.repeat(40) });
const files = new Map([...archiveNames, 'SHA256SUMS'].map((name) => [name, Buffer.from(name)]));
function fixture() {
  const state = { release: null, writes: 0, signed: 0, tag: plan.revision, rejectSignature: false, interrupt: false };
  const adapters = {
    api: async (method, endpoint, body) => {
      if (endpoint.includes('/git/ref/')) return { object: { type: 'commit', sha: state.tag } };
      if (method === 'POST') { state.writes++; state.release = { id: 1, ...body, assets: [] }; }
      if (method === 'PATCH') { state.writes++; Object.assign(state.release, body); }
      return state.release;
    },
    download: async (asset) => asset.bytes,
    upload: async (release, name, bytes) => { state.writes++; release.assets.push({ name, bytes }); if (state.interrupt) { state.interrupt = false; throw new Error('interrupted'); } },
    sign: async (name, bytes) => { state.signed++; return Buffer.from(`signature:${name}:${bytes}`); },
    verify: async (name, bytes, bundle) => { assert.equal(state.rejectSignature, false); assert.equal(bundle.toString(), `signature:${name}:${bytes}`); },
    report: () => {},
  };
  return { state, adapters };
}
test('GNU release archive is deterministic and contains exactly the original executable', () => {
  const bytes = Buffer.from('same npm native bytes');
  assert.deepEqual(archive(bytes), archive(bytes));
  assert.deepEqual(extractExecutable(archive(bytes), 'clibox'), bytes);
});
test('publication completes a signed draft and reuses a complete public release without writes', async () => {
  const { state, adapters } = fixture();
  await publish({ plan, files }, adapters);
  assert.equal(state.release.draft, false); assert.equal(state.release.assets.length, 10); assert.equal(state.signed, 5);
  const writes = state.writes;
  await publish({ plan, files }, adapters);
  assert.equal(state.writes, writes); assert.equal(state.signed, 5);
});
test('interrupted upload resumes only missing assets and preserves completed signatures', async () => {
  const { state, adapters } = fixture();
  const upload = adapters.upload; let count = 0;
  adapters.upload = async (...args) => { await upload(...args); if (++count === 3) throw new Error('interrupted'); };
  await assert.rejects(publish({ plan, files }, adapters), /interrupted/u);
  const firstSignature = state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes;
  adapters.upload = upload;
  await publish({ plan, files }, adapters);
  assert.equal(state.release.draft, false); assert.equal(state.signed, 5);
  assert.equal(state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes, firstSignature);
});
test('Darwin archives retain executable bytes and original license notices reproducibly', () => {
  for (const arch of ['amd64', 'arm64']) {
    const binary = machO(arch);
    const notices = darwinNotices();
    const bytes = archive(binary, notices);
    assert.deepEqual(bytes, archive(binary, notices));
    const entries = readDarwinArchive(bytes, arch);
    assert.deepEqual(entries.get('clibox'), binary);
    for (const [name, value] of notices) assert.deepEqual(entries.get(name), value);
    assert.throws(() => readDarwinArchive(bytes, arch === 'amd64' ? 'arm64' : 'amd64'), /architecture/u);
    assert.throws(() => readDarwinArchive(archive(binary), arch), /Incomplete/u);
    notices.set('LICENSE.fspy', Buffer.from('missing original notice'));
    assert.throws(() => readDarwinArchive(archive(binary, notices), arch), /license mismatch/u);
    notices.set('../outside', Buffer.from('untrusted'));
    assert.throws(() => readDarwinArchive(archive(binary, notices), arch), /Invalid Darwin archive entry/u);
  }
});
test('Darwin archive verification rejects missing execute bits and truncated payloads', () => {
  const bytes = archive(machO('arm64'), darwinNotices());
  const tar = gunzipSync(bytes);
  tar.write('0000644\0', 100, 8);
  tar.fill(32, 148, 156);
  const checksum = tar.subarray(0, 512).reduce((sum, byte) => sum + byte, 0);
  tar.write(`${checksum.toString(8).padStart(6, '0')}\0 `, 148, 8);
  assert.throws(() => readDarwinArchive(gzipSync(tar), 'arm64'), /Invalid Darwin archive payload/u);
  assert.throws(() => readDarwinArchive(gzipSync(gunzipSync(bytes).subarray(0, 600)), 'arm64'), /Incomplete Darwin archive/u);
});
test('wrong tags, corrupt bytes, bad signatures and incomplete public releases fail before writes', async () => {
  const { state, adapters } = fixture(); state.tag = 'b'.repeat(40);
  await assert.rejects(publish({ plan, files }, adapters), /source commit/u); assert.equal(state.writes, 0);
  state.tag = plan.revision; await publish({ plan, files }, adapters); const writes = state.writes;
  const asset = state.release.assets[0]; const original = asset.bytes; asset.bytes = Buffer.from('tampered');
  await assert.rejects(publish({ plan, files }, adapters), /Conflicting immutable/u); asset.bytes = original;
  state.rejectSignature = true; await assert.rejects(publish({ plan, files }, adapters)); state.rejectSignature = false;
  state.release.assets.pop(); await assert.rejects(publish({ plan, files }, adapters), /Incomplete public release/u);
  assert.equal(state.writes, writes);
});
