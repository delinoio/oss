import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import { createRequire } from "node:module";
import { ensure, event, metadata, packageRoot, root } from "./common.mjs";
import { archiveName } from "./package.mjs";
import { companion } from "./manifests.mjs";

const require = createRequire(import.meta.url);
const { targets } = require("../src/platforms.cjs");
const { values } = parseArgs({ options: { target: { type: "string" }, directory: { type: "string", default: path.join(packageRoot, "dist") } } });
const target = targets.find(({ rust }) => rust === values.target);
ensure(target && target.os === process.platform && target.cpu === process.arch, "Installed archive smoke requires its native host");
const release = path.resolve(values.directory, "archives");
const archive = archiveName(target);
const checksum = createHash("sha256").update(readFileSync(path.join(release, archive))).digest("hex");
const sums = path.join(release, "SHA256SUMS");
const previousSums = existsSync(sums) ? readFileSync(sums) : null;
const install = mkdtempSync(path.join(tmpdir(), "pnport-install-smoke-"));
try {
  writeFileSync(sums, `${checksum}  ${archive}\n`);
  const { version } = metadata();
  const command = target.os === "win32"
    ? ["pwsh", ["-NoProfile", "-NonInteractive", "-File", path.join(root, "scripts/install/pnport.ps1"), "-Version", version, "-SourceDir", release, "-InstallDir", install]]
    : ["bash", [path.join(root, "scripts/install/pnport.sh"), "--version", version, "--source-dir", release, "--install-dir", install]];
  const result = spawnSync(command[0], command[1], { encoding: "utf8" });
  assert.equal(result.status, 0, `Installer failed: ${result.stderr}`);
  const installed = path.join(install, ".pnport", "versions", version);
  ensure(existsSync(path.join(installed, target.binary)) && existsSync(path.join(installed, companion(target))), "Installed pair is incomplete");
  ensure(existsSync(path.join(installed, "LICENSE")), "Installed pnport license is missing");
  if (target.os !== "win32") ensure(existsSync(path.join(installed, "LICENSE.fspy")), "Installed fspy license is missing");
  const launcher = path.join(install, target.os === "win32" ? "pnport.cmd" : "pnport");
  ensure(existsSync(launcher), "Installer did not activate the public launcher");
  const invokeLauncher = () => target.os === "win32"
    ? spawnSync("pwsh", ["-NoProfile", "-NonInteractive", "-Command", "& $env:PNPORT_SMOKE_LAUNCHER --version; exit $LASTEXITCODE"], { encoding: "utf8", env: { ...process.env, PNPORT_SMOKE_LAUNCHER: launcher } })
    : spawnSync(launcher, ["--version"], { encoding: "utf8" });
  const executable = invokeLauncher();
  assert.equal(executable.status, 0, executable.stderr);
  assert.equal(executable.stdout.trim(), `pnport ${version}`);
  writeFileSync(sums, `${"0".repeat(64)}  ${archive}\n`);
  const rejected = spawnSync(command[0], command[1], { encoding: "utf8" });
  assert.notEqual(rejected.status, 0, "Tampered checksum was accepted");
  assert.equal(readFileSync(path.join(installed, target.binary)).length > 0, true);
  const afterRejection = invokeLauncher();
  assert.equal(afterRejection.status, 0, afterRejection.stderr);
  assert.equal(afterRejection.stdout.trim(), `pnport ${version}`);
  event("install_smoke", { target: target.suffix, version, sha256: checksum });
} finally {
  if (previousSums) writeFileSync(sums, previousSums); else rmSync(sums, { force: true });
  rmSync(install, { recursive: true, force: true });
}
