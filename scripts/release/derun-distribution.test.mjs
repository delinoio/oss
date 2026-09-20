import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { sha256 } from './linux-packages/model.mjs';

test('Derun Homebrew rendering requires and installs the Linux ARM64 release asset', () => {
  const args = ['scripts/release/update-homebrew.sh', '--project', 'derun', '--version', '1.2.3', '--dry-run'];
  for (const target of ['darwin-amd64', 'darwin-arm64', 'linux-amd64']) args.push(`--${target}-url`, `https://example.com/derun-${target}.tar.gz`, `--${target}-sha256`, 'a'.repeat(64));
  assert.throws(() => execFileSync('bash', args, { stdio: 'pipe' }), /requires --linux-arm64/u);
  args.push('--linux-arm64-url', 'https://example.com/derun-linux-arm64.tar.gz', '--linux-arm64-sha256', 'b'.repeat(64));
  const formula = execFileSync('bash', args, { encoding: 'utf8', stdio: 'pipe' });
  assert.match(formula, /url "https:\/\/example.com\/derun-linux-arm64.tar.gz"\s+sha256 "b{64}"/u);
  assert.doesNotMatch(formula, /__|odie/u);
  assert.match(formula, /license "Apache-2.0"/u);
});

test('Derun direct ARM64 installation checks the selected archive checksum', (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), 'derun-install-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const mock = path.join(directory, 'mock');
  mkdirSync(mock);
  const binary = '#!/bin/sh\nprintf "derun fixture\\n"\n';
  writeFileSync(path.join(directory, 'derun'), binary, { mode: 0o755 });
  const archivePath = path.join(directory, 'derun-linux-arm64.tar.gz');
  execFileSync('tar', ['-czf', archivePath, '-C', directory, 'derun']);
  const bytes = readFileSync(archivePath);
  writeFileSync(path.join(directory, 'SHA256SUMS'), `${sha256(bytes)}  derun-linux-arm64.tar.gz\n`);
  writeFileSync(path.join(mock, 'uname'), '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo aarch64;; *) exit 2;; esac\n', { mode: 0o755 });
  writeFileSync(path.join(mock, 'curl'), '#!/bin/sh\ncase "$2" in https://github.com/delinoio/oss/releases/download/derun@v1.2.3/*) cp "$DERUN_FIXTURE/${2##*/}" .;; *) exit 2;; esac\n', { mode: 0o755 });
  const install = path.join(directory, 'installed');
  const args = ['scripts/install/derun.sh', '--version', '1.2.3', '--method', 'direct', '--install-dir', install];
  const options = { env: { ...process.env, PATH: `${mock}:${process.env.PATH}`, DERUN_FIXTURE: directory }, stdio: 'pipe' };
  execFileSync('bash', args, options);
  assert.equal(readFileSync(path.join(install, 'derun'), 'utf8'), binary);
  rmSync(install, { recursive: true });
  writeFileSync(path.join(directory, 'SHA256SUMS'), `${'0'.repeat(64)}  derun-linux-arm64.tar.gz\n`);
  assert.throws(() => execFileSync('bash', args, options), /checksum|FAILED/u);
});
