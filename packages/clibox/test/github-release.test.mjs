import assert from 'node:assert/strict';
import test from 'node:test';
import { archive, publish } from '../scripts/github-release.mjs';
import { extractExecutable, identity } from '../../../scripts/release/linux-packages/model.mjs';

const plan = identity({ project: 'clibox', version: '1.2.3', revision: 'a'.repeat(40) });
const files = new Map([['clibox-linux-amd64.tar.gz', Buffer.from('amd64')], ['clibox-linux-arm64.tar.gz', Buffer.from('arm64')], ['SHA256SUMS', Buffer.from('checksums')]]);
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
  assert.equal(state.release.draft, false); assert.equal(state.release.assets.length, 6); assert.equal(state.signed, 3);
  const writes = state.writes;
  await publish({ plan, files }, adapters);
  assert.equal(state.writes, writes); assert.equal(state.signed, 3);
});
test('interrupted upload resumes only missing assets and preserves completed signatures', async () => {
  const { state, adapters } = fixture();
  const upload = adapters.upload; let count = 0;
  adapters.upload = async (...args) => { await upload(...args); if (++count === 3) throw new Error('interrupted'); };
  await assert.rejects(publish({ plan, files }, adapters), /interrupted/u);
  const firstSignature = state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes;
  adapters.upload = upload;
  await publish({ plan, files }, adapters);
  assert.equal(state.release.draft, false); assert.equal(state.signed, 3);
  assert.equal(state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes, firstSignature);
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
