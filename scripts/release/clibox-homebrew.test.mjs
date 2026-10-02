import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

const root = new URL('../../', import.meta.url);
function args(version = '1.2.3') {
  return ['scripts/release/update-homebrew.sh', '--project', 'clibox', '--version', version,
    ...['amd64', 'arm64'].flatMap((arch) => [`--darwin-${arch}-url`, `https://github.com/delinoio/oss/releases/download/clibox@v${version}/clibox-darwin-${arch}.tar.gz`, `--darwin-${arch}-sha256`, 'a'.repeat(64)])];
}

test('clibox Formula renders only canonical macOS prebuilt URLs with full notices', () => {
  const renderArgs = args();
  renderArgs[renderArgs.indexOf('--darwin-arm64-sha256') + 1] = 'b'.repeat(64);
  const formula = execFileSync('bash', [...renderArgs, '--dry-run'], { cwd: root, encoding: 'utf8' });
  for (const pattern of [/depends_on :macos/u, /if Hardware::CPU.arm\?/u, /license "Apache-2.0"/u, /prefix.install "LICENSE", "NOTICE", "LICENSE.fspy"/u, /base64 encode/u]) assert.match(formula, pattern);
  const branches = formula.match(/if Hardware::CPU.arm\?\n([\s\S]*?)  else\n([\s\S]*?)  end/u);
  assert.ok(branches, 'architecture-specific sources must use an audit-compatible conditional');
  for (const [branch, arch, digest] of [[branches[1], 'arm64', 'b'], [branches[2], 'amd64', 'a']]) {
    assert.ok(branch.includes(`url "https://github.com/delinoio/oss/releases/download/clibox@v1.2.3/clibox-darwin-${arch}.tar.gz"`));
    assert.ok(branch.includes(`sha256 "${digest.repeat(64)}"`));
    assert.equal((branch.match(/\burl /gu) ?? []).length, 1);
    assert.equal((branch.match(/\bsha256 /gu) ?? []).length, 1);
  }
  for (const line of formula.split('\n')) assert.ok(line.length <= 118, `Formula line exceeds strict audit limit: ${line.length}`);
  assert.doesNotMatch(formula, /__VERSION__|on_arm do|on_intel do|on_linux|depends_on "(?:node|rust)"|service do|cargo build/u);
  for (const [key, value] of [['--version', '1.2.3-beta'], ['--darwin-amd64-url', 'https://example.invalid/clibox-darwin-amd64.tar.gz'], ['--darwin-arm64-sha256', 'invalid']]) {
    const invalid = args(); invalid[invalid.indexOf(key) + 1] = value;
    assert.notEqual(spawnSync('bash', [...invalid, '--dry-run'], { cwd: root }).status, 0);
  }
  assert.notEqual(spawnSync('bash', [...args(), '--linux-arm64-url', 'https://example.invalid/linux', '--dry-run'], { cwd: root }).status, 0);
});

test('clibox tap publication adds only its Formula, retries identically, and rejects downgrade or replacement', (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), 'clibox-tap-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const remote = path.join(directory, 'tap.git'); const checkout = path.join(directory, 'checkout');
  const git = (argv, cwd = directory) => execFileSync('git', argv, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  git(['init', '--bare', '--initial-branch=main', remote]);
  git(['clone', remote, checkout]);
  git(['config', 'user.name', 'Fixture'], checkout); git(['config', 'user.email', 'fixture@example.invalid'], checkout);
  mkdirSync(path.join(checkout, 'Formula')); writeFileSync(path.join(checkout, 'Formula/existing.rb'), 'preserve other products\n');
  git(['add', 'Formula/existing.rb'], checkout); git(['commit', '-m', 'Existing product'], checkout); git(['push', 'origin', 'HEAD:main'], checkout);
  const env = { ...process.env, GH_TOKEN: 'fixture', HOMEBREW_TAP_GH_TOKEN: 'fixture', RELEASE_BOT_NAME: 'Fixture', RELEASE_BOT_EMAIL: 'fixture@example.invalid', GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: `url.file://${remote}.insteadOf`, GIT_CONFIG_VALUE_0: 'https://github.com/fixture/clibox-tap.git' };
  const run = (argv) => spawnSync('bash', [...argv, '--tap-repo', 'fixture/clibox-tap'], { cwd: root, env, encoding: 'utf8' });
  const first = run(args()); assert.equal(first.status, 0, first.stderr);
  const before = git(['rev-parse', 'main'], remote);
  assert.equal(git(['show', 'main:Formula/existing.rb'], remote), readFileSync(path.join(checkout, 'Formula/existing.rb'), 'utf8'));
  const same = run(args()); assert.equal(same.status, 0, same.stderr);
  const older = run(args('1.2.2')); assert.notEqual(older.status, 0); assert.match(older.stderr, /refusing clibox Homebrew downgrade/u);
  const changed = args(); changed[changed.indexOf('--darwin-arm64-sha256') + 1] = 'b'.repeat(64);
  const conflict = run(changed); assert.notEqual(conflict.status, 0); assert.match(conflict.stderr, /conflicting clibox formula bytes/u);
  assert.equal(git(['rev-parse', 'main'], remote), before);
  const upgrade = run(args('1.2.4')); assert.equal(upgrade.status, 0, upgrade.stderr);
  assert.notEqual(git(['rev-parse', 'main'], remote), before);
});
