import { execFileSync, spawnSync } from "node:child_process";
import { readFileSync, readdirSync, realpathSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import yaml from "js-yaml";

export const WORKFLOW_DIRECTORY = ".github/workflows";

// Closed set of workflows allowed to declare GitHub's `queue: max` concurrency
// option. Each entry pins the owning workflow file, the exact declaration
// location and the exact text of the one line masked for actionlint.
export const QueueScope = Object.freeze({
  Workflow: "workflow",
  Job: "job",
});

export const ALLOWED_QUEUE_DECLARATIONS = Object.freeze([
  Object.freeze({
    file: "release-linux-packages.yml",
    scope: QueueScope.Job,
    job: "publish",
    concurrency: Object.freeze({
      group: "linux-package-repository",
      "cancel-in-progress": false,
      queue: "max",
    }),
    header: "    concurrency:",
    queueLine: "      queue: max",
  }),
]);

const MASKED_LINE_PREFIX = "# queue:max validated by repository contract";
const indentOf = (line) => line.length - line.trimStart().length;
const sameConcurrency = (actual, expected) =>
  actual !== null &&
  typeof actual === "object" &&
  JSON.stringify(Object.entries(actual).sort()) ===
    JSON.stringify(Object.entries(expected).sort());

/**
 * Validates every `queue` concurrency declaration of one workflow file and
 * returns the text to feed to actionlint.
 *
 * Returns `{ masked: false, source }` unchanged when the workflow declares no
 * queue. Returns `{ masked: true, source }` with only the single validated
 * queue line replaced by a same-indentation comment when the workflow matches
 * one allowed declaration exactly. Throws for every other queue declaration.
 */
export function prepareLintSource(name, source) {
  const workflow = yaml.load(source);
  const queuedJobs = Object.entries(workflow?.jobs ?? {}).filter(
    ([, job]) => job?.concurrency?.queue !== undefined,
  );
  const workflowQueue = workflow?.concurrency?.queue !== undefined;
  if (!workflowQueue && !queuedJobs.length) return { masked: false, source };

  const allowed = ALLOWED_QUEUE_DECLARATIONS.find(
    (declaration) => declaration.file === name,
  );
  const matches =
    allowed !== undefined &&
    (allowed.scope === QueueScope.Workflow
      ? workflowQueue &&
        queuedJobs.length === 0 &&
        sameConcurrency(workflow.concurrency, allowed.concurrency)
      : !workflowQueue &&
        queuedJobs.length === 1 &&
        queuedJobs[0][0] === allowed.job &&
        sameConcurrency(queuedJobs[0][1].concurrency, allowed.concurrency));
  if (!matches) throw new Error("Unsupported concurrency queue declaration");

  // actionlint 1.7.12 predates GitHub's queue:max. Validate the exact
  // declaration above, then omit only that line from a temporary lint input.
  // Remove this compatibility adapter when the pinned actionlint accepts queue.
  const lines = source.split("\n");
  const queueIndexes = lines.flatMap((line, index) =>
    /^\s*queue\s*:/u.test(line) ? [index] : [],
  );
  if (queueIndexes.length !== 1 || lines[queueIndexes[0]] !== allowed.queueLine)
    throw new Error("Unexpected queue syntax");
  const queueIndex = queueIndexes[0];
  // The line must sit directly inside the validated concurrency block, not in
  // another mapping that happens to be indented the same way.
  let parent = queueIndex - 1;
  while (
    parent >= 0 &&
    (lines[parent].trim() === "" ||
      indentOf(lines[parent]) >= indentOf(allowed.queueLine))
  )
    parent -= 1;
  if (parent < 0 || lines[parent] !== allowed.header)
    throw new Error("Unexpected queue syntax");

  lines[queueIndex] =
    `${" ".repeat(indentOf(allowed.queueLine))}${MASKED_LINE_PREFIX}`;
  return { masked: true, source: lines.join("\n") };
}

function describeLintFailure(step, error) {
  const detail = [
    error?.code !== undefined ? `code=${error.code}` : null,
    error?.status != null ? `status=${error.status}` : null,
    error?.signal ? `signal=${error.signal}` : null,
    `message=${JSON.stringify(error?.message ?? String(error))}`,
  ]
    .filter(Boolean)
    .join(" ");
  return `workflow lint failed: step=${JSON.stringify(step)} ${detail}\n`;
}

/**
 * Lints every workflow in `directory` with actionlint. Workflows without a
 * queue declaration are linted together as files; a workflow with a validated
 * queue declaration is linted individually from its masked text. Returns the
 * process exit code. Runners are injectable for tests.
 */
export function lintWorkflows(
  directory = WORKFLOW_DIRECTORY,
  {
    runFiles = (paths) =>
      execFileSync("go", ["tool", "actionlint", ...paths], {
        stdio: "inherit",
      }),
    runStdin = (file, input) =>
      spawnSync("go", ["tool", "actionlint", "-stdin-filename", file, "-"], {
        input,
        encoding: "utf8",
      }),
    stdout = process.stdout,
    stderr = process.stderr,
  } = {},
) {
  let exitCode = 0;
  const paths = [];
  for (const name of readdirSync(directory)
    .filter((name) => /\.ya?ml$/u.test(name))
    .sort()) {
    const file = path.join(directory, name);
    const prepared = prepareLintSource(name, readFileSync(file, "utf8"));
    if (!prepared.masked) {
      paths.push(file);
      continue;
    }
    const result = runStdin(file, prepared.source);
    stdout.write(result.stdout ?? "");
    stderr.write(result.stderr ?? "");
    if (result.error)
      stderr.write(describeLintFailure(`actionlint for ${file}`, result.error));
    if (result.status !== 0) exitCode = 1;
  }
  if (paths.length) {
    try {
      runFiles(paths);
    } catch (error) {
      // actionlint prints its own diagnostics on a lint failure; a spawn error
      // (for example a missing Go toolchain) has none, so always name the cause.
      stderr.write(describeLintFailure("actionlint batch", error));
      exitCode = 1;
    }
  }
  return exitCode;
}

const invokedDirectly =
  process.argv[1] !== undefined &&
  realpathSync(process.argv[1]) ===
    realpathSync(fileURLToPath(import.meta.url));
if (invokedDirectly) {
  const exitCode = lintWorkflows();
  if (exitCode !== 0) process.exitCode = exitCode;
}
