import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const root = path.resolve(import.meta.dirname, '../..');
const read = (file) => readFileSync(path.join(root, file), 'utf8');
const vendorPrefixes = ['fspy', 'materialized_artifact', 'vt_'];
const isVendor = (name) => vendorPrefixes.some((prefix) => name.startsWith(prefix));

test('repository-owned manifests declare Apache-2.0 and imported crates retain MIT', () => {
  assert.match(read('LICENSE'), /Apache License\s+Version 2\.0/u);
  assert.match(read('NOTICE'), /Copyright 2026 Delino/u);
  assert.match(read('Cargo.toml'), /\[workspace\.package\][\s\S]*?license = "Apache-2\.0"/u);
  for (const name of readdirSync(path.join(root, 'crates'))) {
    const file = `crates/${name}/Cargo.toml`;
    let cargo;
    try { cargo = read(file); } catch { continue; }
    assert.match(cargo, new RegExp(`^license = "${isVendor(name) ? 'MIT' : 'Apache-2\\.0'}"$`, 'mu'), file);
  }
  for (const directory of ['apps', 'packages', 'servers']) {
    for (const name of readdirSync(path.join(root, directory))) {
      const file = `${directory}/${name}/package.json`;
      let manifest;
      try { manifest = JSON.parse(read(file)); } catch { continue; }
      assert.equal(manifest.license, 'Apache-2.0', file);
    }
  }
});

test('owned archive licenses and upstream notices remain distinct', () => {
  for (const name of ['clibox', 'clibox-config', 'clibox-system', 'clibox-transform', 'clibox-wait', 'pnport', 'pnport-core', 'pnport-preload']) {
    assert.equal(read(`crates/${name}/LICENSE`), read('LICENSE'), name);
  }
  for (const name of ['fspy', 'fspy_detours_sys', 'materialized_artifact', 'vt_str']) {
    assert.match(read(`crates/${name}/LICENSE`), /MIT License/u, name);
  }
  assert.match(read('crates/fspy_detours_sys/detours/LICENSE'), /Copyright \(c\) Microsoft Corporation/u);
  assert.match(read('apps/devhud/src-tauri/assets/fonts/noto-sans-kr/LICENSE'), /SIL OPEN FONT LICENSE/u);
});
