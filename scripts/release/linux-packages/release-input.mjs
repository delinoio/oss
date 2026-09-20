import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { architectures, requireValue, validateRelease, checksumFor, sha256, extractExecutable, inspectElf, sourceIdentity } from './model.mjs';

export function command(executable, args, options = {}) {
  try { return execFileSync(executable, args, { encoding: 'utf8', maxBuffer: 300 * 1024 * 1024, stdio: ['pipe', 'pipe', 'pipe'], ...options }); }
  catch (error) { throw new Error(`COMMAND_FAILED_${path.basename(executable).toUpperCase().replaceAll(/[^A-Z0-9]/gu, '_')}`, { cause: error }); }
}
export function readElf(file, architecture) {
  return inspectElf(readFileSync(file), architecture, command('readelf', ['--version-info', file]), command('readelf', ['--dynamic', file]), command('readelf', ['--notes', file]));
}
export function verifyBundle(file, bundle, plan) {
  const workflow = `https://github.com/delinoio/oss/.github/workflows/release-${plan.project}.yml`;
  // Both supported release entrypoints bind signatures to the same owned workflow.
  const ref = `(?:refs/tags/${plan.tag.replaceAll('.', '\\.')}|refs/heads/main)`;
  command('cosign', ['verify-blob', '--bundle', bundle, '--certificate-identity-regexp', `^${workflow.replaceAll('.', '\\.')}@${ref}$`, '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', '--certificate-github-workflow-sha', plan.revision, '--certificate-github-workflow-repository', 'delinoio/oss', file]);
}
export function downloadRelease(plan, output) {
  const api = (endpoint) => JSON.parse(command('gh', ['api', endpoint]));
  let object = api(`repos/delinoio/oss/git/ref/tags/${encodeURIComponent(plan.tag)}`).object;
  for (let depth = 0; object.type === 'tag' && depth < 4; depth++) {
    requireValue(/^[a-f0-9]{40}$/u.test(object.sha), 'INVALID_TAG_OBJECT');
    object = api(`repos/delinoio/oss/git/tags/${object.sha}`).object;
  }
  requireValue(object.type === 'commit', 'INVALID_TAG_OBJECT');
  const release = api(`repos/delinoio/oss/releases/tags/${encodeURIComponent(plan.tag)}`);
  const files = validateRelease(release, plan, object.sha);
  // A contract introduced after historical releases is the explicit no-backfill boundary.
  const contract = api(`repos/delinoio/oss/contents/packaging/linux/pins.json?ref=${plan.revision}`);
  const checked = JSON.parse(Buffer.from(contract.content, 'base64'));
  requireValue(checked.schema_version === 1 && checked.origin === 'https://pkgs.oss.delino.io' && checked.rust_image?.includes('@sha256:'), 'RELEASE_PREDATES_PACKAGE_SUPPORT');
  const sourceFile = ['derun', 'runmoor'].includes(plan.project)
    ? (plan.project === 'derun' ? 'cmds/derun/internal/version/version.go' : 'cmds/runmoor/internal/runmoor/types.go')
    : `crates/${plan.project}/Cargo.toml`;
  const sourceResponse = api(`repos/delinoio/oss/contents/${sourceFile}?ref=${plan.revision}`);
  const sourceText = Buffer.from(sourceResponse.content, 'base64').toString();
  const sourceVersion = ['derun', 'runmoor'].includes(plan.project)
    ? sourceText.match(/^const Version = "([^"]+)"$/mu)?.[1]
    : sourceText.match(/^version = "([^"]+)"$/mu)?.[1];
  requireValue(sourceVersion === plan.version, 'SOURCE_VERSION_MISMATCH');
  const workflowResponse = api(`repos/delinoio/oss/contents/.github/workflows/release-${plan.project}.yml?ref=${plan.revision}`);
  requireValue(Buffer.from(workflowResponse.content, 'base64').toString().includes('./.github/workflows/release-linux-packages.yml'), 'RELEASE_PREDATES_PACKAGE_SUPPORT');
  const commit = api(`repos/delinoio/oss/commits/${plan.revision}`);
  const epoch = Math.floor(Date.parse(commit.commit.committer.date) / 1000);
  requireValue(Number.isSafeInteger(epoch) && epoch > 0, 'INVALID_RELEASE_TIME');
  mkdirSync(output, { recursive: true });
  command('gh', ['release', 'download', plan.tag, '--repo', 'delinoio/oss', '--dir', output, ...files.flatMap((file) => ['--pattern', file])]);
  const manifest = path.join(output, 'SHA256SUMS');
  verifyBundle(manifest, `${manifest}.sigstore.json`, plan);
  const hashes = {}; const binaries = {};
  for (const arch of architectures) {
    const name = `${plan.project}-linux-${arch}.tar.gz`;
    const file = path.join(output, name);
    const bytes = readFileSync(file);
    hashes[arch] = checksumFor(readFileSync(manifest, 'utf8'), name);
    requireValue(sha256(bytes) === hashes[arch], 'ARCHIVE_CHECKSUM_MISMATCH');
    verifyBundle(file, `${file}.sigstore.json`, plan);
    const executable = path.join(output, `${plan.project}-${arch}`);
    writeFileSync(executable, extractExecutable(bytes, plan.project), { mode: 0o755 });
    binaries[arch] = { file: executable, libraries: readElf(executable, arch), sha256: sha256(readFileSync(executable)) };
  }
  return { epoch, hashes, binaries, source: sourceIdentity(hashes, plan) };
}
