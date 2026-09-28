import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Event, jobPaths, matricesForEvent } from "./plan.mjs";

export function validateResults(needs) {
  for (const id of ["changes", "ci-contracts"]) {
    if (needs[id]?.result !== "success") throw new Error(`${id} did not succeed`);
  }
  const expected = JSON.parse(needs.changes.outputs.jobs);
  const event = needs.changes.outputs.event;
  const matrices = matricesForEvent(event);
  for (const [output, matrix] of [["desktop_matrix", matrices.desktopMatrix], ["react_forge_matrix", matrices.reactForgeMatrix]]) {
    const actual = JSON.parse(needs.changes.outputs[output]);
    if (JSON.stringify(actual) !== JSON.stringify(matrix)) throw new Error(`${output} differs from the ${event} policy`);
  }
  const ids = Object.keys(jobPaths).sort();
  if (JSON.stringify(Object.keys(expected).sort()) !== JSON.stringify(ids)) throw new Error("CI plan has an unexpected job inventory");
  const actualIds = Object.keys(needs).filter((id) => !["changes", "ci-contracts"].includes(id)).sort();
  if (JSON.stringify(actualIds) !== JSON.stringify(ids)) throw new Error("CI dependencies have an unexpected job inventory");
  for (const id of ids) {
    if (typeof expected[id] !== "boolean") throw new Error(`${id} has an invalid execution decision`);
    if (event === Event.Manual && !expected[id]) throw new Error(`${id} must run on manual CI`);
    if (id === "devhud-ios-simulator" && expected[id] !== (event === Event.Manual)) throw new Error(`${id} violates the event policy`);
    if (event === Event.PullRequest && jobPaths[id].native && expected[id]) throw new Error(`${id} cannot run on a pull request`);
    const required = expected[id] ? "success" : "skipped";
    if (needs[id]?.result !== required) throw new Error(`${id}: expected ${required}, got ${needs[id]?.result ?? "missing"}`);
  }
  return true;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    validateResults(JSON.parse(process.env.CI_NEEDS));
    console.log(JSON.stringify({ event: "ci_result", result: "success" }));
  } catch (error) {
    console.error(JSON.stringify({ event: "ci_result", result: "failure", message: error.message }));
    process.exitCode = 1;
  }
}
