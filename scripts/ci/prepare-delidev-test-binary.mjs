import { spawnSync } from "node:child_process";
import { appendFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

if (!process.env.GITHUB_ENV) throw new Error("Fixture preparation requires the CI environment file");
const directory = mkdtempSync(join(tmpdir(), "delidev-ci-binary-"));
const binary = join(directory, process.platform === "win32" ? "delidev.exe" : "delidev");
const start = performance.now();
const result = spawnSync("go", ["build", "-mod=readonly", "-o", binary, "./cmds/delidev-cli"], { shell: false, stdio: "inherit", timeout: 120000 });
if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
appendFileSync(process.env.GITHUB_ENV, `DELIDEV_TEST_BINARY=${binary}\n`);
console.log(JSON.stringify({ event: "ci_delidev_fixture_build", elapsedSeconds: Math.round((performance.now() - start) / 1000) }));
