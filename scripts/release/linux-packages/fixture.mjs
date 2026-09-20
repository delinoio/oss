import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { identity, Project, architectures, encode, sha256 } from './model.mjs';
import { temporarySigningKey, packageFiles, candidateRecord } from './package.mjs';
import { buildRepositories } from './repository.mjs';

const output = path.resolve(process.argv[2]);
const temporary = mkdtempSync(path.join(tmpdir(), 'delino-native-fixture-'));
try {
  const signing = temporarySigningKey(path.join(temporary, 'keys'));
  const records = []; const packages = [];
  mkdirSync(path.join(output, 'previous'), { recursive: true });
  for (const project of Object.values(Project)) {
    const plan = identity({ project, version: '1.2.3', revision: 'a'.repeat(40) });
    const file = path.join(temporary, project);
    writeFileSync(file, `#!/bin/sh\nprintf '%s\\n' '${project} 1.2.3'\n`, { mode: 0o755 });
    const input = { epoch: 1700000000, binaries: Object.fromEntries(architectures.map((arch) => [arch, { file, libraries: [], sha256: sha256(Buffer.from(project)) }])), source: sha256(encode(plan)), hashes: Object.fromEntries(architectures.map((arch) => [arch, 'b'.repeat(64)])) };
    const next = packageFiles(plan, input, path.join(temporary, 'packages', project), signing);
    packages.push(...next); records.push(candidateRecord(plan, input, next, signing));
    for (const previous of packageFiles({ ...plan, version: '0.0.0' }, input, path.join(temporary, 'previous', project), signing)) writeFileSync(path.join(output, 'previous', previous.name), previous.bytes);
  }
  const generation = sha256(encode(records));
  const files = await buildRepositories(records, async (file) => packages.find((entry) => entry.sha256 === file.sha256).bytes, path.join(temporary, 'repositories'), signing, generation);
  for (const file of files) { const target = path.join(output, file.key); mkdirSync(path.dirname(target), { recursive: true }); writeFileSync(target, file.bytes); }
  writeFileSync(path.join(output, 'fixture.json'), encode({ projects: records.map((record) => record.identity), fingerprint: signing.fingerprint, generation }));
} catch (error) {
  // Only disposable fixtures print external tool stderr; production always reports a stable code.
  console.error(error.cause?.stderr?.toString() ?? error.message); process.exitCode = 1;
} finally { rmSync(temporary, { recursive: true, force: true }); }
