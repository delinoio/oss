import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Event, jobPaths, matricesForEvent, changedFiles, comparisonMode, planJobs, previousJobPaths } from "./plan.mjs";
import { rustJobs, validateRustPackages } from "./rust-affected.mjs";

export function validateResults(needs, comparison) {
  for (const id of ["changes", "ci-contracts"]) {
    if (needs[id]?.result !== "success") throw new Error(`${id} did not succeed`);
  }
  const expected = JSON.parse(needs.changes.outputs.jobs);
  const event = needs.changes.outputs.event;
  const outputs = needs.changes.outputs;
  const compared = outputs.mode === "manual-compared";
  if (compared && !comparison) throw new Error("Compared manual CI requires a verified comparison");
  if (comparison) {
    if (event !== comparison.event || outputs.mode !== comparison.mode || outputs.base !== comparison.range.base || outputs.head !== comparison.range.head) throw new Error("CI comparison identity mismatch");
    if (compared) {
      const planned = planJobs(event, comparison.range.paths, comparison.rules ?? jobPaths, { comparedManual: true });
      if (JSON.stringify(expected) !== JSON.stringify(planned.jobs)) throw new Error("Compared manual CI differs from owning job paths");
      const forced = JSON.parse(outputs.forced);
      for (const [id, selected] of Object.entries(expected)) if (selected && forced[id] !== true) throw new Error(`${id} must execute its complete workspace checks`);
    }
  }
  const rustPackages = validateRustPackages(JSON.parse(needs.changes.outputs.rust_packages));
  if (rustJobs.some(id => expected[id]) !== (rustPackages.length > 0)) throw new Error("Rust package selection differs from the planned jobs");
  const matrices = matricesForEvent(event);
  for (const [output, matrix] of [["go_test_matrix", matrices.goTestMatrix], ["desktop_matrix", matrices.desktopMatrix], ["react_forge_matrix", matrices.reactForgeMatrix], ["delidev_frontend_matrix", matrices.delidevFrontendMatrix]]) {
    const actual = JSON.parse(needs.changes.outputs[output]);
    if (JSON.stringify(actual) !== JSON.stringify(matrix)) throw new Error(`${output} differs from the ${event} policy`);
  }
  const ids = Object.keys(jobPaths).sort();
  if (JSON.stringify(Object.keys(expected).sort()) !== JSON.stringify(ids)) throw new Error("CI plan has an unexpected job inventory");
  const actualIds = Object.keys(needs).filter((id) => !["changes", "ci-contracts"].includes(id)).sort();
  if (JSON.stringify(actualIds) !== JSON.stringify(ids)) throw new Error("CI dependencies have an unexpected job inventory");
  for (const id of ids) {
    if (typeof expected[id] !== "boolean") throw new Error(`${id} has an invalid execution decision`);
    if (event === Event.Manual && !compared && !expected[id]) throw new Error(`${id} must run on manual CI`);
    if (id === "devhud-ios-simulator" && !compared && expected[id] !== (event === Event.Manual)) throw new Error(`${id} violates the event policy`);
    if (event === Event.PullRequest && jobPaths[id].native && expected[id]) throw new Error(`${id} cannot run on a pull request`);
    const required = expected[id] ? "success" : "skipped";
    if (needs[id]?.result !== required) throw new Error(`${id}: expected ${required}, got ${needs[id]?.result ?? "missing"}`);
  }
  return true;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
    const range = changedFiles(process.env.GITHUB_EVENT_NAME, event, process.env.GITHUB_SHA);
    validateResults(JSON.parse(process.env.CI_NEEDS), {
      event: process.env.GITHUB_EVENT_NAME, mode: comparisonMode(process.env.GITHUB_EVENT_NAME, event), range,
      rules: range.paths.includes("scripts/ci/job-paths.json") ? previousJobPaths(range.base) : jobPaths,
    });
    console.log(JSON.stringify({ event: "ci_result", result: "success" }));
  } catch (error) {
    console.error(JSON.stringify({ event: "ci_result", result: "failure", message: error.message }));
    process.exitCode = 1;
  }
}
