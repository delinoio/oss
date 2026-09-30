// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import test from 'node:test';

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const read = path => readFileSync(resolve(root, path), 'utf8');
const hash = value => createHash('sha256').update(value).digest('hex');
const blocks = text => text.split(/\n\s*\n|\n(?=- )/u).map(value => value.trim()).filter(Boolean);

test('relocated DeliDev requirements remain complete and verbatim', () => {
  const inventory = JSON.parse(read('docs/evidence/delidev/pr-conflict-structure/document-relocations.json'));
  const destinations = new Map();
  for (const item of inventory.blocks) {
    if (!destinations.has(item.destination)) destinations.set(item.destination, new Set(blocks(read(item.destination)).map(hash)));
    assert.ok(destinations.get(item.destination).has(item.sha256), `${item.source} -> ${item.destination}: ${item.sha256}`);
  }
});

test('PR closure inventory remains restricted to the owner-approved 22 PRs', () => {
  const snapshot = JSON.parse(read('docs/evidence/delidev/pr-conflict-structure/pr-snapshot.json'));
  assert.equal(snapshot.repository, 'delinoio/oss');
  assert.deepEqual(snapshot.pullRequests.map(pr => pr.number).sort((a, b) => a - b), [1108,1109,1110,1111,1112,1113,1114,1115,1116,1117,1118,1121,1122,1124,1125,1126,1127,1140,1141,1151,1154,1159]);
  for (const pr of snapshot.pullRequests) {
    assert.match(pr.headRefOid, /^[a-f0-9]{40}$/u);
    assert.equal(pr.url, `https://github.com/delinoio/oss/pull/${pr.number}`);
    assert.ok(pr.issues.length > 0);
  }
});
