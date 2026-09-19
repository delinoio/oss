import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { assertMatchingDraft, prepareDraft, files } from '../release/runlens-draft.cjs';

const tag = 'runlens@v0.1.0';
const context = { repo: { owner: 'delinoio', repo: 'oss' }, sha: 'a'.repeat(40) };
const digest = data => `sha256:${createHash('sha256').update(data).digest('hex')}`;
function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'runlens-draft-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const file of files) writeFileSync(join(directory, file), `verified:${file}`);
  const state = { release: undefined, assets: [], creates: 0, uploads: 0, verifications: 0, failAt: undefined };
  const github = { rest: { repos: {
    async getReleaseByTag() {
      if (!state.release) throw Object.assign(Error('missing'), { status: 404 });
      return { data: state.release };
    },
    async createRelease(args) {
      state.creates++;
      state.release = { ...args, id: 1, prerelease: false };
      return { data: state.release };
    },
    async uploadReleaseAsset({ name, data }) {
      if (state.failAt === state.uploads) throw Error('upload interrupted');
      state.uploads++;
      state.assets.push({ id: state.assets.length + 1, name, size: data.length, digest: digest(data), state: 'uploaded', data });
    },
    async listReleaseAssets() {},
    async getReleaseAsset({ asset_id, headers }) {
      assert.equal(headers.accept, 'application/octet-stream');
      return { data: state.assets.find(asset => asset.id === asset_id).data };
    },
  } }, async paginate() { return state.assets; } };
  const run = () => prepareDraft({ github, context, tag, directory, verify: (bundle, payload, identity) => {
    state.verifications++;
    assert.equal(identity, tag);
    assert.ok(readFileSync(payload).toString().startsWith('verified:'));
    assert.match(bundle.toString(), /^verified:/u);
  } });
  return { directory, state, run };
}

test('interrupted draft uploads resume without replacing retained assets', async t => {
  const { state, run } = fixture(t);
  state.failAt = 5;
  await assert.rejects(run, /interrupted/u);
  assert.equal(state.release.target_commitish, context.sha);
  const retained = state.assets.slice();
  state.failAt = undefined;
  await run();
  assert.equal(state.creates, 1);
  assert.equal(state.uploads, 14);
  assert.deepEqual(state.assets.slice(0, 5), retained);
  assert.equal(state.release.draft, true);
});

test('tap or publication retries preserve authenticated original signature bytes', async t => {
  const { directory, state, run } = fixture(t);
  await run();
  const retained = structuredClone(state.assets);
  for (const file of files.filter(file => file.endsWith('.sigstore.json'))) {
    writeFileSync(join(directory, file), 'new-signing-attempt');
  }
  await run();
  await run();
  assert.equal(state.uploads, 14);
  assert.equal(state.creates, 1);
  assert.equal(state.verifications, 14);
  assert.deepEqual(state.assets.map(a => a.digest), retained.map(a => a.digest));
});

test('published, foreign-commit, foreign-tag and prerelease candidates are refused', async t => {
  const { state, run } = fixture(t);
  await run();
  for (const changes of [{ draft: false }, { target_commitish: 'b'.repeat(40) }, { tag_name: 'runlens@v0.2.0' }, { prerelease: true }]) {
    assert.throws(() => assertMatchingDraft({ ...state.release, ...changes }, tag, context.sha), /unpublished draft/u);
  }
  state.release.draft = false;
  await assert.rejects(run, /unpublished draft/u);
  assert.equal(state.uploads, 14);
});

test('mismatched payloads, unauthenticated signatures and unexpected assets never mutate drafts', async t => {
  const { state, run } = fixture(t);
  await run();
  const original = state.assets.slice();
  for (const corrupt of [
    assets => { assets[0].digest = 'sha256:' + '0'.repeat(64); },
    assets => { assets[1].data = Buffer.from('untrusted'); assets[1].size = 9; assets[1].digest = digest(assets[1].data); },
    assets => { assets.push({ ...assets[0], name: 'unexpected' }); },
    assets => { assets.push({ ...assets[0] }); },
    assets => { assets[0].state = 'starter'; },
  ]) {
    state.assets = original.map(asset => ({ ...asset }));
    corrupt(state.assets);
    await assert.rejects(run);
    assert.equal(state.uploads, 14);
    assert.equal(state.release.draft, true);
  }
});
