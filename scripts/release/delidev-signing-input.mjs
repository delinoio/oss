// SPDX-License-Identifier: Apache-2.0
import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { artifactName } from './generate-delidev-updater.mjs';
import { digest, identity, releaseTargets, requireValue, updaterTarget } from './delidev-release.mjs';
import { cefCredits } from '../../apps/delidev/scripts/native-package.mjs';

const creditsName = 'Chromium-CREDITS.html';
async function verifyInputs(directory, target, expected) {
  const selected = releaseTargets.find(t => t.target === target && t.platform === 'darwin');
  requireValue(selected, 'Unsupported macOS signing input target');
  await digest(join(directory, 'input.json'));
  const report = JSON.parse(readFileSync(join(directory, 'input.json'), 'utf8'));
  const platform = updaterTarget(selected);
  requireValue(report.schemaVersion === 1 && report.sourceRevision === expected.sourceRevision && report.version === expected.version && report.protocolVersion === 1 && report.platformSigning === 'keyless-dry-run' && report.nativeAcceptance === 'unverified' && report.publication === 'not-requested', 'macOS signing input identity conflict');
  const names = ['desktop', 'worker'].map(component => artifactName(component, platform)).sort();
  requireValue(JSON.stringify(report.artifacts?.map(a => a.name).sort()) === JSON.stringify(names), 'macOS signing input inventory conflict');
  for (const artifact of report.artifacts) {
    requireValue(artifact.target === platform && artifact.name === artifactName(artifact.component, platform), 'macOS signing input architecture conflict');
    const actual = await digest(join(directory, artifact.name));
    requireValue(actual.size === artifact.size && actual.sha256 === artifact.sha256, 'macOS signing input bytes conflict');
  }
  return report;
}
export async function stageSigningInput({ input, output, target, expected, credits }) {
  const report = await verifyInputs(input, target, expected);
  requireValue(!existsSync(output), 'Retain original macOS signing inputs');
  mkdirSync(output, {recursive: true});
  for (const name of ['input.json', ...report.artifacts.map(a => a.name)]) copyFileSync(join(input, name), join(output, name));
  const originalCredits = await digest(credits);
  copyFileSync(credits, join(output, creditsName));
  const copiedCredits = await digest(join(output, creditsName));
  requireValue(originalCredits.size === copiedCredits.size && originalCredits.sha256 === copiedCredits.sha256, 'Original CEF notices changed during staging');
  writeFileSync(join(output, 'signing-input.json'), JSON.stringify({schemaVersion: 1, version: expected.version, sourceRevision: expected.sourceRevision, target, credits: {name: creditsName, ...copiedCredits}}, null, 2) + '\n', {flag: 'wx'});
  await verifySigningInput(output, target, expected);
}
export async function verifySigningInput(directory, target, expected) {
  const report = await verifyInputs(directory, target, expected);
  await digest(join(directory, 'signing-input.json'));
  const notice = JSON.parse(readFileSync(join(directory, 'signing-input.json'), 'utf8'));
  requireValue(notice.schemaVersion === 1 && notice.version === expected.version && notice.sourceRevision === expected.sourceRevision && notice.target === target && notice.credits?.name === creditsName, 'macOS signing notice identity conflict');
  requireValue(JSON.stringify(readdirSync(directory).sort()) === JSON.stringify(['input.json', 'signing-input.json', creditsName, ...report.artifacts.map(a => a.name)].sort()), 'macOS signing input files conflict');
  const actual = await digest(join(directory, creditsName));
  requireValue(actual.size === notice.credits.size && actual.sha256 === notice.credits.sha256, 'macOS signing notice bytes conflict');
  return join(directory, creditsName);
}
async function main() {
  const expected = identity(process.env.RELEASE_VERSION, process.env.RELEASE_REVISION);
  const target = process.env.RELEASE_TARGET;
  const selected = releaseTargets.find(t => t.target === target && t.platform === 'darwin');
  requireValue(selected, 'Unsupported macOS signing input target');
  await stageSigningInput({input: resolve('target/delidev-updater-input', target, expected.sourceRevision), output: resolve('target/delidev-signing-input', target), target, expected, credits: cefCredits(selected, process.env)});
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(() => { console.error('DeliDev signing input preparation failed'); process.exitCode = 1; });
