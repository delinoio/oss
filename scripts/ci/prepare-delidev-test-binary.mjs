import { spawnSync } from "node:child_process";
import { appendFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

if (!process.env.GITHUB_ENV) throw new Error("Fixture preparation requires the CI environment file");
const directory = mkdtempSync(join(tmpdir(), "delidev-ci-binary-"));
const binary = join(directory, process.platform === "win32" ? "delidev.exe" : "delidev");
// Cold runners download modules and compile SQLite before any fixture runs.
// This five-minute preparation bound does not change fixture or product deadlines.
const buildWatchdogMs = 5 * 60 * 1000;
const start = performance.now();
console.log(JSON.stringify({ event: "ci_delidev_fixture_build_start", watchdogSeconds: buildWatchdogMs / 1000 }));
const result = spawnSync("go", ["build", "-mod=readonly", "-o", binary, "./cmds/delidev-cli"], { shell: false, stdio: "inherit", timeout: buildWatchdogMs });
if (result.error || result.status !== 0) {
  console.error(JSON.stringify({ event: "ci_delidev_fixture_build", result: "failure", code: result.error?.code ?? null, exitCode: result.status, elapsedSeconds: Math.round((performance.now() - start) / 1000) }));
  if (result.error) throw result.error;
  process.exit(result.status ?? 1);
}
appendFileSync(process.env.GITHUB_ENV, `DELIDEV_TEST_BINARY=${binary}\n`);
console.log(JSON.stringify({ event: "ci_delidev_fixture_build", elapsedSeconds: Math.round((performance.now() - start) / 1000) }));
