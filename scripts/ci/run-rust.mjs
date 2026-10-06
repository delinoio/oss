// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { validateRustPackages } from "./rust-affected.mjs";

// Explicit graph variants retain the central Cargo selection without eagerly
// building unselected desktop inputs. Turbo owns each variant's prerequisites.
export function rustTask(command, packages) {
  if (!["test", "clippy"].includes(command)) throw new Error("Expected Rust test or clippy command");
  validateRustPackages(packages);
  if (!packages.length) throw new Error("A scheduled Rust job requires a nonempty package selection");
  const owners = [
    ["devhud", "devhud"],
    ["delidev-desktop", "delidev"],
    ...(command === "test" ? [["pnport", "pnport"]] : []),
  ].filter(([name]) => packages.includes(name)).map(([, label]) => label);
  return `ci:rust:${command}${owners.length ? ":" + owners.join("-") : ""}`;
}

export function main(env = process.env) {
  const task = rustTask(process.argv[2], JSON.parse(env.RUST_PACKAGES));
  console.log(JSON.stringify({ event: "ci_rust_task_graph", task }));
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./run-affected.mjs", import.meta.url)), "@delinoio/ci", task], {
    stdio: "inherit", shell: false, env: { ...env, FORCE_RUN: "true" },
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) process.exitCode = main();
