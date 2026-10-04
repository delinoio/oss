import assert from 'node:assert/strict';
import test from 'node:test';
import { requireTagDryRun } from '../scripts/release-dry-run.mjs';
import { nativeMatrix } from '../scripts/native-matrix.mjs';

const revision = '1'.repeat(40);
const tag = 'pnport@v0.1.0';
const run = { id: 17, head_branch: tag, head_sha: revision, event: 'push', path: '.github/workflows/release-pnport.yml', status: 'completed', conclusion: 'success', repository: { full_name: 'delinoio/oss' }, head_repository: { full_name: 'delinoio/oss' } };
const jobs = [
  ...['prepare', 'package', 'summary', ...nativeMatrix.include.map(({ runner, target, suffix }) => `build (${runner}, ${target}, ${suffix})`)].map((name) => ({ name, status: 'completed', conclusion: 'success' })),
  ...['publish-npm', 'publish-release', 'homebrew'].map((name) => ({ name, status: 'completed', conclusion: 'skipped' })),
];
const request = (runs = [run], phases = jobs) => async (route) => route.includes('/jobs?') ? { jobs: phases } : { workflow_runs: runs };

test('publication accepts the complete nonpublishing exact-tag run only', async () => {
  assert.equal(await requireTagDryRun({ tag, revision }, request()), 17);
  for (const change of [{ head_branch: 'main' }, { head_sha: '2'.repeat(40) }, { event: 'workflow_dispatch' }, { path: '.github/workflows/other.yml' }, { repository: { full_name: 'fork/oss' } }, { head_repository: { full_name: 'fork/oss' } }, { id: '17' }, { status: 'in_progress' }, { conclusion: 'failure' }]) {
    await assert.rejects(requireTagDryRun({ tag, revision }, request([{ ...run, ...change }])));
  }
  await assert.rejects(requireTagDryRun({ tag, revision }, request([])));
});

test('newer pending or failed tag runs cannot be hidden by an older success', async () => {
  for (const conclusion of ['failure', 'cancelled', null]) {
    await assert.rejects(requireTagDryRun({ tag, revision }, request([run, { ...run, id: 18, conclusion }])), /has not succeeded/u);
  }
});

test('native/package failures and attempted publication never grant dry-run authority', async () => {
  for (const job of jobs) {
    const phases = jobs.map((phase) => phase.name === job.name ? { ...phase, conclusion: phase.conclusion === 'skipped' ? 'success' : 'failure' } : phase);
    await assert.rejects(requireTagDryRun({ tag, revision }, request([run], phases)), /proof failed/u);
  }
  await assert.rejects(requireTagDryRun({ tag, revision }, request([run], [{ name: 'unknown', status: 'completed' }, ...jobs.slice(1)])), /proof failed/u);
  for (const phases of [jobs.slice(1), [...jobs, jobs[0]], [...jobs.slice(1), jobs[1]]]) {
    await assert.rejects(requireTagDryRun({ tag, revision }, request([run], phases)), /Incomplete/u);
  }
});

test('discovery includes later pages and lookup errors block publication', async () => {
  const unrelated = Array.from({ length: 100 }, (_, id) => ({ ...run, id: id + 100, head_branch: 'other' }));
  assert.equal(await requireTagDryRun({ tag, revision }, async (route) => route.includes('/jobs?') ? { jobs } : { workflow_runs: route.endsWith('page=1') ? unrelated : [run] }), 17);
  for (const data of [{}, { workflow_runs: null }]) await assert.rejects(requireTagDryRun({ tag, revision }, async () => data));
  await assert.rejects(requireTagDryRun({ tag, revision }, async () => { throw new Error('lookup unavailable'); }), /lookup unavailable/u);
});
