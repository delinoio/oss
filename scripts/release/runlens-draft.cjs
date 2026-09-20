const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

const payloads = ['SHA256SUMS', ...['darwin', 'linux', 'windows'].flatMap(os =>
  ['amd64', 'arm64'].map(arch => `runlens-${os}-${arch}.${os === 'windows' ? 'zip' : 'tar.gz'}`))];
const files = payloads.flatMap(name => [name, `${name}.sigstore.json`]);
const digest = data => `sha256:${crypto.createHash('sha256').update(data).digest('hex')}`;

function assertMatchingDraft(release, tag, sha) {
  if (!release.draft || release.prerelease || release.tag_name !== tag || release.target_commitish !== sha) {
    throw Error('Only an unpublished draft at the exact tag and commit can be resumed');
  }
}

function verifyBundle(bundle, payload, tag) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'runlens-signature-'));
  try {
    const file = path.join(directory, 'bundle.json');
    fs.writeFileSync(file, bundle, { mode: 0o600 });
    execFileSync('cosign', ['verify-blob', '--bundle', file, '--certificate-identity',
      `https://github.com/delinoio/oss/.github/workflows/release-runlens.yml@refs/tags/${tag}`,
      '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', payload], { stdio: 'pipe' });
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

function expectedInventory(directory) {
  return new Map(files.map(name => {
    const data = fs.readFileSync(path.join(directory, name));
    return [name, { size: data.length, digest: digest(data) }];
  }));
}

async function verifyExisting({ github, context, tag, directory, verify, expected, assets }) {
  const names = new Set();
  for (const asset of assets) {
    if (!expected.has(asset.name) || names.has(asset.name) || asset.state !== 'uploaded') {
      throw Error('Unexpected, duplicate, or incomplete draft asset');
    }
    names.add(asset.name);
    if (asset.name.endsWith('.sigstore.json')) {
      if (asset.size <= 0 || asset.size > 1024 * 1024) throw Error('Invalid retained signature size');
      const { data } = await github.rest.repos.getReleaseAsset({ ...context.repo, asset_id: asset.id,
        headers: { accept: 'application/octet-stream' } });
      const bundle = Buffer.from(data);
      if (bundle.length !== asset.size || digest(bundle) !== asset.digest) throw Error('Retained signature digest mismatch');
      // Re-signing produces different bundle bytes. Authenticate retained bytes
      // against the identical payload/tag instead of trusting the new sidecar.
      await verify(bundle, path.join(directory, asset.name.slice(0, -'.sigstore.json'.length)), tag);
      expected.set(asset.name, { size: bundle.length, digest: digest(bundle) });
    }
    const wanted = expected.get(asset.name);
    if (asset.size !== wanted.size || asset.digest !== wanted.digest) throw Error('Retained draft asset mismatch');
  }
  return names;
}

function assertCompleteInventory(assets, expected) {
  if (assets.length !== files.length || new Set(assets.map(asset => asset.name)).size !== files.length
      || assets.some(asset => asset.state !== 'uploaded' || asset.size !== expected.get(asset.name)?.size
        || asset.digest !== expected.get(asset.name)?.digest)) {
    throw Error('Incomplete or mismatched uploaded assets; draft retained for inspection');
  }
}

async function prepareDraft({ github, context, tag, directory, verify = verifyBundle }) {
  const expected = expectedInventory(directory);
  let release;
  try {
    ({ data: release } = await github.rest.repos.getReleaseByTag({ ...context.repo, tag }));
    assertMatchingDraft(release, tag, context.sha);
  } catch (error) {
    if (error.status !== 404) throw error;
    ({ data: release } = await github.rest.repos.createRelease({ ...context.repo,
      tag_name: tag, target_commitish: context.sha, draft: true, generate_release_notes: true }));
  }
  const list = () => github.paginate(github.rest.repos.listReleaseAssets, { ...context.repo, release_id: release.id });
  // Check every retained asset before adding anything. Never delete or replace
  // mismatches: a failed candidate remains private for operator inspection.
  const names = await verifyExisting({ github, context, tag, directory, verify, expected, assets: await list() });
  for (const name of files) {
    if (!names.has(name)) await github.rest.repos.uploadReleaseAsset({ ...context.repo,
      release_id: release.id, name, data: fs.readFileSync(path.join(directory, name)) });
  }
  assertCompleteInventory(await list(), expected);
  return release;
}

async function publishDraft({ github, context, releaseId, tag, directory, verify = verifyBundle }) {
  if (!Number.isSafeInteger(releaseId) || releaseId <= 0) throw Error('Missing draft release identity');
  const identity = async () => {
    const { data: release } = await github.rest.repos.getRelease({ ...context.repo, release_id: releaseId });
    assertMatchingDraft(release, tag, context.sha);
  };
  await identity();
  const expected = expectedInventory(directory);
  const list = () => github.paginate(github.rest.repos.listReleaseAssets, { ...context.repo, release_id: releaseId });
  const assets = await list();
  if (assets.length !== files.length) throw Error('Incomplete draft inventory before publication');
  await verifyExisting({ github, context, tag, directory, verify, expected, assets });
  // Authentication downloads can take time too. Re-list after them and reject
  // replacement IDs, even when the new object's name/size/digest is identical.
  const current = await list();
  assertCompleteInventory(current, expected);
  const verifiedIds = new Map(assets.map(asset => [asset.name, asset.id]));
  if (current.some(asset => verifiedIds.get(asset.name) !== asset.id)) {
    throw Error('Draft asset replaced during publication verification');
  }
  await identity();
  return github.rest.repos.updateRelease({ ...context.repo, release_id: releaseId, draft: false });
}

module.exports = { assertMatchingDraft, prepareDraft, publishDraft, files };
