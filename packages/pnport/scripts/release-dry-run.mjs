import { ensure } from './common.mjs';
import { nativeMatrix } from './native-matrix.mjs';

const prefix = '/repos/delinoio/oss/actions';
const requiredJobs = new Map([
  ['prepare', 'success'], ['package', 'success'], ['summary', 'success'],
  ...nativeMatrix.include.map(({ runner, target, suffix }) => [`build (${runner}, ${target}, ${suffix})`, 'success']),
  ['publish-npm', 'skipped'], ['publish-release', 'skipped'], ['homebrew', 'skipped'],
]);

// Only the latest push run for this immutable tag can grant publication.
// Branch dispatches and successful older attempts cannot hide a failed tag run.
export async function requireTagDryRun({ tag, revision }, request) {
  ensure(/^pnport@v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-next\.[1-9]\d*)?$/u.test(tag) && /^[a-f0-9]{40}$/u.test(revision), 'Invalid tag dry-run identity');
  const candidates = [];
  for (let page = 1; ; page++) {
    ensure(page <= 100, 'Tag dry-run discovery limit exceeded');
    const data = await request(`${prefix}/workflows/release-pnport.yml/runs?event=push&head_sha=${revision}&per_page=100&page=${page}`);
    ensure(Array.isArray(data?.workflow_runs) && data.workflow_runs.length <= 100, 'Invalid tag dry-run discovery');
    for (const run of data.workflow_runs) {
      if (run.head_branch !== tag || run.head_sha !== revision) continue;
      ensure(Number.isSafeInteger(run.id) && run.id > 0 && run.event === 'push' && run.path === '.github/workflows/release-pnport.yml' && run.repository?.full_name === 'delinoio/oss' && run.head_repository?.full_name === 'delinoio/oss', 'Untrusted tag dry-run identity');
      candidates.push(run);
    }
    if (data.workflow_runs.length < 100) break;
  }
  ensure(candidates.length > 0 && new Set(candidates.map(({ id }) => id)).size === candidates.length, 'Successful exact-tag dry run is required before publication');
  const run = candidates.reduce((latest, candidate) => candidate.id > latest.id ? candidate : latest);
  ensure(run.status === 'completed' && run.conclusion === 'success', 'Latest exact-tag dry run has not succeeded');
  const jobs = [];
  for (let page = 1; ; page++) {
    ensure(page <= 100, 'Tag dry-run job discovery limit exceeded');
    const data = await request(`${prefix}/runs/${run.id}/jobs?filter=latest&per_page=100&page=${page}`);
    ensure(Array.isArray(data?.jobs) && data.jobs.length <= 100, 'Invalid tag dry-run jobs');
    jobs.push(...data.jobs);
    if (data.jobs.length < 100) break;
  }
  ensure(jobs.length === requiredJobs.size && new Set(jobs.map(({ name }) => name)).size === requiredJobs.size, 'Incomplete tag dry-run jobs');
  for (const job of jobs) ensure(requiredJobs.has(job.name) && job.status === 'completed' && job.conclusion === requiredJobs.get(job.name), 'Tag dry-run native/package or nonpublication proof failed');
  return run.id;
}

export async function actionsRead(route) {
  ensure(route.startsWith(`${prefix}/`) && process.env.GH_TOKEN, 'Read-only Actions authority is required');
  const response = await fetch(`https://api.github.com${route}`, {
    headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' },
    redirect: 'error', signal: AbortSignal.timeout(30000),
  });
  ensure(response.ok, `Tag dry-run lookup failed: HTTP ${response.status}`);
  return response.json();
}
