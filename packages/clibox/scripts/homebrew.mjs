import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { ensure, event, isMain, metadata, revision, root } from './common.mjs';
import { archiveNames, assetNames, readDarwinArchive, unsignedNames } from './github-release.mjs';
import { identity, sha256 } from '../../../scripts/release/linux-packages/model.mjs';
import { command, verifyBundle } from '../../../scripts/release/linux-packages/release-input.mjs';

const apiRoot = 'repos/delinoio/oss';
const Action = Object.freeze({ Render: 'render', Prepare: 'prepare', Publish: 'publish' });
export function publicationContext(plan, env = process.env) {
  ensure(env.GITHUB_ACTIONS === 'true' && env.GITHUB_REPOSITORY === 'delinoio/oss' && env.GITHUB_REF === `refs/tags/${plan.tag}` && env.GITHUB_SHA === plan.revision, 'Homebrew publication requires the exact first-party tag and commit');
}

export async function publishedRelease(plan, api) {
  let object = (await api(`${apiRoot}/git/ref/tags/${encodeURIComponent(plan.tag)}`)).object;
  for (let depth = 0; object?.type === 'tag' && depth < 4; depth++) {
    ensure(/^[a-f0-9]{40}$/u.test(object.sha), 'Invalid annotated tag');
    object = (await api(`${apiRoot}/git/tags/${object.sha}`)).object;
  }
  ensure(object?.type === 'commit' && object.sha === plan.revision, 'Homebrew tag revision mismatch');
  const sources = new Map();
  for (const file of ['crates/clibox/Cargo.toml', 'packages/clibox/package.json']) {
    const source = await api(`${apiRoot}/contents/${file}?ref=${plan.revision}`);
    ensure(source.encoding === 'base64' && typeof source.content === 'string', 'Invalid release source');
    sources.set(file, Buffer.from(source.content, 'base64').toString('utf8'));
  }
  ensure(metadata((file) => sources.get(file)).version === plan.version, 'Homebrew source version mismatch');
  const release = await api(`${apiRoot}/releases/tags/${encodeURIComponent(plan.tag)}`);
  ensure(Number.isSafeInteger(release.id) && release.id > 0 && release.tag_name === plan.tag && release.target_commitish === plan.revision && release.draft === false && release.prerelease === false, 'Homebrew release identity mismatch');
  ensure(Array.isArray(release.assets) && JSON.stringify(release.assets.map(({ name }) => name).sort()) === JSON.stringify([...assetNames].sort()), 'Homebrew release asset inventory mismatch');
  ensure(new Set(release.assets.map(({ id }) => id)).size === assetNames.length && release.assets.every((asset) => Number.isSafeInteger(asset.id) && asset.id > 0 && asset.state === 'uploaded' && Number.isSafeInteger(asset.size) && asset.size > 0 && asset.size <= 64 * 1024 * 1024 && /^sha256:[a-f0-9]{64}$/u.test(asset.digest ?? '')), 'Invalid Homebrew asset metadata');
  event('homebrew_release_verified', { version: plan.version, revision: plan.revision, release_id: release.id });
  return release;
}

export function verifyArchives(directory) {
  const hashes = new Map(archiveNames.map((name) => [name, sha256(readFileSync(path.join(directory, name)))]));
  ensure(readFileSync(path.join(directory, 'SHA256SUMS'), 'utf8') === [...hashes].map(([name, hash]) => `${hash}  ${name}\n`).join(''), 'Homebrew checksum manifest mismatch');
  for (const arch of ['amd64', 'arm64']) readDarwinArchive(readFileSync(path.join(directory, `clibox-darwin-${arch}.tar.gz`)), arch);
  return hashes;
}

export function renderHomebrew(plan, directory, publish = false) {
  const hashes = verifyArchives(directory);
  const args = [path.join(root, 'scripts/release/update-homebrew.sh'), '--project', 'clibox', '--version', plan.version];
  for (const arch of ['amd64', 'arm64']) {
    const name = `clibox-darwin-${arch}.tar.gz`;
    args.push(`--darwin-${arch}-url`, `https://github.com/delinoio/oss/releases/download/${plan.tag}/${name}`, `--darwin-${arch}-sha256`, hashes.get(name));
  }
  if (!publish) args.push('--dry-run');
  return execFileSync('bash', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] });
}

export async function prepareHomebrew(plan, directory, {
  api = async (endpoint) => JSON.parse(command('gh', ['api', endpoint], { timeout: 120000 })),
  download = async (asset) => command('gh', ['api', `${apiRoot}/releases/assets/${asset.id}`, '-H', 'Accept: application/octet-stream'], { encoding: null, maxBuffer: 64 * 1024 * 1024, timeout: 120000 }),
  verify = verifyBundle,
  render = renderHomebrew,
} = {}) {
  const release = await publishedRelease(plan, api);
  for (const asset of release.assets) {
    const bytes = await download(asset);
    ensure(bytes.length === asset.size && `sha256:${sha256(bytes)}` === asset.digest, 'Homebrew asset digest mismatch');
    writeFileSync(path.join(directory, asset.name), bytes, { flag: 'wx' });
  }
  for (const name of unsignedNames) await verify(path.join(directory, name), path.join(directory, `${name}.sigstore.json`), plan);
  event('homebrew_signatures_verified', { version: plan.version, revision: plan.revision });
  return render(plan, directory);
}

export function verifyTestedFormula(formula, directory) {
  for (const arch of ['amd64', 'arm64']) ensure(readFileSync(path.join(directory, arch, 'clibox.rb'), 'utf8') === formula, 'Native-tested Homebrew formula changed');
}

export async function main(args = process.argv.slice(2), env = process.env) {
  const { values, positionals } = parseArgs({ args, allowPositionals: true, options: { output: { type: 'string' }, archives: { type: 'string' }, validated: { type: 'string' } } });
  ensure(positionals.length === 1 && Object.values(Action).includes(positionals[0]), 'Expected render, prepare or publish');
  const [action] = positionals;
  const plan = identity({ project: 'clibox', version: metadata().version, revision: revision() });
  if (action === Action.Publish) {
    publicationContext(plan, env);
    ensure(values.validated && env.HOMEBREW_TAP_GH_TOKEN, 'Native validation evidence and tap token required');
  } else ensure(values.output, 'Formula output required');
  if (action === Action.Render) {
    ensure(values.archives, 'Candidate archives required');
    writeFileSync(values.output, renderHomebrew(plan, values.archives));
    return;
  }
  const directory = mkdtempSync(path.join(tmpdir(), 'clibox-homebrew-'));
  try {
    const formula = await prepareHomebrew(plan, directory);
    if (action === Action.Prepare) {
      if (values.validated) verifyTestedFormula(formula, values.validated);
      writeFileSync(values.output, formula);
    } else {
      // Reverify public bytes after native tests and immediately before the tap write.
      verifyTestedFormula(formula, values.validated);
      process.stdout.write(renderHomebrew(plan, directory, true));
      event('homebrew_published', { version: plan.version, revision: plan.revision });
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

if (isMain(import.meta.url)) main().catch((error) => { console.error(JSON.stringify({ event: 'clibox_homebrew_failed', message: error.message })); process.exitCode = 1; });
