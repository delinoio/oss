import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Event, executionMode, jobPaths, matricesForEvent } from "./plan.mjs";
import { rustJobs, validateRustPackages } from "./rust-affected.mjs";

export function validateResults(needs) {
  for (const id of ["changes", "ci-contracts"]) {
    if (needs[id]?.result !== "success")
      throw new Error(`${id} did not succeed`);
  }
  const expected = JSON.parse(needs.changes.outputs.jobs);
  const event = needs.changes.outputs.event;
  const mode = executionMode(event, needs.changes.outputs.mode);
  if (!needs.changes.outputs.mode) throw new Error("Missing CI mode");
  const pr = event !== Event.Push;
  const rustPackages = validateRustPackages(
    JSON.parse(needs.changes.outputs.rust_packages),
  );
  if (rustJobs.some((id) => expected[id]) !== rustPackages.length > 0)
    throw new Error("Rust package selection differs from the planned jobs");
  const matrices = matricesForEvent(event, mode);
  for (const [output, matrix] of [
    ["environment_matrix", matrices.environmentMatrix],
    ["forge_matrix", matrices.forgeMatrix],
    ["clibox_matrix", matrices.cliboxMatrix],
    ["pnport_matrix", matrices.pnportMatrix],
    ["go_test_matrix", matrices.goTestMatrix],
    ["desktop_matrix", matrices.desktopMatrix],
    ["react_forge_matrix", matrices.reactForgeMatrix],
  ]) {
    const actual = JSON.parse(needs.changes.outputs[output]);
    if (JSON.stringify(actual) !== JSON.stringify(matrix))
      throw new Error(`${output} differs from the ${event} policy`);
  }
  const ids = Object.keys(jobPaths).sort();
  if (JSON.stringify(Object.keys(expected).sort()) !== JSON.stringify(ids))
    throw new Error("CI plan has an unexpected job inventory");
  const actualIds = Object.keys(needs)
    .filter((id) => !["changes", "ci-contracts"].includes(id))
    .sort();
  if (JSON.stringify(actualIds) !== JSON.stringify(ids))
    throw new Error("CI dependencies have an unexpected job inventory");
  for (const id of ids) {
    if (typeof expected[id] !== "boolean")
      throw new Error(`${id} has an invalid execution decision`);
    if (pr && jobPaths[id].native && expected[id])
      throw new Error(`${id} cannot run with PR eligibility`);
    const required = expected[id] ? "success" : "skipped";
    if (needs[id]?.result !== required)
      throw new Error(
        `${id}: expected ${required}, got ${needs[id]?.result ?? "missing"}`,
      );
  }
  return true;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    validateResults(JSON.parse(process.env.CI_NEEDS));
    console.log(JSON.stringify({ event: "ci_result", result: "success" }));
  } catch (error) {
    console.error(
      JSON.stringify({
        event: "ci_result",
        result: "failure",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  }
}
