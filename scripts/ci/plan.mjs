import { appendFileSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { posix, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { developmentMatrix as pnportMatrix } from "../../packages/pnport/scripts/native-matrix.mjs";

export const jobPaths = JSON.parse(
  readFileSync(new URL("./job-paths.json", import.meta.url), "utf8"),
);
export const nativeMatrices = JSON.parse(
  readFileSync(new URL("./native-matrices.json", import.meta.url), "utf8"),
);
export const Mode = Object.freeze({ Affected: "affected" });
export function executionMode(event, mode = Mode.Affected) {
  if (!Object.values(Event).includes(event))
    throw new Error(`Unsupported CI event: ${event}`);
  if (!Object.values(Mode).includes(mode))
    throw new Error(`Unsupported CI mode: ${mode}`);
  return mode;
}
export const Event = Object.freeze({
  PullRequest: "pull_request",
  Push: "push",
  Manual: "workflow_dispatch",
});
const configuration = [
  ".gitattributes",
  ".github/workflows/CI.yml",
  ".github/actions/**",
  "scripts/ci/plan.mjs",
  "scripts/ci/result.mjs",
  "scripts/ci/run-affected.mjs",
  "scripts/ci/native-matrices.json",
  "scripts/ci/package.json",
  "scripts/ci/turbo.json",
  "scripts/ci/from-root.mjs",
  "scripts/ci/buf-entry.mjs",
  "scripts/ci/cache-context.mjs",
  "scripts/ci/protocol-fresh.mjs",
  "scripts/ci/rust-affected*.mjs",
  "scripts/ci/cargo-mono-prebuilt*.mjs",
  "scripts/ci/run-rust.mjs",
];
const rustfmtConfiguration = new Set([".rustfmt.toml", "rustfmt.toml"]);
// Git reports POSIX paths. Filename ownership also covers hidden directories,
// which node:path's recursive globs do not match.
const isRustfmtConfiguration = (path) =>
  rustfmtConfiguration.has(path.slice(path.lastIndexOf("/") + 1));
export function matchesPath(path, pattern) {
  // Node's matcher excludes leading dots at every wildcard boundary and has no
  // dot option. Prefix each segment on both sides without removing filename
  // characters; keep pattern globstars bare for zero-or-more-directory matching.
  // Git paths use POSIX separators on every host. Remove this adapter when Node
  // supports dot-aware matching that passes the same path fixtures.
  const visiblePath = path
    .split("/")
    .map((segment) => `x${segment}`)
    .join("/");
  const visiblePattern = pattern
    .split("/")
    .map((segment) => (segment === "**" ? segment : `x${segment}`))
    .join("/");
  return posix.matchesGlob(visiblePath, visiblePattern);
}

const matches = (path, patterns) =>
  patterns.some((pattern) => matchesPath(path, pattern));

function ruleSignature(rule) {
  if (!rule) return null;
  const canonical = (value) => {
    if (Array.isArray(value))
      return value
        .map(canonical)
        .sort((left, right) =>
          JSON.stringify(left).localeCompare(JSON.stringify(right)),
        );
    if (value && typeof value === "object")
      return Object.fromEntries(
        Object.entries(value)
          .sort(([left], [right]) => left.localeCompare(right))
          .map(([key, item]) => [key, canonical(item)]),
      );
    return value;
  };
  return JSON.stringify(canonical(rule));
}

export function matricesForEvent(event, mode = Mode.Affected) {
  executionMode(event, mode);
  const pr = event !== Event.Push;
  const hosts = (linux) => ({ os: [linux, "windows-latest"] });
  return {
    goTestMatrix: {
      include: nativeMatrices[pr ? "go-test-pr" : "go-test"].filter(
        (row) => !row.os.startsWith("macos"),
      ),
    },
    environmentMatrix: hosts("ubuntu-latest"),
    forgeMatrix: hosts("ubuntu-latest"),
    cliboxMatrix: hosts("ubuntu-22.04"),
    desktopMatrix: {
      include: nativeMatrices["devhud-desktop"].filter(
        (row) => row.os !== "macos",
      ),
    },
    reactForgeMatrix: {
      include: nativeMatrices["react-forge"].filter(
        (row) => row.platform !== "darwin",
      ),
    },
    pnportMatrix: {
      include: pnportMatrix.include.filter(
        (row) => !row.runner.startsWith("macos"),
      ),
    },
  };
}

export function planJobs(
  event,
  paths,
  previousRules = jobPaths,
  mode = Mode.Affected,
) {
  executionMode(event, mode);
  const pr = event !== Event.Push;
  // Broad source/configuration-directory rules also match rustfmt overrides.
  // Only rust-fmt owns these files, even inside a native package or action.
  const nonFormattingPaths = paths.filter(
    (path) => !isRustfmtConfiguration(path),
  );
  const force = nonFormattingPaths.some((path) => matches(path, configuration));
  const rulesChanged = paths.includes("scripts/ci/job-paths.json");
  const jobs = {};
  const forced = {};
  for (const [id, rule] of Object.entries(jobPaths)) {
    const relevant = (id === "rust-fmt" ? paths : nonFormattingPaths).filter(
      (path) =>
        (id === "rust-fmt" && isRustfmtConfiguration(path)) ||
        matches(path, rule.paths),
    );
    const ruleChanged =
      rulesChanged && ruleSignature(rule) !== ruleSignature(previousRules[id]);
    const eligible = !rule.native || !pr;
    jobs[id] = eligible && (force || ruleChanged || relevant.length > 0);
    // Turbo cannot discover inputs outside the workspace graph, such as installer
    // scripts and native pin contracts. Run their owning workspace explicitly.
    forced[id] =
      force ||
      ruleChanged ||
      Boolean(
        rule.workspace &&
        relevant.some((path) => !matches(path, rule.workspacePaths)),
      );
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
  return JSON.parse(
    execFileSync("git", ["show", `${revision}:${file}`], options),
  );
}

function commit(value) {
  if (
    typeof value !== "string" ||
    !/^[0-9a-f]{40}$/u.test(value) ||
    /^0+$/u.test(value)
  ) {
    throw new Error("CI comparison requires a nonzero 40-character commit SHA");
  }
  return value;
}

export function changedFiles(
  eventName,
  event,
  sha,
  cwd = process.cwd(),
  { mode = Mode.Affected, ref, mainSha } = {},
) {
  executionMode(eventName, mode);
  const git = (...args) =>
    execFileSync("git", args, {
      cwd,
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
    });
  const head = commit(
    eventName === Event.PullRequest ? event.pull_request?.head?.sha : sha,
  );
  git("cat-file", "-e", `${head}^{commit}`);
  let base;
  if (eventName === Event.PullRequest) {
    base = git(
      "merge-base",
      commit(event.pull_request?.base?.sha),
      head,
    ).trim();
  } else if (eventName === Event.Manual) {
    if (ref === "refs/heads/main") base = git("rev-parse", `${head}^`).trim();
    else {
      if (typeof ref !== "string" || !ref.startsWith("refs/heads/"))
        throw new Error("Manual CI requires a branch ref");
      const main = commit(mainSha);
      git("cat-file", "-e", `${main}^{commit}`);
      base = git("merge-base", main, head).trim();
    }
  } else if (eventName === Event.Push) {
    // Use the entire pushed range, including non-linear/force pushes. A missing
    // comparison is an error, never an empty affected set.
    base = commit(event.before);
  } else throw new Error(`Unsupported CI event: ${eventName}`);
  commit(base);
  // Disabling rename detection includes both the deleted and added paths. NUL
  // separation preserves whitespace and newline-bearing filenames exactly.
  const paths = git(
    "diff",
    "--name-only",
    "--no-renames",
    "-z",
    base,
    head,
    "--",
  )
    .split("\0")
    .filter(Boolean);
  return { base, head, paths };
}

export function main(env = process.env) {
  const event = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8"));
  const mode = executionMode(
    env.GITHUB_EVENT_NAME,
    env.GITHUB_EVENT_NAME === Event.Manual
      ? (event.inputs?.mode ?? Mode.Affected)
      : Mode.Affected,
  );
  const mainSha =
    env.GITHUB_EVENT_NAME === Event.Manual &&
    env.GITHUB_REF !== "refs/heads/main"
      ? commit(
          execFileSync("git", ["rev-parse", "refs/remotes/origin/main"], {
            encoding: "utf8",
          }).trim(),
        )
      : undefined;
  const range = changedFiles(
    env.GITHUB_EVENT_NAME,
    event,
    env.GITHUB_SHA,
    process.cwd(),
    { mode, ref: env.GITHUB_REF, mainSha },
  );
  const oldRules = range.paths.includes("scripts/ci/job-paths.json")
    ? previousJobPaths(range.base)
    : jobPaths;
  const plan = planJobs(env.GITHUB_EVENT_NAME, range.paths, oldRules, mode);
  const matrices = matricesForEvent(env.GITHUB_EVENT_NAME, mode);
  const outputs = {
    mode,
    main_sha: mainSha ?? "",
    environment_matrix: JSON.stringify(matrices.environmentMatrix),
    forge_matrix: JSON.stringify(matrices.forgeMatrix),
    clibox_matrix: JSON.stringify(matrices.cliboxMatrix),
    base: range.base,
    head: range.head,
    event: env.GITHUB_EVENT_NAME,
    jobs: JSON.stringify(plan.jobs),
    forced: JSON.stringify(plan.forced),
    go_test_matrix: JSON.stringify(matrices.goTestMatrix),
    desktop_matrix: JSON.stringify(matrices.desktopMatrix),
    react_forge_matrix: JSON.stringify(matrices.reactForgeMatrix),
    pnport_matrix: JSON.stringify(matrices.pnportMatrix),
  };
  appendFileSync(
    env.GITHUB_OUTPUT,
    Object.entries(outputs)
      .map(([key, value]) => `${key}=${value}\n`)
      .join(""),
  );
  console.log(
    JSON.stringify({
      event: "ci_plan",
      trigger: env.GITHUB_EVENT_NAME,
      mode,
      mainSha,
      selectedJobs: Object.values(plan.jobs).filter(Boolean).length,
      base: range.base,
      head: range.head,
      changedFiles: range.paths.length,
      ...plan,
      ...matrices,
    }),
  );
  if (env.GITHUB_STEP_SUMMARY) {
    appendFileSync(
      env.GITHUB_STEP_SUMMARY,
      `## CI execution plan\n\nEvent: ${env.GITHUB_EVENT_NAME}; mode: ${mode}\n\nComparison: ${range.base}..${range.head}\n\nMain revision: ${mainSha ?? "not used"}\n\nSelected jobs: ${Object.values(plan.jobs).filter(Boolean).length}\n\nPlatforms: ${JSON.stringify(matrices)}\n\n| Job | Decision |\n| --- | --- |\n${Object.entries(
        plan.jobs,
      )
        .map(([id, selected]) => `| ${id} | ${selected ? "run" : "skip"} |`)
        .join("\n")}\n`,
    );
  }
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  main();
