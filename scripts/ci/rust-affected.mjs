import { execFileSync, spawnSync } from "node:child_process";
import { appendFileSync, mkdtempSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { isAbsolute, join, matchesGlob, posix, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { changedFiles, Event, jobPaths } from "./plan.mjs";

export const rustJobs = Object.freeze(["rust-test", "rust-clippy"]);
export const excludedRustPackages = Object.freeze(["forge-scene", "forge-glb", "forge-fbx"]);
export const RustSelection = Object.freeze({ Affected: "affected", Full: "full", Empty: "empty" });
const globalInputs = ["Cargo.toml", "Cargo.lock", ".cargo/**", "**/rust-toolchain", "**/rust-toolchain.toml", "rust-toolchain", "rust-toolchain.toml"];
const options = cwd => ({ cwd, encoding: "utf8", maxBuffer: 32 * 1024 * 1024, stdio: ["ignore", "pipe", "inherit"] });
const git = (cwd, ...args) => execFileSync("git", args, options(cwd));
const packageName = /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/u;
const commit = value => {
  if (typeof value !== "string" || !/^[0-9a-f]{40}$/u.test(value) || /^0+$/u.test(value)) throw new Error("Rust selection requires a nonzero commit SHA");
  return value;
};
function strings(value, label) {
  if (!Array.isArray(value) || value.some(item => typeof item !== "string" || item.includes("\0")) || new Set(value).size !== value.length) throw new Error(`Invalid cargo-mono ${label}`);
  return value;
}
export function validateRustPackages(value) {
  const packages = strings(value, "packages");
  if (packages.some(name => !packageName.test(name) || excludedRustPackages.includes(name))) throw new Error("Invalid or excluded Rust package selection");
  return packages;
}
function inventory(result, workspace) {
  if (typeof result?.workspace_root !== "string" || realpathSync(result.workspace_root) !== realpathSync(workspace) || !Array.isArray(result.packages)) throw new Error("Invalid cargo-mono workspace inventory");
  const directories = new Map();
  for (const entry of result.packages) {
    const path = entry?.manifest_path;
    if (!packageName.test(entry?.name ?? "") || directories.has(entry.name) || typeof path !== "string" || isAbsolute(path) || path.includes("\\") || path.includes("\0") || path.split("/").some(part => part === ".." || part === "") || posix.basename(path) !== "Cargo.toml") throw new Error("Invalid cargo-mono package inventory");
    directories.set(entry.name, posix.dirname(path));
  }
  if (!directories.size) throw new Error("cargo-mono returned an empty workspace inventory");
  return directories;
}
function changedResult(result, workspace, range, names) {
  if (typeof result?.workspace_root !== "string" || realpathSync(result.workspace_root) !== realpathSync(workspace) || result.base_ref !== range.base || result.include_uncommitted !== false || result.direct_only !== false) throw new Error("Invalid cargo-mono changed result");
  commit(result.merge_base);
  strings(result.files, "files");
  strings(result.packages, "packages");
  if (result.packages.some(name => !names.has(name))) throw new Error("cargo-mono selected an unknown workspace package");
  return result;
}

export function selectRustPackages({ event, range, plan, cwd = process.cwd(), binary,
  validationHead = git(cwd, "rev-parse", "HEAD").trim(),
  run = (file, args, directory, env) => JSON.parse(execFileSync(file, args, { ...options(directory), env })),
}) {
  if (!Object.values(Event).includes(event)) throw new Error("Unsupported Rust CI event");
  commit(range.base); commit(range.head); commit(validationHead);
  const jobs = { ...plan.jobs };
  if (!rustJobs.some(id => jobs[id])) return { jobs, packages: [], mode: RustSelection.Empty, reason: "no-rust-jobs", validationHead };
  if (!isAbsolute(binary ?? "")) throw new Error("Rust selection requires the verified cargo-mono executable path");
  let reason;
  if (event === Event.Manual) reason = "manual";
  else if (rustJobs.some(id => jobs[id] && plan.forced[id])) reason = "forced";
  else if (range.paths.some(path => globalInputs.some(pattern => matchesGlob(path, pattern)))) reason = "shared-input";
  // Package detection runs at the event head, while PR tests retain the merge
  // checkout. A changed dependency graph requires the merged workspace baseline.
  if (!reason && validationHead !== range.head && git(cwd, "diff", "--name-only", "-z", range.head, validationHead, "--", "Cargo.toml", "Cargo.lock", ":(glob)**/Cargo.toml", ":(glob)**/Cargo.lock", ".cargo", "rust-toolchain", "rust-toolchain.toml").length) reason = "merge-graph";
  if (!reason) {
    const ancestor = spawnSync("git", ["merge-base", "--is-ancestor", range.base, range.head], options(cwd));
    if (ancestor.error) throw ancestor.error;
    if (ancestor.status === 1) reason = "non-linear-range";
    else if (ancestor.status !== 0) throw new Error("Unable to validate Rust comparison ancestry");
  }
  const temporary = mkdtempSync(join(tmpdir(), "ci-rust-selection-"));
  const workspace = join(temporary, "workspace");
  let added = false;
  try {
    // Metadata does not read LFS payloads. Real compilation checkouts hydrate
    // assets separately; this temporary reference never builds or tests source.
    execFileSync("git", ["worktree", "add", "--detach", workspace, reason ? validationHead : range.head], { ...options(cwd), env: { ...process.env, GIT_LFS_SKIP_SMUDGE: "1" } });
    added = true;
    const count = Number(process.env.GIT_CONFIG_COUNT ?? "0");
    if (!Number.isSafeInteger(count) || count < 0) throw new Error("Invalid inherited Git configuration count");
    const environment = { ...process.env, GIT_CONFIG_COUNT: String(count + 2), [`GIT_CONFIG_KEY_${count}`]: "diff.renames", [`GIT_CONFIG_VALUE_${count}`]: "false", [`GIT_CONFIG_KEY_${count + 1}`]: "core.quotepath", [`GIT_CONFIG_VALUE_${count + 1}`]: "false" };
    const names = inventory(run(binary, ["--output", "json", "list"], workspace, environment), workspace);
    if (!reason && range.paths.some(path => rustJobs.some(id => jobs[id] && jobPaths[id].paths.some(pattern => matchesGlob(path, pattern))) && ![...names.values()].some(directory => directory === "." || path.startsWith(directory + "/")))) reason = "external-input";
    let packages;
    if (reason) packages = [...names.keys()];
    else {
      const changed = changedResult(run(binary, ["--output", "json", "changed", "--base", range.base], workspace, environment), workspace, range, names);
      // The released CLI uses newline-separated paths. Never treat a quoted,
      // trimmed or truncated path as an empty affected set. Remove this fallback
      // after a pinned release supports exact NUL-separated endpoint comparisons.
      if (changed.merge_base !== range.base || JSON.stringify([...changed.files].sort()) !== JSON.stringify([...range.paths].sort())) {
        reason = "path-comparison";
        packages = [...names.keys()];
      } else packages = changed.packages;
    }
    // pnport discovers this companion at runtime, outside Cargo's path graph.
    if (packages.includes("pnport-preload")) {
      if (!names.has("pnport")) throw new Error("pnport-preload requires its pnport runtime test owner");
      packages = [...new Set([...packages, "pnport"])];
    }
    packages = validateRustPackages(packages.filter(name => !excludedRustPackages.includes(name)).sort());
    git(workspace, "diff", "--exit-code", "--", "Cargo.lock");
    if (!packages.length) for (const id of rustJobs) jobs[id] = false;
    return { jobs, packages, mode: reason ? RustSelection.Full : packages.length ? RustSelection.Affected : RustSelection.Empty, reason: reason ?? (packages.length ? "changed-packages" : "no-affected-packages"), validationHead };
  } finally {
    try { if (added) git(cwd, "worktree", "remove", "--force", workspace); }
    finally { rmSync(temporary, { recursive: true, force: true }); }
  }
}

export function runRustCheck(command, packages, { run = spawnSync, cwd = process.cwd() } = {}) {
  if (!["test", "clippy"].includes(command)) throw new Error("Expected Rust test or clippy command");
  validateRustPackages(packages);
  if (!packages.length) throw new Error("A scheduled Rust job requires a nonempty package selection");
  const args = [command, "--locked", ...packages.flatMap(name => ["-p", name]), "--all-targets", ...(command === "clippy" ? ["--all-features", "--", "-D", "warnings"] : [])];
  console.log(JSON.stringify({ event: "ci_rust_check", command, revision: git(cwd, "rev-parse", "HEAD").trim(), packages, args, result: "started" }));
  const result = run("cargo", args, { cwd, stdio: "inherit", shell: false });
  if (result.error) throw result.error;
  const status = result.status ?? 1;
  console.log(JSON.stringify({ event: "ci_rust_check", command, result: status === 0 ? "success" : "failure", status, signal: result.signal ?? null }));
  return status;
}

export function main(env = process.env) {
  const command = process.argv[2];
  if (command !== "plan") return runRustCheck(command, JSON.parse(env.RUST_PACKAGES));
  const event = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8"));
  const range = changedFiles(env.GITHUB_EVENT_NAME, event, env.GITHUB_SHA);
  if (range.base !== env.CI_BASE || range.head !== env.CI_HEAD) throw new Error("Rust comparison differs from the central CI plan");
  const selection = selectRustPackages({ event: env.GITHUB_EVENT_NAME, range, plan: { jobs: JSON.parse(env.CI_JOBS), forced: JSON.parse(env.CI_FORCED) }, binary: env.CARGO_MONO_BINARY });
  appendFileSync(env.GITHUB_OUTPUT, `jobs=${JSON.stringify(selection.jobs)}\nrust_packages=${JSON.stringify(selection.packages)}\n`);
  console.log(JSON.stringify({ event: "ci_rust_plan", base: range.base, head: range.head, ...selection }));
  if (env.GITHUB_STEP_SUMMARY) appendFileSync(env.GITHUB_STEP_SUMMARY, `\n## Final Rust execution plan\n\nMode: ${selection.mode}; reason: ${selection.reason}\n\nComparison: ${range.base}..${range.head}\n\nValidation revision: ${selection.validationHead}\n\nPackages: ${selection.packages.join(", ") || "none"}\n\n| Job | Final decision |\n| --- | --- |\n${rustJobs.map(id => `| ${id} | ${selection.jobs[id] ? "run" : "skip"} |`).join("\n")}\n`);
  return 0;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) process.exitCode = main();
