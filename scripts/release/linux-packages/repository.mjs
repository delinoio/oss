import { readFileSync, writeFileSync, mkdirSync, readdirSync, statSync, copyFileSync } from 'node:fs';
import path from 'node:path';
import { architectures, Channel, origin, encode, requireValue, setupFiles, sha256, safeKey } from './model.mjs';
import { command } from './release-input.mjs';
import { gpgSign } from './package.mjs';
import { Cache } from './store.mjs';
import { packageKeyring } from './keyring.mjs';

function listFiles(root) {
  return readdirSync(root, { recursive: true }).filter((name) => statSync(path.join(root, name)).isFile()).sort();
}
export async function buildRepositories(records, loadPackage, directory, signing, generation, keyring) {
  requireValue(/^[a-f0-9]{64}$/u.test(generation), 'INVALID_GENERATION');
  const files = [];
  const add = (key, bytes, mutable = false) => files.push({ key: safeKey(key), bytes, sha256: sha256(bytes), mutable, cache: mutable ? Cache.Mutable : Cache.Immutable });
  mkdirSync(directory, { recursive: true });
  keyring ??= packageKeyring(signing, path.join(directory, 'keyring'));
  const aptlyRoot = path.join(directory, 'aptly');
  const config = path.join(directory, 'aptly.json');
  writeFileSync(config, encode({ rootDir: aptlyRoot, architectures, skipLegacyPool: true }));
  const apt = (args) => command('aptly', ['-config', config, ...args], { env: signing.env });
  for (const channel of Object.values(Channel)) {
    apt(['repo', 'create', '-distribution', channel, '-component', 'main', channel]);
    const channelRecords = records.filter((record) => record.identity.channel === channel);
    const imported = path.join(directory, 'packages', channel);
    mkdirSync(imported, { recursive: true });
    const keyringFile = path.join(imported, keyring.file.name);
    requireValue(sha256(keyring.bytes) === keyring.file.sha256, 'KEYRING_STORAGE_CORRUPT');
    writeFileSync(keyringFile, keyring.bytes);
    apt(['repo', 'add', channel, keyringFile]);
    for (const record of channelRecords) for (const file of record.files) {
      const bytes = await loadPackage(file);
      requireValue(sha256(bytes) === file.sha256 && bytes.length === file.size, 'PACKAGE_STORAGE_CORRUPT');
      writeFileSync(path.join(imported, file.name), bytes);
      if (file.format === 'deb') apt(['repo', 'add', channel, path.join(imported, file.name)]);
    }
    apt(['snapshot', 'create', channel, 'from', 'repo', channel]);
    apt(['publish', 'snapshot', '-batch', '-skip-signing', '-acquire-by-hash', '-architectures=amd64,arm64', `-distribution=${channel}`, '-component=main', '-origin=Delino', '-label=Delino', ...(channel === Channel.Preview ? ['-notautomatic=yes', '-butautomaticupgrades=yes'] : []), channel]);
    const release = path.join(aptlyRoot, 'public', 'dists', channel, 'Release');
    const signed = gpgSign(release, signing, true);
    add(`apt/dists/${channel}/InRelease`, readFileSync(signed), true);
    for (const arch of architectures) {
      const rpmArch = arch === 'amd64' ? 'x86_64' : 'aarch64';
      const rpmRoot = path.join(directory, 'rpm', channel, rpmArch);
      const packageRoot = path.join(rpmRoot, 'Packages');
      mkdirSync(packageRoot, { recursive: true });
      for (const record of channelRecords) for (const file of record.files.filter((file) => file.format === 'rpm' && file.architecture === arch)) copyFileSync(path.join(imported, file.name), path.join(packageRoot, file.name));
      command('createrepo_c', ['--checksum', 'sha256', '--compress-type', 'gz', '--general-compress-type', 'gz', '--no-database', '--unique-md-filenames', rpmRoot]);
      gpgSign(path.join(rpmRoot, 'repodata', 'repomd.xml'), signing);
      for (const name of listFiles(rpmRoot)) add(`rpm/snapshots/${generation}/${channel}/${rpmArch}/${name}`, readFileSync(path.join(rpmRoot, name)));
      add(`rpm/${channel}/${rpmArch}/mirrorlist`, Buffer.from(`${origin}/rpm/snapshots/${generation}/${channel}/${rpmArch}/\n`), true);
    }
  }
  for (const name of listFiles(path.join(aptlyRoot, 'public'))) {
    // Clients consume the atomic InRelease and immutable by-hash indexes. Do not expose
    // independently mutable Release/signature pairs or ordinary index filenames.
    if (name.startsWith('pool/') || name.includes('/by-hash/')) add(`apt/${name}`, readFileSync(path.join(aptlyRoot, 'public', name)));
  }
  for (const [key, bytes] of Object.entries(setupFiles())) add(key, bytes, true);
  add('keys/delino-packages.asc', signing.publicKey, true);
  return files;
}
