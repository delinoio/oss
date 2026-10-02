// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import test from 'node:test';

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const read = path => readFileSync(resolve(root, path), 'utf8');

test('pending database versions are ordered reservations independent of executable migrations', () => {
  const ledger = JSON.parse(read('cmds/delidev-cli/internal/store/migration-reservations.json'));
  let previous = ledger.baselineVersion;
  const seen = new Set();
  for (const entry of ledger.reservations) {
    assert.equal(entry.version, previous + 1);
    assert.notEqual(Object.hasOwn(entry, 'pr'), Object.hasOwn(entry, 'issue'), 'a migration has one original PR or owning issue');
    const owner = entry.pr ?? entry.issue;
    assert.ok(Number.isSafeInteger(owner) && owner > 0, 'migration provenance is a positive GitHub number');
    assert.ok(!seen.has(owner), 'migration owners are unique');
    if ([1108, 1115, 1117].includes(entry.pr)) assert.equal(entry.originalBranchVersion, 25);
    for (const dependency of entry.dependsOn ?? []) assert.ok(seen.has(dependency));
    previous = entry.version;
    seen.add(owner);
  }
  for (const originalPR of [1108, 1115, 1117]) assert.ok(seen.has(originalPR));
});
