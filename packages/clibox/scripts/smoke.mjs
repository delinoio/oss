import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import platforms from "../src/platforms.cjs";
import { ensure, event, metadata, npm, root } from "./common.mjs";
import { buildPackage } from "./package.mjs";

const { values } = parseArgs({ options: { binary: { type: "string" }, target: { type: "string" } } });
const target = values.target ? platforms.targets.find(({ rust }) => rust === values.target) : platforms.selectTarget();
ensure(target, "Unsupported smoke-test target");
if (!values.binary) execFileSync("cargo", ["build", "--locked", "-p", "clibox", "--target-dir", path.join(root, "target")], { cwd: root, stdio: "inherit" });
const binary = path.resolve(values.binary ?? path.join(root, "target/debug", target.binary));
const directory = mkdtempSync(path.join(tmpdir(), "clibox-package-"));
try {
  const output = path.join(directory, "packed");
  const native = buildPackage({ target, binary, output });
  const main = buildPackage({ output });
  // Repacking identical source must produce identical npm integrity, including
  // when a release is resumed after an interrupted publication.
  ensure(buildPackage({ target, binary, output }).integrity === native.integrity, "Native package is not deterministic");
  ensure(buildPackage({ output }).integrity === main.integrity, "Launcher package is not deterministic");
  for (const manager of ["npm", "pnpm"]) {
    const consumer = path.join(directory, manager);
    mkdirSync(consumer, { recursive: true });
    writeFileSync(path.join(consumer, "package.json"), JSON.stringify({ name: "clibox-smoke", private: true, scripts: { check: "clibox --version" }, dependencies: { [main.name]: `file:${path.join(output, "tarballs", main.filename)}`, [native.name]: `file:${path.join(output, "tarballs", native.filename)}` } }, null, 2));
    // Local tarballs exercise the published package boundary without registry
    // credentials or any dependency on an already-published clibox version.
    const run = (args) => manager === "npm" ? npm(args, { cwd: consumer }) : execFileSync(process.execPath, [process.env.npm_execpath, ...args], { cwd: consumer, encoding: "utf8", stdio: "pipe" });
    if (manager === "pnpm") ensure(/pnpm\.(c?js|mjs)$/u.test(process.env.npm_execpath ?? ""), "Run this smoke test through pnpm --filter @delino/clibox test:package");
    run(["install", "--offline", "--ignore-scripts", ...(manager === "npm" ? ["--no-audit", "--no-fund"] : ["--config.confirmModulesPurge=false"])]);
    ensure(run(["run", "check"]).includes(`clibox ${metadata().version}`), `${manager} script did not execute the matching binary`);
    const launcher = path.join(consumer, "node_modules/@delino/clibox/bin/clibox.cjs");
    const help = execFileSync(process.execPath, [launcher, "--help"], { cwd: consumer, encoding: "utf8" });
    ensure(help.includes("Usage: clibox"), `${manager} help smoke failed`);
    const readyFile = path.join(consumer, "ready file");
    writeFileSync(readyFile, "");
    const ready = JSON.parse(execFileSync(process.execPath, [launcher, "wait", "file", readyFile, "--json", "--timeout", "5s"], { cwd: consumer, encoding: "utf8" }));
    ensure(ready.kind === "file" && ready.status === "ready" && ready.attempts === 1 && ready.error === null, `${manager} readiness smoke failed`);
    const installed = JSON.parse(readFileSync(path.join(consumer, "node_modules", native.name, "package.json"), "utf8"));
    ensure(installed.version === metadata().version, "Installed native version mismatch");
    event("consumer_smoke", { manager, target: target.suffix, version: installed.version });
  }
} finally {
  rmSync(directory, { recursive: true, force: true });
}
