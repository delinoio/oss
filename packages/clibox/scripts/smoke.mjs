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
// Exercise the release artifact shape when no prebuilt target is supplied.
// Debug symbols can exceed the package reader's bounded 64 MiB archive limit;
// the release profile strips them and matches the native release workflow.
if (!values.binary) execFileSync("cargo", ["build", "--locked", "--release", "-p", "clibox", "--target-dir", path.join(root, "target")], { cwd: root, stdio: "inherit" });
const binary = path.resolve(values.binary ?? path.join(root, "target/release", target.binary));
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
    const invoke = (args, options = {}) => execFileSync(process.execPath, [launcher, ...args], { cwd: consumer, ...options });
    const equal = (actual, expected, operation) => ensure(Buffer.from(actual).equals(Buffer.from(expected)), operation);
    const consumerManifest = path.join(consumer, "package.json");
    const scriptPackage = JSON.parse(readFileSync(consumerManifest, "utf8"));
    const capture = process.platform === "win32" ? "$1 world" : "\\$1 world";
    scriptPackage.scripts.rewrite = 'clibox text replace "(hello)" "' + capture + '" --regex --input "path with spaces.txt" --in-place';
    writeFileSync(consumerManifest, JSON.stringify(scriptPackage));
    writeFileSync(path.join(consumer, "path with spaces.txt"), "hello");
    run(["run", "rewrite"]);
    equal(readFileSync(path.join(consumer, "path with spaces.txt")), "hello world", "npm-script capture/path quoting failed");
    equal(invoke(["text", "replace", "(hello)", "$1 world", "--regex", "--text", "hello"]), "hello world", "Installed text replacement failed");
    equal(invoke(["time", "format", "-1", "--from", "unix-ms", "--to", "unix-s"]), "-1\n", "Installed time formatting failed");
    equal(invoke(["time", "add", "2024-03-09T12:00:00-05:00", "--timezone", "America/New_York", "--days", "1"]), "2024-03-10T12:00:00-04:00\n", "Installed calendar arithmetic failed");
    const bytes = Buffer.from([0, 255, 13, 10, 127, 128]);
    const encoded = invoke(["base64", "encode"], { input: bytes });
    equal(encoded, bytes.toString("base64"), "Installed Base64 encoding failed");
    equal(invoke(["base64", "decode"], { input: encoded }), bytes, "Installed binary Base64 decoding failed");
    const digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";
    equal(invoke(["hash", "encode", "--text", "abc"]), digest + "\n", "Installed hash generation failed");
    equal(invoke(["hash", "verify", digest, "--text", "abc"]), "text: ok\n", "Installed hash verification failed");
    const file = path.join(consumer, "binary input.dat");
    writeFileSync(file, bytes);
    const manifest = invoke(["hash", "encode", "--input", file, "--format", "checksum"]);
    equal(invoke(["hash", "verify", "--check", "-", "--quiet"], { input: manifest }), "", "Installed manifest verification failed");
    mkdirSync(path.join(consumer, "checksums"));
    equal(invoke(["hash", "encode", "--input", "binary input.dat", "--format", "checksum", "--output", "checksums/sums"]), "", "Installed manifest output failed");
    equal(invoke(["hash", "verify", "--check", "checksums/sums", "--quiet"]), "", "Installed manifest-relative path verification failed");
    const readyFile = path.join(consumer, "ready file");
    writeFileSync(readyFile, "");
    const ready = JSON.parse(execFileSync(process.execPath, [launcher, "wait", "file", readyFile, "--json", "--timeout", "5s"], { cwd: consumer, encoding: "utf8" }));
    ensure(ready.kind === "file" && ready.status === "ready" && ready.attempts === 1 && ready.error === null, `${manager} readiness smoke failed`);
    const fixture = path.join(consumer, "utility-check.cjs");
    writeFileSync(fixture, "if (process.env.CLIBOX_TEST_EXIT) process.exit(37); process.stdout.write(JSON.stringify({ value: process.env.CLIBOX_TEST_VALUE, args: process.argv.slice(2) }));");
    const utility = JSON.parse(execFileSync(process.execPath, [launcher, "run", "env", "CLIBOX_TEST_VALUE=unicode 🦀", "--", process.execPath, fixture, "", "two words", "a&b|c"], { cwd: consumer, encoding: "utf8" }));
    ensure(utility.value === "unicode 🦀" && JSON.stringify(utility.args) === JSON.stringify(["", "two words", "a&b|c"]), `${manager} utility argv/environment smoke failed`);
    let delegatedStatus;
    try {
      execFileSync(process.execPath, [launcher, "run", "env", "CLIBOX_TEST_EXIT=1", "--", process.execPath, fixture], { cwd: consumer, stdio: "pipe" });
    } catch (error) { delegatedStatus = error.status; }
    ensure(delegatedStatus === 37, `${manager} utility exit propagation failed`);
    const installed = JSON.parse(readFileSync(path.join(consumer, "node_modules", native.name, "package.json"), "utf8"));
    ensure(installed.version === metadata().version, "Installed native version mismatch");
    event("consumer_smoke", { manager, target: target.suffix, version: installed.version });
  }
} finally {
  rmSync(directory, { recursive: true, force: true });
}
