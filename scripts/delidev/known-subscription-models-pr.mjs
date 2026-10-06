// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from "node:child_process";
import { readFile, writeFile, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { catalogPath, collect, validateCatalog, changes, sameSourceProvenance } from "./known-subscription-models.mjs";

export const branch = "kdy1/delidev-known-subscription-model-catalog";
export const repository = "delinoio/oss";
const bot = "delino-release-bot[bot]";
const command = (name, args, env = process.env) => execFileSync(name, args, { encoding: "utf8", env, stdio: ["ignore", "pipe", "pipe"], maxBuffer: 4 << 20 }).trim();
const gh = args => command("gh", args);
const api = path => JSON.parse(gh(["api", path]));
export function assertBotBranch({ commits, files, total, pullRequests }) {
  if (total > 100 || commits.length !== total || commits.some(commit => commit.author?.login !== bot || commit.committer?.login !== bot)) throw new Error("Branch includes non-bot or incomplete history");
  if (files.some(file => file.filename !== catalogPath) || pullRequests.length > 1 || pullRequests.some(pr => pr.user?.login !== bot || pr.head.repo?.full_name !== repository || pr.base.ref !== "main")) throw new Error("Branch or PR has another owner");
}
export function findClosedCandidateReview(pullRequests, candidateVersion, readCatalog) {
  return pullRequests.find(pr => {
    if (pr.user?.login !== bot || pr.head?.repo?.full_name !== repository || pr.base?.ref !== "main" || !pr.head.sha) return false;
    return readCatalog(pr.head.sha).catalog_version === candidateVersion;
  });
}
export function describeUpdate(before, after, revision) {
  const diff = changes(before, after);
  const list = items => items.length ? items.map(item => `- \`${item}\``).join("\n") : "- None";
  return `Update the known subscription model suggestions from official native-tool metadata. Apps consume this catalog only after a person merges this PR. Existing saved models and execution history remain unchanged.

Added:
${list(diff.added)}

Changed (including minimum harness versions or announced retirement dates):
${list(diff.changed)}

Removed from new suggestions (retired or no longer listed by the official native source):
${list(diff.retired)}

Sources:
${after.sources.map(source => `- [${source.key}](${source.url}) — revision \`${source.revision}\`, SHA-256 \`${source.sha256}\``).join("\n")}

Validation at source revision \`${revision}\`:
- \`node --test scripts/ci/delidev-known-models.test.mjs\`: passed in the collecting workflow.
- \`node scripts/delidev/known-subscription-models.mjs --validate\`: passed before write-token creation and again before publication.
- Catalog version: \`${after.catalog_version}\`; reviewed-data date: ${after.updated_at}.
- Extraction requires all three nonempty bounded service inventories and rejects unsupported structure, hidden/API-only candidates, duplicates and invalid metadata. Date-only or source-byte-only changes do not create a PR.
- Limits: public metadata and synthetic parsing fixtures only. No subscription entitlement, real account, installed harness or native execution acceptance was tested. Ordinary PR CI must still pass.

No automatic merge is configured. A branch with any non-bot commit or another changed path is retained for manual review.\n`;
}
export async function publish() {
  if (process.env.GITHUB_REPOSITORY !== repository || !["schedule", "workflow_dispatch"].includes(process.env.GITHUB_EVENT_NAME) || process.env.GITHUB_REF !== "refs/heads/main") throw new Error("Untrusted publication context");
  const revision = command("git", ["rev-parse", "HEAD"]);
  const main = api(`repos/${repository}/git/ref/heads/main`).object.sha;
  if (main !== revision) throw new Error("Main changed during collection; collect again from its new revision");
  const candidate = validateCatalog(JSON.parse(await readFile(catalogPath, "utf8")));
  const previous = validateCatalog(JSON.parse(command("git", ["show", `${revision}:${catalogPath}`])));
  const candidateChanged = candidate.catalog_version !== previous.catalog_version;
  const pulls = api(`repos/${repository}/pulls?state=open&head=delinoio:${branch}&base=main&per_page=100`);
  const closedPulls = api(`repos/${repository}/pulls?state=closed&head=delinoio:${branch}&base=main&per_page=100`);
  const readReviewCatalog = sha => {
    const file = api(`repos/${repository}/contents/${catalogPath}?ref=${encodeURIComponent(sha)}`);
    if (file.encoding !== "base64" || file.size > 1 << 20) throw new Error("Closed candidate review unavailable");
    return validateCatalog(JSON.parse(Buffer.from(file.content, "base64").toString("utf8")));
  };
  // Inspect refs without treating a permission/network error as branch absence.
  const refs = api(`repos/${repository}/git/matching-refs/heads/${branch}`);
  const existing = refs.filter(ref => ref.ref === `refs/heads/${branch}`);
  if (existing.length > 1) throw new Error("Ambiguous bot branch");
  const old = existing[0]?.object.sha ?? "";
  if (!old && pulls.length) throw new Error("PR branch missing");
  if (old) {
    const comparison = api(`repos/${repository}/compare/${main}...${old}?per_page=100`);
    assertBotBranch({ commits: comparison.commits, files: comparison.files, total: comparison.total_commits, pullRequests: pulls });
    const currentFile = api(`repos/${repository}/contents/${catalogPath}?ref=${old}`);
    if (currentFile.encoding !== "base64" || currentFile.size > 1 << 20) throw new Error("Existing candidate unavailable");
    const current = validateCatalog(JSON.parse(Buffer.from(currentFile.content, "base64").toString("utf8")));
    if (!candidateChanged) {
      if (pulls.length) {
        // Main has caught up to the candidate. Close the stale review instead
        // of leaving an obsolete, apparently mergeable catalog PR open.
        gh(["pr", "close", String(pulls[0].number), "--repo", repository, "--comment", "Closing this review because the candidate is already present on main."]);
        return { changed: false, closed_pull_request: pulls[0].html_url };
      }
      return { changed: false };
    }
    // A pending review must not receive daily date-only commits either.
    if (current.catalog_version === candidate.catalog_version && pulls.length) return { changed: false, pull_request: pulls[0].html_url };
  } else {
    assertBotBranch({ commits: [], files: [], total: 0, pullRequests: pulls });
    if (!candidateChanged) return { changed: false };
  }
  if (!pulls.length) {
    const closed = findClosedCandidateReview(closedPulls, candidate.catalog_version, readReviewCatalog);
    if (closed) return { changed: false, closed_pull_request: closed.html_url };
  }
  // Re-fetch every recorded source after branch ownership checks and before
  // any candidate commit or push. A source revision/digest change means the
  // collected bytes are stale and must never become a review PR.
  const refreshed = await collect();
  if (!sameSourceProvenance(candidate, refreshed)) throw new Error("Official source changed during publication; collect again");
  const latestMain = api(`repos/${repository}/git/ref/heads/main`).object.sha;
  if (latestMain !== revision) throw new Error("Main changed during publication; collect again from its new revision");
  const identity = api(`users/${encodeURIComponent(bot)}`);
  if (identity.login !== bot || identity.type !== "Bot" || !Number.isSafeInteger(identity.id)) throw new Error("Bot identity unavailable");
  const email = `${identity.id}+${bot}@users.noreply.github.com`;
  const env = { ...process.env, GIT_AUTHOR_NAME: bot, GIT_COMMITTER_NAME: bot, GIT_AUTHOR_EMAIL: email, GIT_COMMITTER_EMAIL: email };
  command("git", ["checkout", "-B", branch, revision], env);
  command("git", ["add", "--", catalogPath], env);
  command("git", ["commit", "-m", "chore(delidev): update known subscription models"], env);
  // Exact old SHA (or an absent ref) closes the race with a later human push.
  // The token is an ephemeral environment-only Git header, never a remote URL
  // or a saved checkout credential. Captured errors are not printed.
  const pushEnv = { ...env };
  for (const key of Object.keys(pushEnv)) if (key.startsWith("GIT_CONFIG_")) delete pushEnv[key];
  Object.assign(pushEnv, { GIT_CONFIG_COUNT: "2", GIT_CONFIG_KEY_0: "http.https://github.com/.extraheader", GIT_CONFIG_VALUE_0: `AUTHORIZATION: basic ${Buffer.from(`x-access-token:${process.env.GH_TOKEN}`).toString("base64")}`, GIT_CONFIG_KEY_1: "credential.helper", GIT_CONFIG_VALUE_1: "" });
  command("git", ["push", `--force-with-lease=refs/heads/${branch}:${old}`, `https://github.com/${repository}.git`, `HEAD:refs/heads/${branch}`], pushEnv);
  const directory = await mkdtemp(join(tmpdir(), "delidev-model-pr-"));
  try {
    const body = join(directory, "body.md"); await writeFile(body, describeUpdate(previous, candidate, revision));
    const title = "chore(delidev): update known subscription models";
    if (pulls.length) { gh(["pr", "edit", String(pulls[0].number), "--repo", repository, "--title", title, "--body-file", body]); return { changed: true, pull_request: pulls[0].html_url }; }
    return { changed: true, pull_request: gh(["pr", "create", "--repo", repository, "--base", "main", "--head", branch, "--title", title, "--body-file", body]) };
  } finally { await rm(directory, { recursive: true, force: true }); }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(await publish())); }
  catch { console.error("Catalog PR publication stopped. Inspect branch ownership, current main, token permissions and PR state before a manual rerun. No automatic retry or merge was attempted."); process.exitCode = 1; }
}
