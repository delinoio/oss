import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { archive, archiveNames, darwinNotices, unsignedNames } from '../scripts/github-release.mjs';
import { metadata, sourceText } from '../scripts/common.mjs';
import { main, prepareHomebrew, publicationContext, publishedRelease, verifyArchives, verifyTestedFormula } from '../scripts/homebrew.mjs';
import { identity, sha256 } from '../../../scripts/release/linux-packages/model.mjs';
import { machO } from './fixtures.mjs';

const plan = identity({ project: 'clibox', version: metadata().version, revision: 'a'.repeat(40) });
function temporary(t) {
  const directory = mkdtempSync(path.join(tmpdir(), 'clibox-homebrew-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
function fixture() {
  const files = new Map(archiveNames.map((name) => [name, name.includes('darwin') ? archive(machO(name.includes('amd64') ? 'amd64' : 'arm64'), darwinNotices()) : archive(Buffer.from(name))]));
  files.set('SHA256SUMS', Buffer.from([...files].map(([name, bytes]) => `${sha256(bytes)}  ${name}\n`).join('')));
  for (const name of unsignedNames) files.set(`${name}.sigstore.json`, Buffer.from(`signature:${name}`));
  const release = { id: 1, tag_name: plan.tag, target_commitish: plan.revision, draft: false, prerelease: false,
    assets: [...files].map(([name, bytes], i) => ({ id: i + 1, name, state: 'uploaded', size: bytes.length, digest: `sha256:${sha256(bytes)}` })) };
  const responses = new Map([
    [`git/ref/tags/${encodeURIComponent(plan.tag)}`, { object: { type: 'commit', sha: plan.revision } }],
    [`releases/tags/${encodeURIComponent(plan.tag)}`, release],
  ]);
  for (const name of ['crates/clibox/Cargo.toml', 'packages/clibox/package.json']) responses.set(`contents/${name}?ref=${plan.revision}`, { encoding: 'base64', content: Buffer.from(sourceText(name)).toString('base64') });
  const api = async (endpoint) => {
    assert.ok(endpoint.startsWith('repos/delinoio/oss/'));
    const response = responses.get(endpoint.replace('repos/delinoio/oss/', ''));
    assert.ok(response, endpoint);
    return response;
  };
  const verified = [];
  return { files, release, responses, api, verified,
    download: async (asset) => files.get(asset.name),
    verify: async (file, bundle, actualPlan) => {
      assert.deepEqual(actualPlan, plan);
      assert.equal(readFileSync(bundle, 'utf8'), `signature:${path.basename(file)}`);
      verified.push(path.basename(file));
    },
    render: (_plan, directory) => { verifyArchives(directory); return 'verified formula'; },
  };
}

test('Homebrew verifies all ten public assets, source identity, checksums, and five signatures', async (t) => {
  const f = fixture();
  assert.equal(await prepareHomebrew(plan, temporary(t), f), 'verified formula');
  assert.deepEqual(f.verified, unsignedNames);
});

test('Homebrew rejects incorrect source identity and incomplete or ambiguous releases', async () => {
  for (const mutate of [
    (f) => { f.responses.get(`git/ref/tags/${encodeURIComponent(plan.tag)}`).object.sha = 'b'.repeat(40); },
    (f) => { f.release.target_commitish = 'b'.repeat(40); },
    (f) => { f.release.tag_name = 'clibox@v0.0.0'; },
    (f) => { f.release.draft = true; },
    (f) => { f.release.prerelease = true; },
    (f) => { f.release.assets.pop(); },
    (f) => { f.release.assets[0] = f.release.assets[1]; },
    (f) => { f.release.assets[0].id = f.release.assets[1].id; },
    (f) => { f.release.assets[0].digest = null; },
    (f) => { f.release.assets[0].state = 'new'; },
    (f) => { f.release.assets[0].size = 65 * 1024 * 1024; },
    (f) => { f.responses.get(`contents/packages/clibox/package.json?ref=${plan.revision}`).content = Buffer.from('{"name":"@delino/clibox","version":"0.0.0"}').toString('base64'); },
  ]) {
    const f = fixture(); mutate(f);
    await assert.rejects(publishedRelease(plan, f.api));
  }
});

test('Homebrew resolves annotated tags and rejects corrupt bytes and invalid signatures', async (t) => {
  const f = fixture();
  f.responses.set(`git/ref/tags/${encodeURIComponent(plan.tag)}`, { object: { type: 'tag', sha: 'b'.repeat(40) } });
  f.responses.set(`git/tags/${'b'.repeat(40)}`, { object: { type: 'commit', sha: plan.revision } });
  assert.equal((await publishedRelease(plan, f.api)).id, 1);
  await assert.rejects(prepareHomebrew(plan, temporary(t), { ...f, download: async () => Buffer.from('tampered') }), /digest mismatch/u);
  assert.deepEqual(f.verified, []);
  await assert.rejects(prepareHomebrew(plan, temporary(t), { ...f, verify: () => { throw new Error('bad signature'); } }), /bad signature/u);
});

test('signed-but-inconsistent checksums and altered native validation evidence fail closed', async (t) => {
  const f = fixture();
  const directory = temporary(t);
  for (const [name, bytes] of f.files) writeFileSync(path.join(directory, name), bytes);
  assert.equal(verifyArchives(directory).size, 4);
  writeFileSync(path.join(directory, 'SHA256SUMS'), '0'.repeat(64) + '  clibox-darwin-amd64.tar.gz\n');
  assert.throws(() => verifyArchives(directory), /checksum manifest mismatch/u);
  for (const arch of ['amd64', 'arm64']) {
    mkdirSync(path.join(directory, arch));
    writeFileSync(path.join(directory, arch, 'clibox.rb'), 'tested');
  }
  verifyTestedFormula('tested', directory);
  writeFileSync(path.join(directory, 'arm64', 'clibox.rb'), 'different');
  assert.throws(() => verifyTestedFormula('tested', directory), /formula changed/u);
});

test('publication requires the exact first-party tag before network access', async () => {
  const env = { GITHUB_ACTIONS: 'true', GITHUB_REPOSITORY: 'delinoio/oss', GITHUB_REF: `refs/tags/${plan.tag}`, GITHUB_SHA: plan.revision };
  publicationContext(plan, env);
  for (const change of [{ GITHUB_REF: 'refs/heads/main' }, { GITHUB_REPOSITORY: 'fork/oss' }, { GITHUB_SHA: 'b'.repeat(40) }, { GITHUB_ACTIONS: 'false' }]) assert.throws(() => publicationContext(plan, { ...env, ...change }), /exact first-party/u);
  await assert.rejects(main(['publish', '--validated', 'unused'], {}), /exact first-party/u);
});
