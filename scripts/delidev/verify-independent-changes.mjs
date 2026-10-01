// SPDX-License-Identifier: Apache-2.0
// A disposable merge experiment; never publishes or changes the current checkout.
import { cpSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync, symlinkSync, rmSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../../', import.meta.url));
const temporary = mkdtempSync(join(tmpdir(), 'delidev-independent-'));
const run = (command, args) => execFileSync(command, args, { cwd: temporary, encoding: 'utf8', env: { ...process.env, GIT_LFS_SKIP_SMUDGE: '1' } });
const git = (...args) => run('git', args);
const put = (path, value) => { mkdirSync(dirname(join(temporary, path)), { recursive: true }); writeFileSync(join(temporary, path), value); };
const generate = () => { run(process.execPath, [join(root, 'node_modules/@bufbuild/buf/bin/buf'), 'generate']); run(process.execPath, ['scripts/delidev/proto-compat.mjs']); };
try {
  for (const path of ['protos', 'buf.yaml', 'buf.gen.yaml', 'go.mod', 'go.sum', 'package.json', 'pnpm-workspace.yaml', 'scripts/delidev/proto-compat.mjs', 'scripts/delidev/proto-layout.json']) {
    mkdirSync(dirname(join(temporary, path)), { recursive: true }); cpSync(join(root, path), join(temporary, path), { recursive: true });
  }
  symlinkSync(join(root, 'node_modules'), join(temporary, 'node_modules'), 'dir');
  put('.gitignore', 'node_modules/\n');
  git('init', '--quiet', '--initial-branch=baseline');
  git('config', 'user.name', 'DeliDev merge fixture'); git('config', 'user.email', 'fixture@example.invalid');
  generate(); git('add', '.'); git('commit', '--quiet', '-m', 'Frozen structural baseline');
  const baseline = git('rev-parse', 'HEAD').trim();
  const changes = [];
  for (const [family, service] of [['provider', 'ProviderService'], ['schedule', 'ScheduleService']]) {
    git('checkout', '--quiet', '-b', `feature-${family}`, baseline);
    const path = `protos/delidev/v1/${family}.proto`;
    let source = readFileSync(join(temporary, path), 'utf8');
    const prefix = `Fixture${family[0].toUpperCase()}${family.slice(1)}`;
    source = source.replace(`service ${service} {`, `service ${service} {\n  rpc ${prefix}(${prefix}Request) returns (${prefix}Response);`);
    source += `\nmessage ${prefix}Request { string label = 1; }\nmessage ${prefix}Response { string label = 1; }\n`;
    put(path, source);
    put(`apps/delidev/src/settings-${family}-fixture.integration.test.tsx`, `import { expect, it } from 'vitest';\nimport { ${service} } from '@delinoio/delidev-api-client';\nit('${family} owns its protocol surface', () => expect(${service}.typeName).toBe('delidev.v1.${service}'));\n`);
    generate();
    git('add', '.'); git('commit', '--quiet', '-m', `Independent ${family} fixture`);
    changes.push({ family, files: git('diff', '--name-only', baseline, 'HEAD').trim().split('\n') });
  }
  git('checkout', '--quiet', 'feature-provider');
  git('merge', '--no-edit', 'feature-schedule');
  const before = git('status', '--porcelain');
  generate();
  if (before || git('status', '--porcelain')) throw new Error('Merged generated bindings are not reproducible');
  run(process.execPath, [join(root, 'node_modules/@bufbuild/buf/bin/buf'), 'lint']);
  const shared = changes[0].files.filter(path => changes[1].files.includes(path));
  if (shared.length) throw new Error(`Unexpected shared source changes: ${shared.join(', ')}`);
  console.log(JSON.stringify({ result: 'passed', generatedBindingsReproduceAfterMerge: true, sharedChangedFiles: shared, branches: changes }, null, 2));
} finally { rmSync(temporary, { recursive: true, force: true }); }
