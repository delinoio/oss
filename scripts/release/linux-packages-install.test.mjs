import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

const source = readFileSync(new URL('./linux-packages/install-test.sh', import.meta.url), 'utf8');
const phase = source.match(/^phase\(\) \{\n[\s\S]*?^\}\n/m)?.[0];
const fault = source.match(/^dnf_fixture_fault\(\) \(\n[\s\S]*?^\)\n/m)?.[0];
assert.ok(phase && fault, 'exercise the actual shell fixture helpers');

function runFault(t, version, scenario, faults) {
  const directory = mkdtempSync(path.join(tmpdir(), 'delino package-mock-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const trace = path.join(directory, 'commands');
  const script = `set -euo pipefail
project=binpm; version=1.2.3; name=delino; arch=amd64; mode=fixture; origin=http://fixture.invalid
command() {
  if [ "$1" = -v ] && [ "$2" = dnf5 ]; then [ "$DNF_VERSION" = 5 ]; else builtin command "$@"; fi
}
curl() { printf 'curl\\t%s\\n' "$*" >> "$TRACE"; }
mock_dnf() {
  printf '%s' "$DNF_VERSION" >> "$TRACE"
  printf '\\t%s' "$@" >> "$TRACE"
  printf '\\n' >> "$TRACE"
  local cache='' verb='' argument
  for argument in "$@"; do
    case "$argument" in
      --setopt=cachedir=*|--setopt=system_cachedir=*) cache=\${argument#*=}; cache=\${cache#*=} ;;
      makecache|install) verb=$argument ;;
    esac
  done
  test -d "$cache"
  if [ "$verb" = makecache ]; then
    test ! -e "$cache/metadata"
    touch "$cache/metadata"
    if [ "$SCENARIO" = metadata-failure ]; then echo 'Cannot download repository metadata'; return 1; fi
    if [ "$CURRENT_FAULT" = bad-signature ]; then
      if [ "$SCENARIO" = unexpected-success ]; then return 0; fi
      if [ "$SCENARIO" = unrelated-failure ]; then echo 'Cannot fetch /bad-signature/repodata'; return 1; fi
      if [ "$SCENARIO" = missing-key ]; then echo 'repomd.xml GPG signature verification error: Signing key not found'; return 1; fi
      echo 'repomd.xml GPG signature verification error: Bad GPG signature'; return 1
    fi
    return 0
  fi
  test "$verb" = install
  test -e "$cache/metadata"
  if [ "$SCENARIO" = unexpected-success ]; then return 0; fi
  if [ "$SCENARIO" = unrelated-failure ]; then echo 'nothing provides required-library'; return 1; fi
  echo 'Downloading successful, but checksum does not match. Calculated: abc Expected: def'; return 1
}
dnf() { mock_dnf "$@"; }
dnf5() { mock_dnf "$@"; }
${phase}${fault}
${faults.map((value) => `CURRENT_FAULT=${value}; dnf_fixture_fault "$CURRENT_FAULT"`).join('\n')}
`;
  const result = spawnSync('bash', ['-c', script], {
    encoding: 'utf8', env: { ...process.env, TMPDIR: directory, TRACE: trace, DNF_VERSION: String(version), SCENARIO: scenario },
  });
  assert.ifError(result.error);
  return { ...result, lines: existsSync(trace) ? readFileSync(trace, 'utf8').trim().split('\n').map((line) => line.split('\t')) : [] };
}

for (const version of [4, 5]) {
  test(`DNF${version} faults use fresh isolated metadata and require verification diagnostics`, (t) => {
    const result = runFault(t, version, 'verification', ['bad-signature', 'bad-checksum']);
    assert.equal(result.status, 0, result.stderr);
    const commands = result.lines.filter((line) => line[0] === String(version));
    assert.equal(commands.length, 3);
    assert.deepEqual(commands.map((line) => line.includes('makecache') ? 'makecache' : 'install'), ['makecache', 'makecache', 'install']);
    const option = version === 5 ? 'system_cachedir' : 'cachedir';
    const caches = commands.map((line) => line.find((value) => value.startsWith(`--setopt=${option}=`))?.slice(`--setopt=${option}=`.length));
    assert.ok(caches.every(Boolean));
    assert.notEqual(caches[0], caches[1]);
    assert.equal(caches[1], caches[2]);
    assert.ok(caches.every((cache) => !existsSync(cache)), 'fault caches are removed after each attempt');
    for (const command of commands) {
      assert.ok(command.includes('--repo=delino'));
      assert.ok(command.includes('--setopt=delino.skip_if_unavailable=0'));
      assert.ok(!command.includes('clean'));
      assert.equal(command.includes('--refresh'), command.includes('makecache'));
    }
    assert.ok(commands[2].includes('binpm-1.2.3-1'));
    const phases = result.stderr.trim().split('\n').map((line) => JSON.parse(line).phase);
    assert.deepEqual(phases, ['dnf-bad-signature-metadata-begin', 'dnf-bad-signature-verified', 'dnf-bad-checksum-metadata-begin', 'dnf-bad-checksum-package-begin', 'dnf-bad-checksum-verified']);
  });
  for (const selectedFault of ['bad-signature', 'bad-checksum']) {
    for (const scenario of (selectedFault === 'bad-signature' ? ['unexpected-success', 'unrelated-failure', 'missing-key'] : ['unexpected-success', 'unrelated-failure'])) {
      test(`DNF${version} rejects ${selectedFault} ${scenario}`, (t) => {
        const result = runFault(t, version, scenario, [selectedFault]);
        assert.notEqual(result.status, 0);
        assert.ok(!result.stderr.includes(`dnf-${selectedFault}-verified`));
      });
    }
  }
  test(`DNF${version} failed checksum metadata prevents a package attempt`, (t) => {
    const result = runFault(t, version, 'metadata-failure', ['bad-checksum']);
    assert.notEqual(result.status, 0);
    assert.ok(!result.lines.some((line) => line.includes('install')));
  });
}
