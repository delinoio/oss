// Run inside the disposable Linux tools image; never use production key material.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, rmSync, readdirSync, copyFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { command } from './release-input.mjs';
import { importSigningKey } from './package.mjs';
import { prepareKeyring } from './keyring.mjs';
import { buildRepositories } from './repository.mjs';
import { FileStore } from './store.mjs';

const work = mkdtempSync(path.join(tmpdir(), 'dl-rotation-'));
try {
  const primary = path.join(work, 'primary'); mkdirSync(primary, { mode: 0o700 });
  const env = { ...process.env, GNUPGHOME: primary };
  const passphrase = 'disposable-rotation-test';
  const passFile = path.join(primary, 'passphrase'); writeFileSync(passFile, passphrase, { mode: 0o600 });
  const unlock = ['--pinentry-mode', 'loopback', '--passphrase-file', passFile];
  command('gpg', ['--batch', ...unlock, '--quick-generate-key', 'APT Rotation Fixture', 'rsa2048', 'cert', '1d'], { env });
  const fingerprint = command('gpg', ['--batch', '--with-colons', '--list-secret-keys'], { env }).split('\n').find((line) => line.startsWith('fpr:')).split(':')[9];
  command('gpg', ['--batch', ...unlock, '--quick-add-key', fingerprint, 'rsa2048', 'sign', '1d'], { env });
  const oldSecret = command('gpg', ['--batch', ...unlock, '--armor', '--export-secret-subkeys', fingerprint], { env });
  const oldPublic = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
  command('gpg', ['--batch', ...unlock, '--quick-add-key', fingerprint, 'rsa2048', 'sign', '1d'], { env });
  const fingerprints = command('gpg', ['--batch', '--with-colons', '--with-subkey-fingerprint', '--list-secret-keys'], { env }).split('\n').filter((line) => line.startsWith('fpr:')).map((line) => line.split(':')[9]);
  const newSecret = command('gpg', ['--batch', ...unlock, '--armor', '--export-secret-subkeys', `${fingerprints.at(-1)}!`], { env });
  const newPublic = command('gpg', ['--batch', '--armor', '--export', fingerprint], { env });
  const stages = [
    { secretKey: oldSecret, publicKey: oldPublic, version: 1 },
    { secretKey: oldSecret, publicKey: newPublic, version: 2 },
    { secretKey: newSecret, publicKey: newPublic, version: 2 },
  ];
  const client = path.join(work, 'client');
  for (const directory of ['var/lib/dpkg', 'usr/share/keyrings', 'apt/lists/partial', 'apt/cache/archives/partial', 'downloads']) mkdirSync(path.join(client, directory), { recursive: true });
  writeFileSync(path.join(client, 'var/lib/dpkg/status'), '');
  const trusted = path.join(client, 'usr/share/keyrings/delino-packages.gpg');
  const source = path.join(client, 'sources.list');
  const apt = (args) => command('apt-get', ['-o', `Dir::Etc::sourcelist=${source}`, '-o', 'Dir::Etc::sourceparts=-',
    '-o', `Dir::State=${client}/apt`, '-o', `Dir::State::status=${client}/var/lib/dpkg/status`, '-o', `Dir::Cache=${client}/apt/cache`,
    '-o', 'APT::Sandbox::User=root', ...args], { cwd: path.join(client, 'downloads') });
  const state = new FileStore(path.join(work, 'state'));
  for (const [index, stage] of stages.entries()) {
    const signing = importSigningKey(path.join(work, `signing-${index}`), { ...stage, fingerprint, passphrase });
    const keyring = await prepareKeyring(state, signing, signing.directory, stage.version);
    if (index === 1) {
      await assert.rejects(prepareKeyring(state, signing, signing.directory, 1), /KEYRING_IDENTITY_CONFLICT/u);
    }
    const root = path.join(work, `repository-${index}`);
    const files = await buildRepositories([], () => { throw new Error('unexpected CLI'); }, path.join(work, `build-${index}`), signing, `${index + 1}`.repeat(64), keyring);
    for (const file of files) { const destination = path.join(root, file.key); mkdirSync(path.dirname(destination), { recursive: true }); writeFileSync(destination, file.bytes); }
    if (index === 0) copyFileSync(path.join(signing.directory, 'keyring/delino-packages.gpg'), trusted);
    // Preview-only clients must also receive the keyring, without enabling stable.
    writeFileSync(source, `deb [signed-by=${trusted}] file:${root}/apt preview main\n`);
    apt(['update', '--error-on=any']);
    if (index < 2) {
      apt(['download', `delino-archive-keyring=${stage.version}-1`]);
      const downloaded = readdirSync(path.join(client, 'downloads')).find((name) => name.endsWith('.deb'));
      command('dpkg', [`--root=${client}`, '--install', path.join(client, 'downloads', downloaded)]);
      rmSync(path.join(client, 'downloads', downloaded));
      assert.equal(command('dpkg-query', [`--admindir=${client}/var/lib/dpkg`, '-W', '-f=${Version}', 'delino-archive-keyring']), `${stage.version}-1`);
    } else {
      // The existing client can verify the new signer without another key download.
      const installed = readFileSync(trusted);
      const stale = path.join(work, 'stale.asc'); writeFileSync(stale, oldPublic);
      command('gpg', ['--batch', '--yes', '--dearmor', '--output', trusted, stale]);
      assert.throws(() => apt(['update', '--error-on=any']));
      writeFileSync(trusted, installed);
      apt(['update', '--error-on=any']);
    }
  }
  console.log('APT keyring upgrade and signing-subkey rotation verified');
} catch (error) {
  console.error(error.cause?.stderr?.toString() ?? error.stack);
  process.exitCode = 1;
} finally { rmSync(work, { recursive: true, force: true }); }
