import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { architectures, dependencies, encode, sha256, requireValue, validateIdentity, pins } from './model.mjs';
import { command } from './release-input.mjs';

export function importSigningKey(directory, { secretKey, passphrase, publicKey, fingerprint }) {
  requireValue(/^[A-F0-9]{40}$/u.test(fingerprint ?? ''), 'INVALID_SIGNING_FINGERPRINT');
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const keyFile = path.join(directory, 'signing-key.asc');
  const passFile = path.join(directory, 'passphrase');
  writeFileSync(keyFile, secretKey, { mode: 0o600 });
  writeFileSync(passFile, passphrase, { mode: 0o600 });
  const env = { ...process.env, GNUPGHOME: directory };
  command('gpg', ['--batch', '--import', keyFile], { env });
  const exported = command('gpg', ['--batch', '--with-colons', '--fingerprint', '--list-secret-keys'], { env });
  requireValue(exported.split('\n').find((line) => line.startsWith('fpr:'))?.split(':')[9] === fingerprint, 'SIGNING_KEY_MISMATCH');
  const publicFile = path.join(directory, 'public.asc');
  writeFileSync(publicFile, publicKey);
  const publicDescription = command('gpg', ['--batch', '--with-colons', '--show-keys', publicFile], { env });
  requireValue(publicDescription.split('\n').find((line) => line.startsWith('fpr:'))?.split(':')[9] === fingerprint, 'PUBLIC_KEY_MISMATCH');
  return { directory, keyFile, passFile, fingerprint, publicKey: Buffer.from(publicKey), env, passphrase };
}
export function temporarySigningKey(directory) {
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const env = { ...process.env, GNUPGHOME: directory };
  command('gpg', ['--batch', '--pinentry-mode', 'loopback', '--passphrase', '', '--quick-generate-key', 'Delino Package Fixture', 'rsa2048', 'cert', '1d'], { env });
  const fingerprint = command('gpg', ['--batch', '--with-colons', '--list-secret-keys'], { env }).split('\n').find((line) => line.startsWith('fpr:')).split(':')[9];
  command('gpg', ['--batch', '--pinentry-mode', 'loopback', '--passphrase', '', '--quick-add-key', fingerprint, 'rsa2048', 'sign', '1d'], { env });
  const secretKey = command('gpg', ['--batch', '--armor', '--export-secret-subkeys', fingerprint], { env });
  const publicKey = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
  return importSigningKey(directory, { secretKey, publicKey, fingerprint, passphrase: '' });
}
export function gpgSign(file, signing, clearsign = false) {
  const output = `${file}.${clearsign ? 'clearsigned' : 'asc'}`;
  command('gpg', ['--batch', '--yes', '--pinentry-mode', 'loopback', '--passphrase-file', signing.passFile, '--local-user', signing.fingerprint, '--digest-algo', 'SHA256', '--armor', '--output', output, clearsign ? '--clearsign' : '--detach-sign', file], { env: signing.env });
  command('gpg', ['--batch', '--verify', output, ...(clearsign ? [] : [file])], { env: signing.env });
  return output;
}
export function packageFiles(plan, input, output, signing) {
  mkdirSync(output, { recursive: true });
  const description = {
    binpm: 'Binary package manager for release assets', 'cargo-mono': 'Cargo subcommand for Rust monorepo management',
    nodeup: 'Node.js version manager', 'with-watch': 'Rerun commands when inputs change', derun: 'Terminal relay and MCP server', runmoor: 'Local ephemeral GitHub Actions runners (preview)',
  }[plan.project];
  const result = [];
  const license = ['derun', 'runmoor'].includes(plan.project) ? 'Apache-2.0' : 'MIT';
  // Preserve upstream declarations and the repository license verbatim; packaging does not relicense source.
  const copyright = path.join(output, 'copyright');
  writeFileSync(copyright, `Upstream: https://github.com/delinoio/oss\nSource: ${plan.tag} (${plan.revision})\nDeclared package license: ${license}\n\nRepository LICENSE (unmodified):\n${readFileSync('LICENSE', 'utf8')}\n`);
  for (const arch of architectures) {
    for (const format of ['deb', 'rpm']) {
      const rpmArch = arch === 'amd64' ? 'x86_64' : 'aarch64';
      const name = format === 'deb' ? `${plan.project}_${plan.version}-1_${arch}.deb` : `${plan.project}-${plan.version}-1.${rpmArch}.rpm`;
      const config = { name: plan.project, arch, platform: 'linux', version: plan.version, release: '1', section: 'utils', priority: 'optional',
        maintainer: 'Delino', description, vendor: 'Delino', homepage: `https://github.com/delinoio/oss`, license,
        mtime: new Date(input.epoch * 1000).toISOString(), depends: dependencies(input.binaries[arch].libraries, format),
        contents: [
          { src: path.resolve(input.binaries[arch].file), dst: `/usr/bin/${plan.project}`, file_info: { mode: 0o755, mtime: new Date(input.epoch * 1000).toISOString() } },
          { src: path.resolve(copyright), dst: `/usr/share/doc/${plan.project}/copyright`, file_info: { mode: 0o644 } },
        ],
        deb: { compression: 'xz' },
        rpm: { compression: 'gzip', signature: { key_file: signing.keyFile } },
      };
      const configFile = path.join(output, `${arch}-${format}.json`);
      writeFileSync(configFile, encode(config));
      const target = path.join(output, name);
      command('nfpm', ['package', '--config', configFile, '--packager', format, '--target', target], { env: { ...process.env, NFPM_RPM_PASSPHRASE: signing.passphrase, SOURCE_DATE_EPOCH: `${input.epoch}` } });
      const bytes = readFileSync(target);
      result.push({ name, architecture: arch, format, sha256: sha256(bytes), size: bytes.length, bytes });
    }
  }
  return result;
}
export function candidateRecord(plan, input, files, signing) {
  return { schema_version: 1, identity: plan, source: input.source, source_archives: input.hashes, fingerprint: signing.fingerprint,
    binaries: Object.fromEntries(architectures.map((arch) => [arch, input.binaries[arch].sha256])),
    tools: sha256(encode(pins)), files: files.map(({ bytes: _bytes, ...file }) => file) };
}
export function validateCandidate(record) {
  requireValue(record?.schema_version === 1, 'INVALID_CANDIDATE');
  validateIdentity(record.identity);
  requireValue(/^[a-f0-9]{64}$/u.test(record.source ?? '') && /^[A-F0-9]{40}$/u.test(record.fingerprint ?? ''), 'INVALID_CANDIDATE');
  requireValue(Array.isArray(record.files) && record.files.length === 4, 'INVALID_PACKAGE_INVENTORY');
  for (const arch of architectures) for (const format of ['deb', 'rpm']) {
    const files = record.files.filter((file) => file.architecture === arch && file.format === format);
    requireValue(files.length === 1, 'INVALID_PACKAGE_INVENTORY');
    const file = files[0];
    const { project, version } = record.identity;
    const expected = format === 'deb' ? `${project}_${version}-1_${arch}.deb` : `${project}-${version}-1.${arch === 'amd64' ? 'x86_64' : 'aarch64'}.rpm`;
    requireValue(file.name === expected && /^[a-f0-9]{64}$/u.test(file.sha256 ?? '') && Number.isSafeInteger(file.size) && file.size > 0 && file.size <= 512 * 1024 * 1024, 'INVALID_PACKAGE_FILE');
  }
  return record;
}

export function signRecord(record, signing, directory) {
  const file = path.join(directory, 'record.json');
  const payload = { ...record, fingerprint: signing.fingerprint };
  writeFileSync(file, encode(payload));
  return { ...payload, signature: readFileSync(gpgSign(file, signing), 'utf8') };
}
export function verifyRecord(record, signing, directory) {
  requireValue(record.fingerprint === signing.fingerprint && typeof record.signature === 'string', 'UNTRUSTED_RECORD');
  const { signature, ...payload } = record;
  const file = path.join(directory, 'verify-record.json');
  writeFileSync(file, encode(payload));
  writeFileSync(`${file}.asc`, signature);
  command('gpg', ['--batch', '--verify', `${file}.asc`, file], { env: signing.env });
  return record;
}
export function signCandidate(record, signing, directory) {
  validateCandidate(record);
  return signRecord(record, signing, directory);
}
export function verifyCandidate(record, signing, directory) {
  validateCandidate(record);
  return verifyRecord(record, signing, directory);
}
