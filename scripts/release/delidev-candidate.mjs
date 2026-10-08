// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from 'node:child_process';
import { appendFileSync, mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifySigningInput } from './delidev-signing-input.mjs';
import { digest, expectedNames, identity, releaseTargets, requireValue, updaterTarget } from './delidev-release.mjs';

export async function verifyCandidate(directory, target, expected) {
  const selected = releaseTargets.find(t => t.target === target);
  requireValue(selected, 'Unsupported DeliDev candidate target');
  const report = JSON.parse(readFileSync(join(directory,'release-input.json'),'utf8'));
  const names = expectedNames().filter(n => n.includes(updaterTarget(selected)));
  requireValue(report.schemaVersion === 1 && report.sourceRevision === expected.sourceRevision && report.version === expected.version && report.target === target && report.nativeAcceptance === 'unverified' && report.platformSigning === (selected.platform === 'darwin' ? 'developer-id-notarized' : 'linux-digests'), 'Retained DeliDev candidate identity conflict');
  requireValue(JSON.stringify(report.artifacts?.map(a => a.name).sort()) === JSON.stringify(names) && JSON.stringify(readdirSync(directory).sort()) === JSON.stringify([...names,'release-input.json'].sort()), 'Retained DeliDev candidate inventory conflict');
  for (const a of report.artifacts) { const actual = await digest(join(directory,a.name)); requireValue(actual.size === a.size && actual.sha256 === a.sha256,'Retained candidate bytes conflict'); }
  return report;
}
export async function restoreCandidate({ list, download, directory, target, expected, runId, signingInput = false }) {
  requireValue(/^[1-9]\d*$/.test(runId), 'Invalid candidate run');
  const name = `${signingInput ? "delidev-signing-input" : "delidev-release"}-${target}-${expected.sourceRevision}-${runId}`;
  const artifacts = await list();
  const matches = artifacts.filter(a => a.name === name);
  requireValue(matches.length <= 1 && matches.every(a => a.expired === false && Number.isSafeInteger(a.id) && a.id > 0), 'Original DeliDev candidate is expired or ambiguous');
  if (!matches.length) return false;
  await download(matches[0],directory);
  if (signingInput) await verifySigningInput(directory,target,expected);
  else await verifyCandidate(directory,target,expected);
  return true;
}
async function main() {
  requireValue(process.env.GITHUB_REPOSITORY === 'delinoio/oss', 'Candidate restore requires delinoio/oss');
  const runId = process.env.GITHUB_RUN_ID, target = process.env.RELEASE_TARGET;
  const expected = identity(process.env.RELEASE_VERSION,process.env.RELEASE_REVISION);
  requireValue(releaseTargets.some(t => t.target === target) && /^[1-9]\d*$/.test(runId), 'Invalid restore target/run');
  const args = process.argv.slice(2);
  requireValue(args.length === 0 || args.length === 1 && args[0] === '--signing-input', 'Invalid candidate restore mode');
  const signingInput = args.length === 1;
  requireValue(!signingInput || releaseTargets.some(t => t.target === target && t.platform === 'darwin'), 'Unsupported signing input restore target');
  const directory = resolve(signingInput ? 'target/delidev-signing-input' : 'target/delidev-release-candidate',target);
  const gh = args => { try { return execFileSync('gh',args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],maxBuffer:8*1024**2}); } catch { throw new Error('DeliDev candidate GitHub operation failed'); } };
  const reused = await restoreCandidate({directory,target,expected,runId,signingInput,list:async () => {
    const pages = JSON.parse(gh(['api',`repos/delinoio/oss/actions/runs/${runId}/artifacts?per_page=100`,'--paginate','--slurp']));
    requireValue(Array.isArray(pages) && pages.every(p => Array.isArray(p.artifacts)), 'Invalid retained artifact listing');
    return pages.flatMap(p => p.artifacts);
  },download:async artifact => {
    // gh handles extraction; validate the exact closed file inventory and all
    // original byte digests before granting reuse, never restore another run.
    mkdirSync(directory,{recursive:true});
    gh(['run','download',runId,'--repo','delinoio/oss','--name',artifact.name,'--dir',directory]);
  }});
  if(process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT,`reused=${reused}\n`);
  console.log(JSON.stringify({component:'delidev.release.candidate',target,revision:expected.sourceRevision,reused}));
}
if(process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(() => { console.error(JSON.stringify({component:'delidev.release.candidate',outcome:'failed'}));process.exitCode=1; });
