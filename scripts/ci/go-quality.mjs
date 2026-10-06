import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { affectedGoPackages, GoMode, selectionOptions } from "./go-affected.mjs";

export function runGoQuality({ run = spawnSync, log = console.log, cwd = process.cwd(), ...options } = selectionOptions()) {
  const mode = options.mode ?? GoMode.Full;
  if (!Object.values(GoMode).includes(mode)) throw new Error("Unknown Go validation mode");
  const selection = mode === GoMode.Affected ? affectedGoPackages({ ...options, run, log, cwd }) : null;
  if (selection && selection.packages.length === 0) { log(JSON.stringify({ event: "ci_go_quality_empty" })); return 0; }
  const tracked = run("git", ["ls-files", "-z", "--", "*.go"], { cwd, shell: false, encoding: "utf8" });
  if (tracked.error) throw tracked.error;
  if (tracked.status !== 0) return tracked.status ?? 1;
  const directories = new Set(selection?.inventory.filter((item) => selection.packages.includes(item.path)).map((item) => item.directory));
  const files = tracked.stdout.split("\0").filter(Boolean).filter((file) => !selection || directories.has(dirname(file).replaceAll("\\", "/")));
  if (files.length === 0) throw new Error("Go quality selected no tracked source files");
  // Batch argv to stay below Windows/macOS process argument limits without
  // invoking a shell or allowing gofmt to rewrite tracked source files.
  for (let index = 0; index < files.length; index += 100) {
    const format = run("gofmt", ["-l", ...files.slice(index, index + 100)], { cwd, shell: false, encoding: "utf8" });
    if (format.error) throw format.error;
    if (format.status !== 0) return format.status ?? 1;
    if (format.stdout.trim()) { log(format.stdout); return 1; }
  }
  const result = run("go", ["vet", ...(selection?.packages ?? ["./..."])], { cwd, shell: false, stdio: "inherit" });
  if (result.error) throw result.error;
  log(JSON.stringify({ event: "ci_go_quality_complete", mode, fileCount: files.length, exitCode: result.status ?? 1 }));
  return result.status ?? 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { process.exitCode = runGoQuality(); }
  catch (error) { console.error(JSON.stringify({ event: "ci_go_quality_failed", message: error.message })); process.exitCode = 1; }
}
