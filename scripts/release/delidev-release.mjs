// SPDX-License-Identifier: Apache-2.0
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { appendFileSync, createReadStream, lstatSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { targets as nativeTargets } from '../../apps/delidev/scripts/native-package.mjs';
import { artifactName, signingRoot } from './generate-delidev-updater.mjs';
import { releaseTag, Project, readVersion } from './project.mjs';

export const releaseTargets = nativeTargets.filter(t => t.platform !== 'win32');
export function requireValue(condition, message) { if (!condition) throw new Error(message); }
export function identity(version, revision) {
  requireValue(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) && version.split('.').every(n => BigInt(n) <= 0xffffffffn) && /^[a-f0-9]{40}$/.test(revision), 'Invalid DeliDev release identity');
  return { schemaVersion: 1, version, sourceRevision: revision, tag: releaseTag(Project.DeliDev, version), channel: 'download-only', excludedTargets: ['windows-amd64', 'windows-arm64'] };
}
export function updaterTarget(t) { return `${t.platform}-${t.arch === 'x64' ? 'amd64' : 'arm64'}`; }
export function expectedNames() {
  return [...releaseTargets.flatMap(t => {
    const target = updaterTarget(t);
    return [artifactName('desktop', target), artifactName('worker', target), ...(t.platform === 'linux' ? [`delidev-desktop-${target}.deb`] : [])];
  }), 'LICENSE', 'NOTICE'].sort();
}
export async function digest(file) {
  const before = lstatSync(file);
  requireValue(before.isFile() && !before.isSymbolicLink() && before.size > 0 && before.size <= 2 * 1024 ** 3, 'Invalid DeliDev release file');
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  const after = lstatSync(file);
  requireValue(before.ino === after.ino && before.dev === after.dev && before.size === after.size && before.mtimeMs === after.mtimeMs, 'DeliDev release file changed during verification');
  return { size: before.size, sha256: hash.digest('hex') };
}
export function validateIndex(index, expected) {
  for (const key of ['schemaVersion', 'version', 'sourceRevision', 'tag', 'channel', 'excludedTargets']) requireValue(JSON.stringify(index[key]) === JSON.stringify(expected[key]), 'DeliDev release identity conflict');
  requireValue(Array.isArray(index.artifacts) && JSON.stringify(index.artifacts.map(a => a.name).sort()) === JSON.stringify(expectedNames()), 'Incomplete DeliDev release inventory');
  for (const a of index.artifacts) requireValue(Number.isSafeInteger(a.size) && a.size > 0 && a.size <= 2 * 1024 ** 3 && /^[a-f0-9]{64}$/.test(a.sha256), 'Invalid DeliDev artifact metadata');
  requireValue(index.nativeAcceptance === 'unverified' && index.platformSigning === 'developer-id-notarized-and-linux-digests', 'Invalid DeliDev verification declaration');
  return index;
}
export async function verifyDirectory(directory, expected) {
  const indexFile = join(directory, 'delidev-release-index.json');
  requireValue(lstatSync(indexFile).isFile() && !lstatSync(indexFile).isSymbolicLink() && lstatSync(indexFile).size <= 65536, 'Invalid release index file');
  const index = validateIndex(JSON.parse(readFileSync(indexFile, 'utf8')), expected);
  const names = [...expectedNames(), 'delidev-release-index.json', 'SHA256SUMS'].sort();
  requireValue(JSON.stringify(readdirSync(directory).sort()) === JSON.stringify(names), 'Unexpected DeliDev release files; partial releases forbid updater manifests');
  for (const a of index.artifacts) requireValue(JSON.stringify(await digest(join(directory, a.name))) === JSON.stringify({ size: a.size, sha256: a.sha256 }), 'DeliDev release artifact bytes conflict');
  const sums = [...index.artifacts, { name: 'delidev-release-index.json', ...await digest(indexFile) }].sort((a,b) => a.name.localeCompare(b.name)).map(a => `${a.sha256}  ${a.name}\n`).join('');
  requireValue(readFileSync(join(directory, 'SHA256SUMS'), 'utf8') === sums, 'DeliDev checksums conflict');
  return index;
}
export async function assemble(inputs, output, expected) {
  mkdirSync(output, { recursive: true });
  requireValue(readdirSync(output).length === 0, 'Retain the original DeliDev release assembly');
  const { copyFileSync } = await import('node:fs');
  const reports = [];
  for (const t of releaseTargets) {
    const directory = join(inputs, t.target);
    const report = JSON.parse(readFileSync(join(directory, 'release-input.json'), 'utf8'));
    requireValue(report.schemaVersion === 1 && report.sourceRevision === expected.sourceRevision && report.version === expected.version && report.target === t.target && report.nativeAcceptance === 'unverified' && report.platformSigning === (t.platform === 'darwin' ? 'developer-id-notarized' : 'linux-digests'), 'DeliDev candidate verification conflict');
    const names = expectedNames().filter(n => n.includes(updaterTarget(t)));
    requireValue(JSON.stringify(report.artifacts.map(a => a.name).sort()) === JSON.stringify(names), 'Incomplete target inventory');
    for (const a of report.artifacts) {
      const actual = await digest(join(directory, a.name));
      requireValue(actual.size === a.size && actual.sha256 === a.sha256, 'Candidate bytes changed after verification');
      copyFileSync(join(directory, a.name), join(output, a.name));
      reports.push({ name: a.name, ...actual });
    }
  }
  for (const name of ['LICENSE', 'NOTICE']) {
    const source = fileURLToPath(new URL(`../../${name}`,import.meta.url));
    copyFileSync(source,join(output,name));
    reports.push({name,...await digest(join(output,name))});
  }
  const index = { ...expected, artifacts: reports.sort((a,b) => a.name.localeCompare(b.name)), nativeAcceptance: 'unverified', platformSigning: 'developer-id-notarized-and-linux-digests' };
  validateIndex(index, expected);
  writeFileSync(join(output, 'delidev-release-index.json'), JSON.stringify(index, null, 2) + '\n', { flag: 'wx' });
  const sums = [...reports, { name: 'delidev-release-index.json', ...await digest(join(output, 'delidev-release-index.json')) }].sort((a,b) => a.name.localeCompare(b.name));
  writeFileSync(join(output, 'SHA256SUMS'), sums.map(a => `${a.sha256}  ${a.name}\n`).join(''), { flag: 'wx' });
  await verifyDirectory(output, expected);
}
export function releaseNotes(expected) {
  return `DeliDev ${expected.version}\n\nSource revision: ${expected.sourceRevision}\n\nDownload-only release for macOS and Linux (x64/arm64). Windows builds are skipped because production Windows signing is not configured. No update manifest is published; this release is not offered through the in-app or Worker updater.\n\nThe standalone Worker executable also provides the delidev CLI and server commands (run chmod +x on a downloaded standalone Unix executable before use). macOS artifacts are Developer ID signed and notarized. Linux artifacts have verified SHA-256 digests. Build/package checks do not establish installed-platform, WidgetKit, or real-account acceptance.\n`;
}
// Read every page, including drafts; a missing public tag lookup is not absence.
export async function findRelease(api, tag) {
  const found = [];
  for (let page = 1; page <= 1000; page++) {
    const items = await api(`releases?per_page=100&page=${page}`);
    requireValue(Array.isArray(items) && items.length <= 100 && items.every(r => Number.isSafeInteger(r?.id) && r.id > 0 && typeof r.tag_name === 'string'), 'Invalid GitHub release listing');
    found.push(...items.filter(r => r.tag_name === tag));
    if (items.length < 100) { requireValue(found.length <= 1, 'Duplicate DeliDev releases'); return found[0] ?? null; }
  }
  throw new Error('DeliDev release pagination limit');
}
export async function reconcile({ api, existing, index, expected, readAsset, upload }) {
  if (!existing) {
    // Reconcile an unknown creation response without blindly creating again.
    try { existing = await api('releases', { method: 'POST', body: { tag_name: expected.tag, target_commitish: expected.sourceRevision, name: `DeliDev v${expected.version}`, body: releaseNotes(expected), draft: true, prerelease: false } }); }
    catch { existing = await findRelease(api, expected.tag); requireValue(existing, 'Release creation outcome is unknown; inspect before retry'); }
  }
  requireValue(Number.isSafeInteger(existing.id) && existing.id > 0 && existing.tag_name === expected.tag && existing.target_commitish === expected.sourceRevision && existing.prerelease === false && existing.body === releaseNotes(expected), 'Conflicting DeliDev release ownership');
  const inventory = [...index.artifacts, { name: 'delidev-release-index.json', ...await readAsset('local-index') }, { name: 'SHA256SUMS', ...await readAsset('local-sums') }];
  const inspect = async () => {
    const assets = [];
    for (let page = 1; page <= 1000; page++) {
      const chunk = await api(`releases/${existing.id}/assets?per_page=100&page=${page}`);
      requireValue(Array.isArray(chunk) && chunk.length <= 100, 'Invalid release asset listing');
      assets.push(...chunk);
      if (chunk.length < 100) break;
      requireValue(page < 1000, 'Asset pagination limit');
    }
    requireValue(new Set(assets.map(a => a.name)).size === assets.length && assets.every(a => inventory.some(i => i.name === a.name)), 'Unexpected or duplicate release assets');
    for (const a of assets) {
      const wanted = inventory.find(i => i.name === a.name);
      const actual = await readAsset(a);
      requireValue(a.size === wanted.size && actual.size === wanted.size && actual.sha256 === wanted.sha256, 'Published or retained draft bytes conflict');
    }
    return assets;
  };
  let assets = await inspect();
  if (!existing.draft) { requireValue(assets.length === inventory.length, 'Incomplete public DeliDev release'); return 'reused'; }
  for (const a of inventory.filter(i => !assets.some(p => p.name === i.name))) {
    try { await upload(existing.id, a.name); }
    catch { assets = await inspect(); requireValue(assets.some(p => p.name === a.name), 'Asset upload outcome is unknown or failed; retry after inspection'); }
  }
  assets = await inspect();
  requireValue(assets.length === inventory.length, 'Incomplete DeliDev draft');
  try { await api(`releases/${existing.id}`, { method: 'PATCH', body: { draft: false, make_latest: 'false' } }); }
  catch { /* Read the original release below before deciding publication failed. */ }
  const published = await api(`releases/${existing.id}`);
  requireValue(published.draft === false && published.prerelease === false && published.tag_name === expected.tag && published.target_commitish === expected.sourceRevision && published.body === releaseNotes(expected), 'DeliDev publication outcome is unknown; preserve original release');
  await inspect();
  return 'published';
}
function gh(args, options = {}) {
  try { return execFileSync('gh', args, { encoding: 'utf8', stdio: ['pipe','pipe','pipe'], ...options }); }
  catch { throw new Error('DeliDev GitHub operation failed'); }
}
const api = async (route, options = {}) => JSON.parse(gh(['api', '--method', options.method ?? 'GET', `repos/delinoio/oss/${route}`, ...(options.body ? ['--input', '-'] : [])], options.body ? { input: JSON.stringify(options.body) } : {}));
function output(values) {
  for (const [key,value] of Object.entries(values)) if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${typeof value === 'object' ? JSON.stringify(value) : value}\n`);
  console.log(JSON.stringify({ component: 'delidev.release', ...values }));
}
export async function main(command) {
  const expected = identity(process.env.RELEASE_VERSION, process.env.RELEASE_REVISION);
  requireValue(process.env.GITHUB_REPOSITORY === 'delinoio/oss', 'DeliDev publication requires delinoio/oss');
  const head = execFileSync('git', ['rev-parse','HEAD'], { encoding: 'utf8' }).trim();
  requireValue(head === expected.sourceRevision && readVersion(Project.DeliDev) === expected.version, 'DeliDev checkout identity conflict');
  if (command === 'plan') {
    // No Windows signing backend is implemented. Never let a variable grant it.
    requireValue(!process.env.DELIDEV_WINDOWS_SIGNING_ENABLED || process.env.DELIDEV_WINDOWS_SIGNING_ENABLED === 'false', 'Windows production signing is not implemented');
    output({ matrix: { include: releaseTargets }, channel: expected.channel, windows: 'skipped' });
    return;
  }
  if (command === 'check-root') { signingRoot(); return; }
  const tag = await api(`git/ref/tags/${expected.tag}`);
  requireValue(tag.object?.type === 'commit' && tag.object.sha === expected.sourceRevision, 'DeliDev tag revision conflict');
  const existing = await findRelease(api, expected.tag);
  if (command === 'inspect') {
    if (existing) requireValue(Number.isSafeInteger(existing.id) && existing.id > 0 && existing.target_commitish === expected.sourceRevision && existing.prerelease === false && existing.body === releaseNotes(expected), 'Conflicting DeliDev release ownership');
    if (!existing || existing.draft) { output({ published: 'false' }); return; }
    const directory = resolve(process.env.RUNNER_TEMP, 'delidev-public-readback');
    mkdirSync(directory);
    gh(['release','download',expected.tag,'--repo','delinoio/oss','--dir',directory]);
    const index = await verifyDirectory(directory, expected);
    await reconcile({ api, existing, index, expected, readAsset: async a => digest(join(directory, a === 'local-index' ? 'delidev-release-index.json' : a === 'local-sums' ? 'SHA256SUMS' : a.name)), upload: async () => { throw new Error('Public release must be immutable'); } });
    output({ published: 'true' }); return;
  }
  if (command === 'assemble') { await assemble(process.env.RELEASE_INPUTS, process.env.RELEASE_OUTPUT, expected); return; }
  requireValue(command === 'publish', 'Unknown DeliDev release command');
  const directory = resolve(process.env.RELEASE_OUTPUT);
  const index = await verifyDirectory(directory, expected);
  const scratch = resolve(process.env.RUNNER_TEMP, 'delidev-asset-readback');
  mkdirSync(scratch);
  const outcome = await reconcile({ api, existing, expected, index, readAsset: async a => {
    if (a === 'local-index' || a === 'local-sums') return digest(join(directory, a === 'local-index' ? 'delidev-release-index.json' : 'SHA256SUMS'));
    requireValue(Number.isSafeInteger(a.id) && a.id > 0, 'Invalid GitHub asset ID');
    const file = join(scratch, String(a.id));
    // gh streams authenticated asset bytes to disk, not into memory or logs.
    const { openSync, closeSync } = await import('node:fs');
    const fd = openSync(file, 'w', 0o600);
    try { gh(['api', `repos/delinoio/oss/releases/assets/${a.id}`, '-H','Accept: application/octet-stream'], { stdio: ['ignore',fd,'pipe'] }); }
    finally { closeSync(fd); }
    return digest(file);
  }, upload: async (id, name) => {
    // Pin the original numeric release ID; do not rediscover a draft by tag.
    const { Readable } = await import('node:stream');
    const file = join(directory,name);
    requireValue(Boolean(process.env.GH_TOKEN), 'A release token is required');
    const response = await fetch(`https://uploads.github.com/repos/delinoio/oss/releases/${id}/assets?name=${encodeURIComponent(name)}`, {
      method: 'POST', headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, 'Content-Type': 'application/octet-stream', 'Content-Length': String(lstatSync(file).size), Accept: 'application/vnd.github+json' },
      body: Readable.toWeb(createReadStream(file)), duplex: 'half', redirect: 'error', signal: AbortSignal.timeout(20*60*1000),
    });
    await response.body?.cancel();
    requireValue(response.status === 201, 'DeliDev asset upload failed');
  } });
  output({ outcome, version: expected.version, revision: expected.sourceRevision, channel: expected.channel, windows: 'skipped' });
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main(process.argv[2]).catch(() => { console.error(JSON.stringify({ component: 'delidev.release', phase: process.argv[2], outcome: 'failed' })); process.exitCode = 1; });
