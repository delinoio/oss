// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { targets } from './native-package.mjs';
import { updaterTarget } from './bundle-updater-dry-run.mjs';
import { artifactName, targets as signedTargets } from '../../../scripts/release/generate-delidev-updater.mjs';
test('native updater build targets exactly match signed desktop and Worker inventory',()=>{
 assert.deepEqual(targets.map(updaterTarget),signedTargets);
 const names=targets.flatMap(v=>['desktop','worker'].map(component=>artifactName(component,updaterTarget(v))));assert.equal(new Set(names).size,12);
 assert.ok(names.includes('delidev-desktop-darwin-arm64.dmg'));assert.ok(names.includes('delidev-worker-windows-arm64.exe'));assert.ok(names.includes('delidev-desktop-linux-amd64.AppImage'));
});
