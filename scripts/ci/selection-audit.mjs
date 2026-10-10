// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { changedFiles, comparisonMode, Event, jobPaths, planJobs, previousJobPaths } from "./plan.mjs";
import { rustJobs, validateRustPackages } from "./rust-affected.mjs";

export function selectionAudit(eventName, event, head, jobs, forced, packages, cwd = process.cwd()) {
  const range = changedFiles(eventName, event, head, cwd), mode = comparisonMode(eventName, event);
  const rules = range.paths.includes("scripts/ci/job-paths.json") ? previousJobPaths(range.base, cwd) : jobPaths;
  const planned = planJobs(eventName, range.paths, rules, { comparedManual: mode === "manual-compared" });
  if (JSON.stringify(Object.keys(jobs).sort()) !== JSON.stringify(Object.keys(jobPaths).sort())) throw new Error("Unexpected final job inventory");
  for (const id of Object.keys(jobPaths)) {
    if (typeof jobs[id] !== "boolean" || forced[id] !== planned.forced[id]) throw new Error("Invalid final job decision");
    // Affected Rust metadata may remove a planned job with no selected packages;
    // manual runs retain complete Rust workspaces whenever Rust is selected.
    if (jobs[id] !== planned.jobs[id] && !(eventName !== Event.Manual && rustJobs.includes(id) && !jobs[id] && packages.length === 0)) throw new Error("Final jobs differ from the execution plan");
  }
  validateRustPackages(packages);
  if (rustJobs.some(id => jobs[id]) !== (packages.length > 0)) throw new Error("Rust packages differ from final jobs");
  return { operation: "ci-validation-selection", mode, event: eventName, base: range.base, head: range.head, jobs, forced, rustPackages: packages, freshWorkspaceTests: eventName === Event.Manual, selectionFinalized: true };
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const audit = selectionAudit(process.env.GITHUB_EVENT_NAME, JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, "utf8")), process.env.GITHUB_SHA,
    JSON.parse(process.env.CI_FINAL_JOBS), JSON.parse(process.env.CI_FINAL_FORCED), JSON.parse(process.env.CI_FINAL_RUST_PACKAGES));
  const checkoutHead = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  if (checkoutHead !== audit.head && audit.event !== Event.PullRequest) throw new Error("Validation checkout differs from planned head");
  writeFileSync(process.env.CI_PLAN_FILE, `${JSON.stringify(audit, null, 2)}\n`);
  console.log(JSON.stringify(audit));
}
