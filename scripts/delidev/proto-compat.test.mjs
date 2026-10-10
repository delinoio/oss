// SPDX-License-Identifier: Apache-2.0
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { normalizeNativeAppsBinding } from './proto-compat.mjs';

test('Apps generation normalization preserves established outputs and is idempotent', () => {
  const directory = mkdtempSync(join(tmpdir(), 'delidev-proto-eof-'));
  try {
    const baseline = join(directory, 'account_pb.ts');
    const apps = join(directory, 'session_native_apps_pb.ts');
    writeFileSync(baseline, 'existing generator output;\n\n');
    normalizeNativeAppsBinding(directory);
    assert.equal(readFileSync(baseline, 'utf8'), 'existing generator output;\n\n');
    writeFileSync(apps, 'new generator output;\n\n');
    normalizeNativeAppsBinding(directory);
    const canonical = readFileSync(apps);
    assert.equal(canonical.toString(), 'new generator output;\n');
    assert.equal(readFileSync(baseline, 'utf8'), 'existing generator output;\n\n');
    normalizeNativeAppsBinding(directory);
    assert.deepEqual(readFileSync(apps), canonical);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
