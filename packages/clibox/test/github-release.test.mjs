import assert from 'node:assert/strict';
import test from 'node:test';
import { gzipSync, gunzipSync } from 'node:zlib';
import { archive, archiveNames, darwinNotices, publish, readDarwinArchive } from '../scripts/github-release.mjs';
import { machO } from './fixtures.mjs';
import { extractExecutable, identity } from '../../../scripts/release/linux-packages/model.mjs';

const plan = identity({ project: 'clibox', version: '1.2.3', revision: 'a'.repeat(40) });
const files = new Map([...archiveNames, 'SHA256SUMS'].map((name) => [name, Buffer.from(name)]));
const prefix = '/repos/delinoio/oss';
const listPage = (page) => `${prefix}/releases?per_page=100&page=${page}`;
const draft = (fields = {}) => ({ id: 71, tag_name: plan.tag, target_commitish: plan.revision, draft: true, prerelease: false, assets: [], ...fields });
const snapshot = (release) => ({ ...release, assets: release.assets.map((asset) => ({ ...asset })) });
function fixture() {
  const state = { release: null, otherReleases: [], calls: [], uploads: [], reports: [], writes: 0, creations: 0, signed: 0, nextAssetId: 1,
    tag: plan.revision, rejectSignature: false, interrupt: false, loseCreateResponse: false };
  const releases = () => [...state.otherReleases, ...(state.release ? [state.release] : [])];
  const adapters = {
    api: async (method, endpoint, body) => {
      state.calls.push({ method, endpoint, body });
      if (endpoint.includes('/git/ref/')) return { object: { type: 'commit', sha: state.tag } };
      // GitHub's tag lookup exposes published releases, never retained drafts.
      if (method === 'GET' && endpoint === `${prefix}/releases/tags/${encodeURIComponent(plan.tag)}`) return state.release && !state.release.draft ? snapshot(state.release) : null;
      const page = endpoint.match(/\/releases\?per_page=100&page=(\d+)$/u);
      if (method === 'GET' && page) return releases().slice((Number(page[1]) - 1) * 100, Number(page[1]) * 100).map(snapshot);
      if (method === 'POST' && endpoint === `${prefix}/releases`) {
        state.writes++; state.creations++; state.release = draft(body);
        if (state.loseCreateResponse) { state.loseCreateResponse = false; throw new Error('create response lost'); }
        return snapshot(state.release);
      }
      const byId = endpoint.match(/\/releases\/(\d+)$/u);
      if (byId) {
        const release = releases().find(({ id }) => id === Number(byId[1]));
        assert.ok(release, 'pinned release must exist');
        if (method === 'PATCH') { state.writes++; Object.assign(release, body); }
        else assert.equal(method, 'GET');
        return snapshot(release);
      }
      assert.fail(`Unexpected fixture route: ${method} ${endpoint}`);
    },
    download: async (asset) => asset.bytes,
    upload: async (release, name, bytes) => {
      state.writes++; state.uploads.push({ releaseId: release.id, name });
      const retained = releases().find(({ id }) => id === release.id);
      assert.ok(retained); assert.ok(!retained.assets.some((asset) => asset.name === name), 'existing assets must never be overwritten');
      retained.assets.push({ id: state.nextAssetId++, name, bytes, size: bytes.length, state: 'uploaded' });
      if (state.interrupt) { state.interrupt = false; throw new Error('interrupted'); }
    },
    sign: async (name, bytes) => { state.signed++; return Buffer.from(`signature:${name}:${bytes}`); },
    verify: async (name, bytes, bundle) => { assert.equal(state.rejectSignature, false); assert.equal(bundle.toString(), `signature:${name}:${bytes}`); },
    report: (event, fields) => state.reports.push({ event, ...fields }),
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
  // Published release metadata may retain a branch name; the exact tag commit
  // and verified asset bytes still establish its source on a read-only retry.
  state.release.target_commitish = 'main';
  await publish({ plan, files }, adapters);
  assert.equal(state.writes, writes); assert.equal(state.signed, 5);
  assert.ok(!state.calls.some(({ endpoint }) => endpoint.includes('/releases/tags/')));
  assert.equal(state.calls.at(-1).endpoint, `${prefix}/releases/71`);
});
test('interrupted upload resumes only missing assets and preserves completed signatures', async () => {
  const { state, adapters } = fixture();
  const upload = adapters.upload; let count = 0;
  adapters.upload = async (...args) => { await upload(...args); if (++count === 3) throw new Error('interrupted'); };
  await assert.rejects(publish({ plan, files }, adapters), /interrupted/u);
  const firstSignature = state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes;
  const releaseId = state.release.id;
  const completed = state.release.assets.map(({ name }) => name);
  state.uploads = [];
  adapters.upload = upload;
  await publish({ plan, files }, adapters);
  assert.equal(state.release.draft, false); assert.equal(state.signed, 5);
  assert.equal(state.release.assets.find(({ name }) => name.endsWith('.sigstore.json')).bytes, firstSignature);
  assert.equal(state.creations, 1); assert.equal(state.release.id, releaseId);
  assert.ok(state.uploads.every(({ releaseId: id, name }) => id === releaseId && !completed.includes(name)));
});
test('retained draft on a later list page resumes by ID with only missing assets', async () => {
  const { state, adapters } = fixture();
  state.release = draft();
  state.otherReleases = Array.from({ length: 100 }, (_, index) => draft({ id: index + 1000, tag_name: `other@v${index}` }));
  const [name, bytes] = [...files][0];
  await adapters.upload(state.release, name, bytes);
  const bundle = await adapters.sign(name, bytes);
  await adapters.upload(state.release, `${name}.sigstore.json`, bundle);
  state.writes = 0; state.uploads = [];
  assert.equal(await adapters.api('GET', `${prefix}/releases/tags/${encodeURIComponent(plan.tag)}`), null);
  await publish({ plan, files }, adapters);
  assert.equal(state.creations, 0); assert.equal(state.signed, 5); assert.equal(state.release.id, 71); assert.equal(state.release.draft, false);
  assert.equal(state.release.assets.find((asset) => asset.name === `${name}.sigstore.json`).bytes, bundle);
  assert.equal(state.uploads.length, 8); assert.ok(state.uploads.every(({ releaseId }) => releaseId === 71));
  assert.ok(state.calls.some(({ endpoint }) => endpoint === listPage(2)));
  assert.ok(state.reports.some(({ event, release_id }) => event === 'github_discovery' && release_id === 71));
});
test('lost create response is recovered without a second draft creation', async () => {
  const { state, adapters } = fixture(); state.loseCreateResponse = true;
  await assert.rejects(publish({ plan, files }, adapters), /create response lost/u);
  const releaseId = state.release.id;
  await publish({ plan, files }, adapters);
  assert.equal(state.creations, 1); assert.equal(state.release.id, releaseId); assert.equal(state.release.draft, false);
});
test('duplicate draft and public candidates block every write, including duplicates on later pages', async () => {
  for (const [primary, fields] of [[{}, {}], [{}, { draft: false }], [{ draft: false }, { draft: false }], [{}, { target_commitish: 'b'.repeat(40) }]]) {
    const { state, adapters } = fixture(); state.release = draft(primary);
    state.otherReleases = [draft({ id: 72, ...fields }), ...Array.from({ length: 99 }, (_, index) => draft({ id: index + 1000, tag_name: `other@v${index}` }))];
    await assert.rejects(publish({ plan, files }, adapters), /Multiple releases/u);
    assert.equal(state.writes, 0); assert.equal(state.signed, 0);
  }
});
test('a full final page requires an empty page before absence can authorize creation', async () => {
  const { state, adapters } = fixture();
  state.otherReleases = Array.from({ length: 100 }, (_, index) => draft({ id: index + 1000, tag_name: `other@v${index}` }));
  await publish({ plan, files }, adapters);
  const creation = state.calls.findIndex(({ method }) => method === 'POST');
  assert.equal(state.calls[creation - 1].endpoint, listPage(2));
  assert.equal(state.creations, 1);
});
test('the pagination limit rejects an unterminated scan before any write', async () => {
  const { state, adapters } = fixture(); const api = adapters.api; let pages = 0;
  adapters.api = async (method, endpoint, ...args) => {
    const page = endpoint.match(/\/releases\?per_page=100&page=(\d+)$/u);
    if (!page) return api(method, endpoint, ...args);
    pages++;
    return Array.from({ length: 100 }, (_, index) => draft({ id: Number(page[1]) * 100 + index, tag_name: `other@v${index}` }));
  };
  await assert.rejects(publish({ plan, files }, adapters), /pagination limit/u);
  assert.equal(pages, 1000); assert.equal(state.writes, 0); assert.equal(state.signed, 0);
});
test('malformed and failed list discovery never authorize a write', async () => {
  for (const response of [null, {}, [null], [{}], [draft({ id: '71' })], [draft({ id: 0 })], [draft({ tag_name: null })],
    [draft({ draft: 'true' })], [draft({ prerelease: null })], [draft({ target_commitish: null })], Array.from({ length: 101 }, (_, index) => draft({ id: index + 1 }))]) {
    const { state, adapters } = fixture(); const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => endpoint === listPage(1) ? response : api(method, endpoint, ...args);
    await assert.rejects(publish({ plan, files }, adapters), /release list/u);
    assert.equal(state.writes, 0); assert.equal(state.signed, 0);
  }
  for (const status of [401, 403, 404, 429, 500]) {
    const { state, adapters } = fixture(); const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => {
      if (endpoint === listPage(1)) throw new Error(`HTTP ${status}`);
      return api(method, endpoint, ...args);
    };
    await assert.rejects(publish({ plan, files }, adapters), new RegExp(`HTTP ${status}`, 'u'));
    assert.equal(state.writes, 0);
  }
});
test('a later page failure or repeated release ID blocks writes even after a matching draft is found', async () => {
  for (const repeat of [false, true]) {
    const { state, adapters } = fixture(); state.release = draft();
    const firstPage = [state.release, ...Array.from({ length: 99 }, (_, index) => draft({ id: index + 1000, tag_name: `other@v${index}` }))];
    const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => {
      if (endpoint === listPage(1)) return firstPage;
      if (endpoint === listPage(2)) { if (repeat) return [firstPage[1]]; throw new Error('pagination failed'); }
      return api(method, endpoint, ...args);
    };
    await assert.rejects(publish({ plan, files }, adapters), repeat ? /Repeated release ID/u : /pagination failed/u);
    assert.equal(state.writes, 0); assert.equal(state.signed, 0);
  }
});
test('mismatched draft source, channel and pinned identity block every write', async () => {
  for (const fields of [{ target_commitish: 'b'.repeat(40) }, { prerelease: true }, { assets: null }]) {
    const { state, adapters } = fixture(); state.release = draft(); const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => {
      const value = await api(method, endpoint, ...args);
      return endpoint === listPage(1) ? [{ ...value[0], ...fields }] : value;
    };
    await assert.rejects(publish({ plan, files }, adapters), /release identity/u);
    assert.equal(state.writes, 0);
  }
  for (const fields of [null, { id: 72 }, { tag_name: 'other' }, { target_commitish: 'b'.repeat(40) }, { draft: false }, { prerelease: true }, { assets: null }]) {
    const { state, adapters } = fixture(); state.release = draft(); const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => {
      const value = await api(method, endpoint, ...args);
      if (endpoint === `${prefix}/releases/71`) return fields === null ? null : { ...value, ...fields };
      return value;
    };
    await assert.rejects(publish({ plan, files }, adapters), /release identity/u);
    assert.equal(state.writes, 0);
  }
});
test('invalid creation responses authorize no uploads or signing', async () => {
  for (const fields of [{ id: '71' }, { id: -1 }, { tag_name: 'other' }, { target_commitish: 'b'.repeat(40) }, { draft: false }, { prerelease: true }, { assets: null }]) {
    const { state, adapters } = fixture(); const api = adapters.api;
    adapters.api = async (method, endpoint, ...args) => {
      const value = await api(method, endpoint, ...args);
      return method === 'POST' ? { ...value, ...fields } : value;
    };
    await assert.rejects(publish({ plan, files }, adapters), /release identity/u);
    assert.equal(state.creations, 1); assert.equal(state.uploads.length, 0); assert.equal(state.signed, 0);
  }
});
test('fresh discovery rejects a concurrent duplicate, replacement or missing draft before publication', async () => {
  for (const change of ['duplicate', 'replacement', 'missing', 'failure']) {
    const { state, adapters } = fixture(); state.release = draft(); const api = adapters.api; let scans = 0;
    adapters.api = async (method, endpoint, ...args) => {
      if (endpoint === listPage(1) && ++scans === 2) {
        if (change === 'duplicate') state.otherReleases.push(draft({ id: 72 }));
        if (change === 'replacement') state.release.id = 72;
        if (change === 'missing') state.release = null;
        if (change === 'failure') throw new Error('final discovery failed');
      }
      return api(method, endpoint, ...args);
    };
    await assert.rejects(publish({ plan, files }, adapters), /Multiple releases|Pinned draft|final discovery failed/u);
    assert.ok(!state.calls.some(({ method }) => method === 'PATCH'));
  }
});
test('final ID read rejects changed draft identity or verified asset inventory before PATCH', async () => {
  for (const change of ['id', 'tag', 'source', 'channel', 'public', 'asset-id', 'asset-size', 'asset-state', 'asset-name', 'asset-removed']) {
    const { state, adapters } = fixture(); state.release = draft(); const api = adapters.api; let reads = 0;
    adapters.api = async (method, endpoint, ...args) => {
      const value = await api(method, endpoint, ...args);
      if (method === 'GET' && endpoint === `${prefix}/releases/71` && ++reads === 3) {
        if (change === 'id') value.id = 72;
        if (change === 'tag') value.tag_name = 'other';
        if (change === 'source') value.target_commitish = 'b'.repeat(40);
        if (change === 'channel') value.prerelease = true;
        if (change === 'public') value.draft = false;
        if (change === 'asset-id') value.assets[0].id = 999;
        if (change === 'asset-size') value.assets[0].size++;
        if (change === 'asset-state') value.assets[0].state = 'starter';
        if (change === 'asset-name') value.assets[0].name = 'other';
        if (change === 'asset-removed') value.assets.pop();
      }
      return value;
    };
    await assert.rejects(publish({ plan, files }, adapters), /release identity|release asset|Verified draft/u);
    assert.ok(!state.calls.some(({ method }) => method === 'PATCH'));
  }
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
