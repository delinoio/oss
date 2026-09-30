// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import test from 'node:test';
import { relocateBaseline } from '../delidev/proto-compat.mjs';

test('descriptor relocation preserves changes and never hides unknown declarations', () => {
  const message = { name: 'Known', field: [{ name: 'value', number: 1, type: 'TYPE_STRING' }] };
  const unknown = { name: 'Later', field: [{ name: 'other', number: 2 }] };
  const original = { file: [{ name: 'old.proto', package: 'fixture.v1', syntax: 'proto3', messageType: [message, unknown] }] };
  const mapping = { legacyFile: 'old.proto', declarations: { Known: { kind: 'message', file: 'known.proto' } } };
  const projected = relocateBaseline(original, mapping);
  assert.deepEqual(projected.file[1].messageType, [message]);
  assert.deepEqual(projected.file[0].messageType, [unknown]);
  assert.deepEqual(original.file[0].messageType, [message, unknown]);
  assert.deepEqual(relocateBaseline(projected, mapping), projected);
  const duplicate = structuredClone(original);
  duplicate.file.push({ name: 'known.proto', package: 'fixture.v1', messageType: [message] });
  assert.throws(() => relocateBaseline(duplicate, mapping), /Ambiguous/);
});

test('current wire numbers match immutable assignments and future reservations', t => {
  const directory = mkdtempSync(join(tmpdir(), 'delidev-allocation-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const root = fileURLToPath(new URL('../../', import.meta.url));
  const output = join(directory, 'schema.json');
  execFileSync(process.execPath, [join(root, 'node_modules/@bufbuild/buf/bin/buf'), 'build', '--as-file-descriptor-set', '--output', output], { cwd: root });
  const descriptor = JSON.parse(readFileSync(output, 'utf8'));
  const ledger = JSON.parse(readFileSync(join(root, 'protos/delidev/allocations.json'), 'utf8'));
  const expected = structuredClone(ledger.baseline);
  for (const item of ledger.reservations) {
    const members = expected[item.declaration].members;
    for (const [name, number] of Object.entries(members)) if (number === item.number) assert.equal(name, item.member, 'a number cannot acquire a second meaning');
    if (Object.hasOwn(members, item.member)) assert.equal(members[item.member], item.number);
    members[item.member] = item.number;
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
