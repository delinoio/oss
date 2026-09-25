import { appendFileSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { matchesGlob, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const jobPaths = JSON.parse(readFileSync(new URL("./job-paths.json", import.meta.url), "utf8"));
export const nativeMatrices = JSON.parse(readFileSync(new URL("./native-matrices.json", import.meta.url), "utf8"));
export const Event = Object.freeze({ PullRequest: "pull_request", Push: "push", Manual: "workflow_dispatch" });
const configuration = [".github/workflows/CI.yml", ".github/actions/**", "scripts/ci/plan.mjs", "scripts/ci/result.mjs", "scripts/ci/native-matrices.json"];
const matches = (path, patterns) => patterns.some((pattern) => matchesGlob(path, pattern));

function ruleSignature(rule) {
  if (!rule) return null;
  const canonical = (value) => {
    if (Array.isArray(value)) return value.map(canonical).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
    if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => [key, canonical(item)]));
    return value;
  };
  return JSON.stringify(canonical(rule));
}

export function matricesForEvent(event) {
  if (!Object.values(Event).includes(event)) throw new Error(`Unsupported CI event: ${event}`);
  const full = event === Event.Manual;
  return {
    desktopMatrix: { include: nativeMatrices["devhud-desktop"].filter((row) => full || row.os !== "macos") },
    reactForgeMatrix: { include: nativeMatrices["react-forge"].filter((row) => full || row.platform !== "darwin") },
  };
}

export function planJobs(event, paths, previousRules = jobPaths) {
  if (!Object.values(Event).includes(event)) throw new Error(`Unsupported CI event: ${event}`);
  const force = event === Event.Manual || paths.some((path) => matches(path, configuration));
  const rulesChanged = paths.includes("scripts/ci/job-paths.json");
  const jobs = {};
  const forced = {};
  for (const [id, rule] of Object.entries(jobPaths)) {
    const relevant = paths.filter((path) => matches(path, rule.paths));
    const ruleChanged = rulesChanged && ruleSignature(rule) !== ruleSignature(previousRules[id]);
    const eligible = (!rule.native || event !== Event.PullRequest) && (id !== "devhud-ios-simulator" || event === Event.Manual);
    jobs[id] = eligible && (force || ruleChanged || relevant.length > 0);
    // Turbo cannot discover inputs outside the workspace graph, such as installer
    // scripts and native pin contracts. Run their owning workspace explicitly.
    forced[id] = force || ruleChanged || Boolean(rule.workspace && relevant.some((path) => !matches(path, rule.workspacePaths)));
  }
  return { jobs, forced };
}

export function previousJobPaths(base, cwd = process.cwd()) {
  const file = "scripts/ci/job-paths.json";
  const revision = commit(base);
  const options = { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] };
  execFileSync("git", ["cat-file", "-e", `${revision}^{commit}`], options);
  try {
    execFileSync("git", ["cat-file", "-e", `${revision}:${file}`], options);
  } catch (error) {
    if (error.status !== 128) throw error;
    // A newly introduced rules file has no historical jobs to compare.
    return {};
  }
  return JSON.parse(execFileSync("git", ["show", `${revision}:${file}`], options));
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
  const oldRules = range.paths.includes("scripts/ci/job-paths.json") ? previousJobPaths(range.base) : jobPaths;
  const plan = planJobs(env.GITHUB_EVENT_NAME, range.paths, oldRules);
  const matrices = matricesForEvent(env.GITHUB_EVENT_NAME);
  const outputs = { base: range.base, head: range.head, event: env.GITHUB_EVENT_NAME, jobs: JSON.stringify(plan.jobs), forced: JSON.stringify(plan.forced), desktop_matrix: JSON.stringify(matrices.desktopMatrix), react_forge_matrix: JSON.stringify(matrices.reactForgeMatrix) };
  appendFileSync(env.GITHUB_OUTPUT, Object.entries(outputs).map(([key, value]) => `${key}=${value}\n`).join(""));
  console.log(JSON.stringify({ event: "ci_plan", mode: env.GITHUB_EVENT_NAME, base: range.base, head: range.head, changedFiles: range.paths.length, ...plan, ...matrices }));
  if (env.GITHUB_STEP_SUMMARY) {
    appendFileSync(env.GITHUB_STEP_SUMMARY, `## CI execution plan\n\nMode: ${env.GITHUB_EVENT_NAME}\n\n| Job | Decision |\n| --- | --- |\n${Object.entries(plan.jobs).map(([id, selected]) => `| ${id} | ${selected ? "run" : "skip"} |`).join("\n")}\n`);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
