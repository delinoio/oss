// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import test from 'node:test';

test('current wire numbers match immutable assignments and future reservations', t => {
  const directory = mkdtempSync(join(tmpdir(), 'delidev-allocation-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const root = fileURLToPath(new URL('../../', import.meta.url));
  const output = join(directory, 'schema.json');
  execFileSync(process.execPath, [join(root, 'node_modules/@bufbuild/buf/bin/buf'), 'build', '--as-file-descriptor-set', '--output', output], { cwd: root });
  const descriptor = JSON.parse(readFileSync(output, 'utf8'));
  const ledger = JSON.parse(readFileSync(join(root, 'protos/delidev/allocations.json'), 'utf8'));
  const expected = structuredClone(ledger.baseline);
  const newDeclarations = new Map();
  for (const item of ledger.reservations) {
    assert.notEqual(Object.hasOwn(item, 'pr'), Object.hasOwn(item, 'issue'), 'an allocation has one original PR or owning issue');
    assert.ok(Number.isSafeInteger(item.pr ?? item.issue) && (item.pr ?? item.issue) > 0, 'allocation provenance is a positive GitHub number');
    if (item.sharedIssues !== undefined) {
      assert.ok(Array.isArray(item.sharedIssues));
      assert.equal(new Set(item.sharedIssues).size, item.sharedIssues.length, 'shared issue consumers are unique');
      for (const issue of item.sharedIssues) assert.ok(Number.isSafeInteger(issue) && issue > 0 && issue !== item.issue, 'shared consumers identify other issues');
    }
    // A wholly new declaration has no active baseline until its feature is implemented.
    // Reserve its closed values first without declaring or advertising support.
    if (item.newDeclaration === true) {
      assert.ok(item.kind === 'enum' || item.kind === 'message', 'new declarations have a closed protocol kind');
      assert.ok(!Object.hasOwn(ledger.baseline, item.declaration), 'original declarations cannot become new');
      const owner = Object.hasOwn(item, 'pr') ? `pr:${item.pr}` : `issue:${item.issue}`;
      if (newDeclarations.has(item.declaration)) assert.equal(newDeclarations.get(item.declaration), owner, 'a new declaration has one original owner');
      newDeclarations.set(item.declaration, owner);
      expected[item.declaration] ??= { kind: item.kind, members: {} };
    }
    const declaration = expected[item.declaration];
    assert.ok(declaration, `${item.declaration} must have a baseline or explicit new-declaration reservation`);
    assert.equal(item.kind, declaration.kind, `${item.declaration} reservation kind must match its declaration`);
    assert.ok(Number.isSafeInteger(item.number) && item.number >= (item.kind === 'enum' ? 0 : 1), 'wire numbers are valid integers');
    const members = declaration.members;
    for (const [name, number] of Object.entries(members)) if (number === item.number) assert.equal(name, item.member, 'a number cannot acquire a second meaning');
    if (Object.hasOwn(members, item.member)) assert.equal(members[item.member], item.number);
    members[item.member] = item.number;
  }
  for (const name of newDeclarations.keys()) {
    if (expected[name].kind !== 'enum') continue;
    assert.ok(Object.entries(expected[name].members).some(([member, number]) => number === 0 && member.endsWith('_UNSPECIFIED')), 'new enums reserve their zero unspecified value');
  }
  const found = new Map();
  for (const file of descriptor.file.filter(file => file.package === 'delidev.v1')) {
    for (const [kind, key, members] of [['enum', 'enumType', 'value'], ['message', 'messageType', 'field']]) {
      for (const declaration of file[key] ?? []) {
        found.set(declaration.name, declaration);
        if (!expected[declaration.name]) continue;
        assert.equal(expected[declaration.name].kind, kind);
        for (const field of declaration[members] ?? []) assert.equal(field.number, expected[declaration.name].members[field.name], `${declaration.name}.${field.name} must have a main-established allocation`);
      }
    }
  }
  for (const [name, declaration] of Object.entries(ledger.baseline)) {
    assert.ok(found.has(name), `missing original declaration ${name}`);
    const actual = Object.fromEntries((found.get(name)[declaration.kind === 'enum' ? 'value' : 'field'] ?? []).map(field => [field.name, field.number]));
    for (const [member, number] of Object.entries(declaration.members)) assert.equal(actual[member], number, `${name}.${member}`);
  }
});
