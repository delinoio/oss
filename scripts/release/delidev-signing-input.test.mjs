// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { stageSigningInput, verifySigningInput } from './delidev-signing-input.mjs';
import { digest, identity } from './delidev-release.mjs';
import { restoreCandidate } from './delidev-candidate.mjs';
const expected = identity('0.1.1', 'a'.repeat(40)), target = 'aarch64-apple-darwin';
async function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'delidev-signing-input-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  const input = join(root, 'input'), output = join(root, 'output'), credits = join(root, 'credits');
  mkdirSync(input); writeFileSync(credits, 'fixture original Chromium notices');
  const artifacts = [];
  for (const [component, name] of [['desktop', 'delidev-desktop-darwin-arm64.dmg'], ['worker', 'delidev-worker-darwin-arm64']]) {
    writeFileSync(join(input, name), `fixture ${name}`);
    artifacts.push({component, target: 'darwin-arm64', name, ...await digest(join(input, name))});
  }
  writeFileSync(join(input, 'input.json'), JSON.stringify({schemaVersion: 1, version: expected.version, sourceRevision: expected.sourceRevision, protocolVersion: 1, artifacts, platformSigning: 'keyless-dry-run', nativeAcceptance: 'unverified', publication: 'not-requested'}));
  await stageSigningInput({input, output, credits, target, expected});
  return {root, input, output, artifacts};
}
test('clean signer receives closed byte-pinned native inputs and original notices without a CEF cache', async t => {
  const {output} = await fixture(t);
  assert.equal(readFileSync(await verifySigningInput(output, target, expected), 'utf8'), 'fixture original Chromium notices');
});
for (const fault of ['revision', 'version', 'target', 'architecture', 'inventory', 'payload', 'notice', 'extra', 'symlink']) test(`clean signing inputs reject ${fault} drift`, async t => {
  const {output, artifacts} = await fixture(t);
  const file = join(output, 'input.json'), report = JSON.parse(readFileSync(file, 'utf8'));
  if (fault === 'revision') report.sourceRevision = 'b'.repeat(40);
  if (fault === 'version') report.version = '0.1.2';
  if (fault === 'target') return assert.rejects(verifySigningInput(output, 'x86_64-apple-darwin', expected));
  if (fault === 'architecture') report.artifacts[0].target = 'darwin-amd64';
  if (fault === 'inventory') report.artifacts.pop();
  if (fault === 'payload') writeFileSync(join(output, artifacts[0].name), 'changed payload');
  if (fault === 'notice') writeFileSync(join(output, 'Chromium-CREDITS.html'), 'changed notices');
  if (fault === 'extra') writeFileSync(join(output, 'unexpected'), 'extra');
  if (fault === 'symlink') { rmSync(join(output, artifacts[0].name)); symlinkSync(join(output, artifacts[1].name), join(output, artifacts[0].name)); }
  writeFileSync(file, JSON.stringify(report));
  await assert.rejects(verifySigningInput(output, target, expected));
});
test('same-run unsigned inputs restore before rebuilding and reject expired or conflicting inventory', async t => {
  const {root, output} = await fixture(t);
  const artifacts = [{id: 1, expired: false, name: `delidev-signing-input-${target}-${expected.sourceRevision}-123`}];
  const directory = join(root, 'restored');
  const restore = () => restoreCandidate({signingInput: true, directory, target, expected, runId: '123', list: async () => artifacts, download: async () => {
    mkdirSync(directory, {recursive: true});
    for (const name of ['input.json', 'signing-input.json', 'Chromium-CREDITS.html', 'delidev-desktop-darwin-arm64.dmg', 'delidev-worker-darwin-arm64']) copyFileSync(join(output, name), join(directory, name));
  }});
  assert.equal(await restore(), true);
  artifacts[0].expired = true;
  await assert.rejects(restore(), /expired/);
  artifacts[0].expired = false; artifacts.push({...artifacts[0], id: 2});
  await assert.rejects(restore(), /ambiguous/);
});
