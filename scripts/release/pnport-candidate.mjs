import { readFileSync, writeFileSync, appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Bump, Project, git, validateCommit, versionChanges } from "./project.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
function ensure(condition, message) { if (!condition) throw new Error(message); }
function output(values) {
  for (const [key, value] of Object.entries(values)) {
    ensure(/^[a-z_]+$/u.test(key) && !/[\r\n]/u.test(String(value)), "Unsafe candidate output");
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
  console.log(JSON.stringify({ event: "pnport_candidate", ...values }));
}

export function recordedCandidate(directory, bump, runId) {
  const marker = `Release-Run: delinoio/oss/${runId}`;
  const candidates = git(directory, ["log", "HEAD", "--format=%H", "--fixed-strings", `--grep=${marker}`]).split("\n").filter(Boolean)
    .filter((sha) => git(directory, ["show", "-s", "--format=%B", sha]).split("\n").includes(marker));
  ensure(candidates.length <= 1, "Multiple pnport commits claim the release run");
  return candidates.length ? validateCommit(directory, candidates[0], Project.Pnport, bump, runId) : null;
}

export function applyCandidate({ directory, bump, runId, mode, base, expectedTree }) {
  ensure(Object.values(Bump).includes(bump) && /^[1-9]\d{0,19}$/u.test(runId), "Invalid pnport release selector");
  ensure(git(directory, ["status", "--porcelain"]) === "", "Candidate checkout must be clean");
  const source = mode === "source" ? recordedCandidate(directory, bump, runId) : null;
  const resumed = mode === "source" ? Boolean(source) : mode === "resume";
  ensure(mode === "source" || mode === "new" || mode === "resume", "Invalid candidate mode");
  const current = git(directory, ["rev-parse", "HEAD"]);
  if (resumed) {
    const identity = source ?? validateCommit(directory, current, Project.Pnport, bump, runId);
    ensure(mode === "source" || current === base, "Resumed candidate commit mismatch");
    const tree = git(directory, ["rev-parse", `${identity.revision}^{tree}`]);
    ensure(!expectedTree || tree === expectedTree, "Resumed candidate tree mismatch");
    return { mode: "resume", base: identity.revision, tree, version: identity.version, tag: identity.tag };
  }
  ensure(!base || current === base, "Candidate base commit mismatch");
  const plan = versionChanges(Project.Pnport, bump, (file) => readFileSync(path.join(directory, file), "utf8"));
  for (const [file, bytes] of Object.entries(plan.changes)) writeFileSync(path.join(directory, file), bytes);
  git(directory, ["add", "--", ...Object.keys(plan.changes)]);
  const tree = git(directory, ["write-tree"]);
  ensure(!expectedTree || tree === expectedTree, "Candidate source tree mismatch");
  return { mode: "new", base: current, tree, version: plan.version, tag: plan.tag };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    output(applyCandidate({ directory: root, bump: process.env.RELEASE_BUMP, runId: process.env.GITHUB_RUN_ID, mode: process.argv[2], base: process.env.RELEASE_CANDIDATE_BASE, expectedTree: process.env.RELEASE_CANDIDATE_TREE }));
  } catch (error) { console.error(JSON.stringify({ event: "pnport_candidate_failed", message: error.message })); process.exitCode = 1; }
}
