// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { relocateBaseline } from './proto-compat.mjs';

const buf = fileURLToPath(new URL('../../node_modules/@bufbuild/buf/bin/buf', import.meta.url));
const revision = execFileSync('git', ['rev-parse', '--verify', '--end-of-options', `${process.argv[2]}^{commit}`], { encoding: 'utf8' }).trim();
const temporary = mkdtempSync(join(tmpdir(), 'delidev-proto-baseline-'));
const run = (...args) => execFileSync(process.execPath, [buf, ...args], {
  stdio: 'inherit', env: { ...process.env, GIT_LFS_SKIP_SMUDGE: '1' },
});
try {
  const original = join(temporary, 'original.json');
  const projected = join(temporary, 'projected.json');
  run('build', `.git#ref=${revision}`, '--as-file-descriptor-set', '--output', original);
  const baseline = JSON.parse(readFileSync(original, 'utf8'));
  const relocated = relocateBaseline(baseline);
  if (JSON.stringify(baseline) !== JSON.stringify(relocated)) {
    // A package comparison independently protects all original API semantics.
    run('breaking', '--against', original, '--config', JSON.stringify({ version: 'v2', modules: [{ path: 'protos' }], breaking: { use: ['PACKAGE'] } }));
  }
  writeFileSync(projected, JSON.stringify(relocated));
  // Keep the repository's actual FILE policy and all other package checks.
  run('breaking', '--against', projected);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
