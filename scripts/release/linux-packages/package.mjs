import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import path from 'node:path';
import { randomBytes } from 'node:crypto';
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
  const exported = command('gpg', ['--batch', '--with-colons', '--with-subkey-fingerprint', '--list-secret-keys'], { env });
  requireValue(exported.split('\n').find((line) => line.startsWith('fpr:'))?.split(':')[9] === fingerprint, 'SIGNING_KEY_MISMATCH');
  const secretLines = exported.split('\n').map((line) => line.split(':'));
  const primaries = secretLines.filter((line) => line[0] === 'sec');
  requireValue(primaries.length === 1 && primaries[0][14] === '#', 'PRIMARY_SECRET_KEY_FORBIDDEN');
  const available = secretLines.flatMap((line, index) => line[0] === 'ssb' && line[14] === '+' && line[11].includes('s') ? [secretLines[index + 1]?.[9]] : []);
  requireValue(available.length === 1 && /^[A-F0-9]{40}$/u.test(available[0]), 'SIGNING_SUBKEY_AMBIGUOUS');
  const publicFile = path.join(directory, 'public.asc');
  writeFileSync(publicFile, publicKey);
  const publicDescription = command('gpg', ['--batch', '--with-colons', '--with-subkey-fingerprint', '--show-keys', publicFile], { env });
  requireValue(publicDescription.split('\n').find((line) => line.startsWith('fpr:'))?.split(':')[9] === fingerprint, 'PUBLIC_KEY_MISMATCH');
  // The primary fingerprint alone cannot detect a rotated signing subkey missing
  // from the certificate clients download. Verify with that certificate only.
  const verifyEnv = { ...process.env, GNUPGHOME: mkdtempSync(path.join(directory, 'verify-')) };
  command('gpg', ['--batch', '--no-autostart', '--import', publicFile], { env: verifyEnv });
  const publicLines = publicDescription.split('\n').map((line) => line.split(':'));
  requireValue(publicLines.filter((line) => line[0] === 'pub').length === 1, 'PUBLIC_KEY_MISMATCH');
  const publicSubkeys = publicLines.flatMap((line, index) => line[0] === 'sub' && line[11].includes('s') ? [publicLines[index + 1]?.[9]] : []).sort();
  const signing = { directory, keyFile, passFile, fingerprint, signer: available[0], publicSubkeys, publicKey: Buffer.from(publicKey), env, verifyEnv, passphrase };
  const probe = path.join(directory, 'certificate-probe');
  writeFileSync(probe, randomBytes(32));
  try { gpgSign(probe, signing); }
  catch { throw new Error('SIGNING_CERTIFICATE_MISMATCH'); }
  finally { rmSync(probe, { force: true }); rmSync(`${probe}.asc`, { force: true }); }
  return signing;
}
export function temporarySigningKey(directory) {
  // Generate the certification key in a separate home, then discard it before the
  // fixture publisher starts. Importing into the original home retains its primary.
  const primaryHome = `${directory}-primary`;
  mkdirSync(primaryHome, { recursive: true, mode: 0o700 });
  const env = { ...process.env, GNUPGHOME: primaryHome };
  const passphrase = randomBytes(24).toString('hex');
  const passFile = path.join(primaryHome, 'passphrase');
  writeFileSync(passFile, passphrase, { mode: 0o600 });
  try {
    const unlock = ['--pinentry-mode', 'loopback', '--passphrase-file', passFile];
    command('gpg', ['--batch', ...unlock, '--quick-generate-key', 'Delino Package Fixture', 'rsa2048', 'cert', '1d'], { env });
    const fingerprint = command('gpg', ['--batch', '--with-colons', '--list-secret-keys'], { env }).split('\n').find((line) => line.startsWith('fpr:')).split(':')[9];
    command('gpg', ['--batch', ...unlock, '--quick-add-key', fingerprint, 'rsa2048', 'sign', '1d'], { env });
    const secretKey = command('gpg', ['--batch', ...unlock, '--armor', '--export-secret-subkeys', fingerprint], { env });
    const publicKey = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
    return importSigningKey(directory, { secretKey, publicKey, fingerprint, passphrase });
  } finally { rmSync(primaryHome, { recursive: true, force: true }); }
}
export function gpgSign(file, signing, clearsign = false) {
  const output = `${file}.${clearsign ? 'clearsigned' : 'asc'}`;
  command('gpg', ['--batch', '--yes', '--pinentry-mode', 'loopback', '--passphrase-file', signing.passFile, '--local-user', `${signing.signer}!`, '--digest-algo', 'SHA256', '--armor', '--output', output, clearsign ? '--clearsign' : '--detach-sign', file], { env: signing.env });
  command('gpg', ['--batch', '--no-auto-key-retrieve', '--verify', output, ...(clearsign ? [] : [file])], { env: signing.verifyEnv });
  return output;
}
export function packageFiles(plan, input, output, signing) {
  mkdirSync(output, { recursive: true });
  const description = {
    binpm: 'Binary package manager for release assets', 'cargo-mono': 'Cargo subcommand for Rust monorepo management',
    nodeup: 'Node.js version manager', 'with-watch': 'Rerun commands when inputs change', derun: 'Terminal relay and MCP server', runmoor: 'Local ephemeral GitHub Actions runners', clibox: 'Cross-platform developer utilities',
  }[plan.project];
  const result = [];
  const license = 'Apache-2.0';
  // Preserve upstream declarations and include the complete applicable terms.
  const copyright = path.join(output, 'copyright');
  writeFileSync(copyright, `Upstream: https://github.com/delinoio/oss\nSource: ${plan.tag} (${plan.revision})\nDeclared package license: ${license}\n\n${readFileSync(`packaging/linux/licenses/${license}.txt`, 'utf8')}\n`);
  for (const arch of architectures) {
    for (const format of ['deb', 'rpm']) {
      const rpmArch = arch === 'amd64' ? 'x86_64' : 'aarch64';
      const name = format === 'deb' ? `${plan.project}_${plan.version}-1_${arch}.deb` : `${plan.project}-${plan.version}-1.${rpmArch}.rpm`;
      const config = { name: plan.project, arch, platform: 'linux', version: plan.version, release: '1', section: 'utils', priority: 'optional',
        maintainer: 'Delino', description, vendor: 'Delino', homepage: `https://github.com/delinoio/oss`, license,
        mtime: new Date(input.epoch * 1000).toISOString(), depends: [...dependencies(input.binaries[arch].libraries, format, plan.project), ...(format === 'deb' ? ['delino-archive-keyring (>= 1-1)'] : [])],
        contents: [
          { src: path.resolve(input.binaries[arch].file), dst: `/usr/bin/${plan.project}`, file_info: { mode: 0o755, mtime: new Date(input.epoch * 1000).toISOString() } },
          { src: path.resolve(copyright), dst: `/usr/share/doc/${plan.project}/copyright`, file_info: { mode: 0o644 } },
        ],
        deb: { compression: 'xz' },
        rpm: { compression: 'gzip' },
      };
      const configFile = path.join(output, `${arch}-${format}.json`);
      writeFileSync(configFile, encode(config));
      const target = path.join(output, name);
      command('nfpm', ['package', '--config', configFile, '--packager', format, '--target', target], { env: { ...process.env, SOURCE_DATE_EPOCH: `${input.epoch}` } });
      if (format === 'rpm') {
        // nFPM 2.47.0 decrypts subkeys only when the primary secret key is encrypted.
        // A CI export intentionally has a dummy primary, so use GnuPG through rpmsign.
        // Remove this adapter only after nFPM supports encrypted subkey-only exports
        // and the encrypted disposable-key lifecycle tests pass without it.
        command('rpmsign', ['--define', '__gpg /usr/bin/gpg', '--define', `_gpg_name ${signing.signer}!`,
          '--define', `_gpg_path ${signing.directory}`, '--define', '_gpg_digest_algo sha256',
          '--define', `_gpg_sign_cmd_extra_args --pinentry-mode loopback --passphrase-file ${signing.passFile}`,
          '--addsign', target], { env: signing.env });
      }
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
  command('gpg', ['--batch', '--no-auto-key-retrieve', '--verify', `${file}.asc`, file], { env: signing.verifyEnv });
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
