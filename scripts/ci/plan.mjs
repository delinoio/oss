import { appendFileSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { matchesGlob, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const jobPaths = JSON.parse(readFileSync(new URL("./job-paths.json", import.meta.url), "utf8"));
export const Event = Object.freeze({ PullRequest: "pull_request", Push: "push", Manual: "workflow_dispatch" });
const configuration = [".gitattributes", ".github/workflows/CI.yml", ".github/actions/**", "scripts/ci/**"];
const matches = (path, patterns) => patterns.some((pattern) => matchesGlob(path, pattern));

export function planJobs(event, paths) {
  if (!Object.values(Event).includes(event)) throw new Error(`Unsupported CI event: ${event}`);
  const force = event === Event.Manual || paths.some((path) => matches(path, configuration));
  const jobs = {};
  const forced = {};
  for (const [id, rule] of Object.entries(jobPaths)) {
    const relevant = paths.filter((path) => matches(path, rule.paths));
    jobs[id] = (!rule.native || event !== Event.PullRequest) && (force || relevant.length > 0);
    // Turbo cannot discover inputs outside the workspace graph, such as installer
    // scripts and native pin contracts. Run their owning workspace explicitly.
    forced[id] = force || Boolean(rule.workspace && relevant.some((path) => !matches(path, rule.workspacePaths)));
  }
  return { jobs, forced };
}

function commit(value) {
  if (typeof value !== "string" || !/^[0-9a-f]{40}$/u.test(value) || /^0+$/u.test(value)) {
    throw new Error("CI comparison requires a nonzero 40-character commit SHA");
  }
  return value;
}

export function changedFiles(eventName, event, sha, cwd = process.cwd()) {
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 });
  const head = commit(eventName === Event.PullRequest ? event.pull_request?.head?.sha : sha);
  git("cat-file", "-e", `${head}^{commit}`);
  if (eventName === Event.Manual) return { base: head, head, paths: [] };
  let base;
  if (eventName === Event.PullRequest) {
    base = git("merge-base", commit(event.pull_request?.base?.sha), head).trim();
  } else if (eventName === Event.Push) {
    // Use the entire pushed range, including non-linear/force pushes. A missing
    // comparison is an error, never an empty affected set.
    base = commit(event.before);
  } else throw new Error(`Unsupported CI event: ${eventName}`);
  commit(base);
  // Disabling rename detection includes both the deleted and added paths. NUL
  // separation preserves whitespace and newline-bearing filenames exactly.
  const paths = git("diff", "--name-only", "--no-renames", "-z", base, head, "--").split("\0").filter(Boolean);
  return { base, head, paths };
}

export function main(env = process.env) {
  const event = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8"));
  const range = changedFiles(env.GITHUB_EVENT_NAME, event, env.GITHUB_SHA);
  const plan = planJobs(env.GITHUB_EVENT_NAME, range.paths);
  const outputs = { base: range.base, head: range.head, jobs: JSON.stringify(plan.jobs), forced: JSON.stringify(plan.forced) };
  appendFileSync(env.GITHUB_OUTPUT, Object.entries(outputs).map(([key, value]) => `${key}=${value}\n`).join(""));
  console.log(JSON.stringify({ event: "ci_plan", mode: env.GITHUB_EVENT_NAME, base: range.base, head: range.head, changedFiles: range.paths.length, ...plan }));
  if (env.GITHUB_STEP_SUMMARY) {
    appendFileSync(env.GITHUB_STEP_SUMMARY, `## CI execution plan\n\nMode: ${env.GITHUB_EVENT_NAME}\n\n| Job | Decision |\n| --- | --- |\n${Object.entries(plan.jobs).map(([id, selected]) => `| ${id} | ${selected ? "run" : "skip"} |`).join("\n")}\n`);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
