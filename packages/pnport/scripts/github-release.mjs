import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { ensure, event, isMain, packageRoot, revision } from './common.mjs';
import { archiveName, verifySet } from './package.mjs';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { verifyBundle } from '../../../scripts/release/linux-packages/release-input.mjs';

const require = createRequire(import.meta.url);
const { targets } = require('../src/platforms.cjs');
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

export function stage(directory, output, sourceRevision) {
  const artifacts = verifySet(directory, sourceRevision);
  const plan = { project: 'pnport', version: artifacts.version, revision: sourceRevision, tag: `pnport@v${artifacts.version}` };
  const files = new Map();
  for (const target of targets) files.set(archiveName(target), readFileSync(path.join(directory, 'archives', archiveName(target))));
  files.set('SHA256SUMS', Buffer.from([...files].map(([name, bytes]) => `${sha256(bytes)}  ${name}\n`).join('')));
  mkdirSync(output, { recursive: true });
  for (const [name, bytes] of files) writeFileSync(path.join(output, name), bytes);
  event('github_stage', { version: plan.version, revision: sourceRevision, assets: [...files.keys()] });
  return { plan, files };
}

// A draft is the recovery boundary. Existing bytes and signatures are verified
// before any writes; public assets are immutable and a complete retry is read-only.
export async function publish({ plan, files }, { api, download, upload, sign, verify, report = event }) {
  const prefix = '/repos/delinoio/oss';
  let object = (await api('GET', `${prefix}/git/ref/tags/${encodeURIComponent(plan.tag)}`)).object;
  for (let depth = 0; object?.type === 'tag' && depth < 4; depth++) {
    ensure(/^[a-f0-9]{40}$/u.test(object.sha), 'Invalid annotated tag');
    object = (await api('GET', `${prefix}/git/tags/${object.sha}`)).object;
  }
  ensure(object?.type === 'commit' && object.sha === plan.revision, 'Release tag does not match the exact source commit');
  let release = await api('GET', `${prefix}/releases/tags/${encodeURIComponent(plan.tag)}`, undefined, true);
  const expected = [...files.keys()].flatMap((name) => [name, `${name}.sigstore.json`]);
  const existing = new Map();
  if (release) {
    ensure(release.tag_name === plan.tag && release.prerelease === false && (!release.draft || release.target_commitish === plan.revision), 'Conflicting release identity');
    for (const asset of release.assets) {
      ensure(expected.includes(asset.name) && !existing.has(asset.name), 'Unexpected release asset');
      existing.set(asset.name, await download(asset));
    }
    for (const [name, bytes] of files) {
      if (existing.has(name)) ensure(existing.get(name).equals(bytes), `Conflicting immutable asset: ${name}`);
      if (existing.has(`${name}.sigstore.json`)) await verify(name, bytes, existing.get(`${name}.sigstore.json`));
    }
    if (!release.draft) {
      ensure(existing.size === expected.length, 'Incomplete public release; refusing to mutate public assets');
      report('github_reuse', { tag: plan.tag }); return;
    }
  }
  if (!release) release = await api('POST', `${prefix}/releases`, { tag_name: plan.tag, target_commitish: plan.revision, name: plan.tag, draft: true, prerelease: false, generate_release_notes: true });
  for (const [name, bytes] of files) {
    if (!existing.has(name)) await upload(release, name, bytes);
    report('github_asset', { tag: plan.tag, asset: name, reused: existing.has(name) });
    const bundleName = `${name}.sigstore.json`;
    if (!existing.has(bundleName)) {
      const bundle = await sign(name, bytes); await verify(name, bytes, bundle);
      await upload(release, bundleName, bundle);
    }
  }
  const ready = await api('GET', `${prefix}/releases/${release.id}`);
  ensure(ready.draft === true && ready.tag_name === plan.tag && ready.target_commitish === plan.revision && ready.prerelease === false, 'Draft identity changed');
  ensure(ready.assets.length === expected.length && new Set(ready.assets.map(({ name }) => name)).size === expected.length, 'Incomplete signed release');
  for (const [name, bytes] of files) {
    const asset = ready.assets.find((item) => item.name === name);
    const signature = ready.assets.find((item) => item.name === `${name}.sigstore.json`);
    ensure(asset && signature && (await download(asset)).equals(bytes), 'Release readback mismatch');
    await verify(name, bytes, await download(signature));
  }
  await api('PATCH', `${prefix}/releases/${release.id}`, { draft: false });
  report('github_publish', { tag: plan.tag, revision: plan.revision });
}

export async function main() {
  ensure(process.argv.slice(2).every((arg) => arg === '--publish') && process.argv.length <= 3, 'Unknown release argument');
  const output = path.join(packageRoot, 'dist/github');
  const candidate = stage(path.join(packageRoot, 'dist'), output, revision());
  if (!process.argv.includes('--publish')) return;
  const { plan } = candidate;
  ensure(process.env.GITHUB_REPOSITORY === 'delinoio/oss' && process.env.GITHUB_REF === `refs/tags/${plan.tag}` && process.env.GITHUB_SHA === plan.revision, 'Publication requires the exact first-party tag and commit');
  ensure(process.env.GH_TOKEN && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN, 'GitHub token and Actions OIDC are required');
  const request = async (url, options = {}, allowMissing = false) => {
    const response = await fetch(url, { ...options, headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28', ...options.headers }, signal: AbortSignal.timeout(60000), redirect: 'error' });
    if (allowMissing && response.status === 404) return null;
    ensure(response.ok, `GitHub release request failed: HTTP ${response.status}`); return response;
  };
  await publish(candidate, {
    api: async (method, endpoint, body, missing) => (await request(`https://api.github.com${endpoint}`, { method, body: body ? JSON.stringify(body) : undefined, headers: { 'Content-Type': 'application/json' } }, missing))?.json() ?? null,
    // gh handles GitHub's authenticated asset redirects without forwarding our
    // authorization header to an arbitrary redirect destination.
    download: (asset) => execFileSync('gh', ['api', `repos/delinoio/oss/releases/assets/${asset.id}`, '-H', 'Accept: application/octet-stream'], { maxBuffer: 300 * 1024 * 1024 }),
    upload: (release, name, bytes) => request(`https://uploads.github.com/repos/delinoio/oss/releases/${release.id}/assets?name=${encodeURIComponent(name)}`, { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: bytes }),
    sign: (name) => { const file = path.join(output, name); execFileSync('cosign', ['sign-blob', '--yes', '--bundle', `${file}.sigstore.json`, file], { stdio: 'inherit' }); return readFileSync(`${file}.sigstore.json`); },
    verify: (name, bytes, bundle) => { const file = path.join(output, name); writeFileSync(file, bytes); writeFileSync(`${file}.sigstore.json`, bundle); verifyBundle(file, `${file}.sigstore.json`, plan); },
  });
}
if (isMain(import.meta.url)) main().catch((error) => { console.error(JSON.stringify({ event: 'pnport_github_release_failed', message: error.message })); process.exitCode = 1; });
