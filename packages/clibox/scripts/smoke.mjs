import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { createServer } from "node:net";
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
    const nativeTarball = `file:${path.join(output, "tarballs", native.filename)}`;
    // pnpm can resolve the launcher's exact-version optional dependency
    // separately from the direct file dependency. Pin both to this test's
    // artifact so offline validation never needs a published native tarball.
    // Remove this override only when the supported pnpm version deduplicates
    // the transitive dependency to the supplied file tarball without it.
    const consumerOverrides = manager === "pnpm" ? { pnpm: { overrides: { [native.name]: nativeTarball } } } : {};
    writeFileSync(path.join(consumer, "package.json"), JSON.stringify({ name: "clibox-smoke", private: true, scripts: { check: "clibox --version" }, dependencies: { [main.name]: `file:${path.join(output, "tarballs", main.filename)}`, [native.name]: nativeTarball }, ...consumerOverrides }, null, 2));
    // Local tarballs exercise the published package boundary without registry
    // credentials or any dependency on an already-published clibox version.
    const run = (args) => manager === "npm" ? npm(args, { cwd: consumer }) : execFileSync(process.execPath, [process.env.npm_execpath, ...args], { cwd: consumer, encoding: "utf8", stdio: "pipe" });
    if (manager === "pnpm") ensure(/pnpm\.(c?js|mjs)$/u.test(process.env.npm_execpath ?? ""), "Run this smoke test through pnpm --filter @delino/clibox test:package");
    run(["install", "--offline", "--ignore-scripts", ...(manager === "npm" ? ["--no-audit", "--no-fund"] : ["--config.confirmModulesPurge=false"])]);
    ensure(run(["run", "check"]).includes(`clibox ${metadata().version}`), `${manager} script did not execute the matching binary`);
    const launcher = path.join(consumer, "node_modules/@delino/clibox/bin/clibox.cjs");
    const help = execFileSync(process.execPath, [launcher, "--help"], { cwd: consumer, encoding: "utf8" });
    ensure(help.includes("Usage: clibox"), `${manager} help smoke failed`);
    const cli = (args, input) => execFileSync(process.execPath, [launcher, ...args], { cwd: consumer, encoding: "utf8", input });
    const available = cli(["system", "cpus"]);
    ensure(/^[1-9][0-9]*\n$/u.test(available), `${manager} available CPU smoke failed`);
    const logical = cli(["system", "cpus", "--kind", "logical"]);
    ensure(/^[1-9][0-9]*\n$/u.test(logical), `${manager} logical CPU smoke failed`);
    ensure(cli(["system", "cpus", "--quiet"]) === "", `${manager} quiet CPU smoke failed`);
    const logicalJson = cli(["system", "cpus", "--kind", "logical", "--json"]);
    const logicalResult = JSON.parse(logicalJson);
    ensure(logicalJson === `{"kind":"logical","count":${logicalResult.count}}\n` && Number.isSafeInteger(logicalResult.count) && logicalResult.count > 0, `${manager} JSON CPU smoke failed`);
    writeFileSync(path.join(consumer, ".env"), 'Z=base\nA="literal ${HOME}"\n');
    writeFileSync(path.join(consumer, "local.env"), "Z=local\n");
    ensure(cli(["dotenv", "list"]) === "A\nZ\n", `${manager} dotenv list smoke failed`);
    ensure(cli(["dotenv", "merge", ".env", "local.env"]) === 'A="literal ${HOME}"\nZ=local\n', `${manager} dotenv merge smoke failed`);
    const normalized = cli(["yaml", "normalize"], "base: &base {z: 2, a: 1}\ncopy: {<<: *base, z: 3}\n");
    ensure(normalized === '"base":\n  "a": 1\n  "z": 2\n"copy":\n  "a": 1\n  "z": 3\n', `${manager} YAML reference smoke failed`);
    ensure(cli(["yaml", "normalize"], normalized) === normalized, `${manager} YAML idempotence smoke failed`);
    writeFileSync(path.join(consumer, "config.yaml"), "z: 2\na: 1\n");
    ensure(cli(["yaml", "normalize", "--input", "config.yaml", "--in-place"]) === "", `${manager} file output leaked to stdout`);
    ensure(readFileSync(path.join(consumer, "config.yaml"), "utf8") === '"a": 1\n"z": 2\n', `${manager} in-place smoke failed`);
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
    equal(invoke(["hash", "compute", "--text", "abc"]), digest + "\n", "Installed hash generation failed");
    equal(invoke(["hash", "verify", digest, "--text", "abc"]), "text: ok\n", "Installed hash verification failed");
    const file = path.join(consumer, "binary input.dat");
    writeFileSync(file, bytes);
    const manifest = invoke(["hash", "compute", "--input", file, "--format", "checksum"]);
    equal(invoke(["hash", "verify", "--check", "-", "--quiet"], { input: manifest }), "", "Installed manifest verification failed");
    mkdirSync(path.join(consumer, "checksums"));
    equal(invoke(["hash", "compute", "--input", "binary input.dat", "--format", "checksum", "--output", "checksums/sums"]), "", "Installed manifest output failed");
    equal(invoke(["hash", "verify", "--check", "checksums/sums", "--quiet"]), "", "Installed manifest-relative path verification failed");
    equal(invoke(["base64", "encode", "--text", "abc", "--output", "-"]), "YWJj", "Installed stdout selector failed");
    equal(invoke(["dotenv", "list", "--output", "-"]), "A\nZ\n", "Installed configuration stdout selector failed");
    equal(invoke(["base64", "encode", "--text", "abc", "--output", "./-"]), "", "Installed literal dash file leaked stdout");
    equal(readFileSync(path.join(consumer, "-")), "YWJj", "Installed literal dash file failed");
    for (const args of [
      ["port", "which"], ["hash", "encode"],
      ["base64", "encode", "--force"],
      ["yaml", "normalize", "--output", "-", "--force"],
      ["port", "list", "80", "--pids", "--quiet"],
      ["port", "kill", "80", "--quiet", "--json"],
      ["system", "cpus", "--quiet", "--json"],
    ]) {
      const rejected = spawnSync(process.execPath, [launcher, ...args], { cwd: consumer, encoding: "utf8", input: "", env: { ...process.env, RUST_LOG: "off" } });
      ensure(rejected.status === 2 && rejected.stdout === "" && rejected.stderr.includes("--help"), `${manager} consistency rejection failed`);
    }
    const removedOutput = path.join(consumer, "PRIVATE-OUTPUT");
    for (const RUST_LOG of ["off", "trace"]) {
      const rejected = spawnSync(process.execPath, [launcher, "env", "run", "SECRET=PRIVATE-VALUE", "--", process.execPath, "-e", 'require("node:fs").writeFileSync(process.argv[1], "PRIVATE-ARG")', removedOutput], { cwd: consumer, encoding: "utf8", env: { ...process.env, RUST_LOG } });
      ensure(rejected.status === 2 && rejected.stdout === "" && rejected.stderr.includes("env run was renamed; use clibox run env --help."), `${manager} environment migration guidance failed`);
      ensure(!rejected.stderr.includes("PRIVATE") && !existsSync(removedOutput), `${manager} removed environment command leaked input or launched a child`);
    }
    // Keep a test-owned listener bound throughout inspection; never kill or
    // release/reacquire a port that another concurrent test could inherit.
    const listener = createServer();
    await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
    try {
      const port = String(listener.address().port);
      const pids = spawnSync(process.execPath, [launcher, "port", "list", port, "--pids"], { cwd: consumer, encoding: "utf8" });
      ensure([0, 1].includes(pids.status) && pids.stdout.trim().split(/\r?\n/u).includes(String(process.pid)), `${manager} PID output failed`);
      const quiet = spawnSync(process.execPath, [launcher, "port", "list", port, "--quiet"], { cwd: consumer, encoding: "utf8" });
      ensure([0, 1].includes(quiet.status) && quiet.stdout === "", `${manager} quiet output failed`);
    } finally {
      await new Promise((resolve) => listener.close(resolve));
    }
    for (const group of ["run", "port", "clipboard", "system", "wait", "text", "time", "base64", "hash", "dotenv", "yaml"]) {
      const missing = spawnSync(process.execPath, [launcher, group], { cwd: consumer, encoding: "utf8" });
      ensure(missing.status === 2 && missing.stdout === "" && missing.stderr.includes(`Usage: ${target.binary} ${group}`) && missing.stderr.includes("Commands:"), `${manager} ${group} missing-subcommand help smoke failed`);
    }
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
    const wrapperHome = path.join(consumer, "wrapper-state");
    const wrapperEnv = { ...process.env, HOME: wrapperHome, XDG_STATE_HOME: path.join(wrapperHome, "xdg-state") };
    // Windows coordination state is rooted in LocalAppData, not HOME/XDG_STATE_HOME.
    // Do not create persistent user-profile state from a removable package fixture.
    if (process.platform !== "win32") {
      ensure(invoke(["run", "with-rate-limit", "--name", "consumer-rate", "--limit", "1", "--period", "1m", "--", process.execPath, "-e", "process.exit(0)"], { env: wrapperEnv }).length === 0, `${manager} rate-limit wrapper smoke failed`);
      ensure(invoke(["run", "with-lock", "--name", "consumer-lock", "--", process.execPath, "-e", "process.exit(0)"], { env: wrapperEnv }).length === 0, `${manager} lock wrapper smoke failed`);
    }
    ensure(invoke(["run", "with-retry", "--max-attempts", "1", "--", process.execPath, "-e", "process.exit(0)"], { env: wrapperEnv }).length === 0, `${manager} retry wrapper smoke failed`);
    ensure(invoke(["run", "with-timeout", "--timeout", "5s", "--", process.execPath, "-e", "process.exit(0)"], { env: wrapperEnv }).length === 0, `${manager} timeout wrapper smoke failed`);
    const serviceSyntax = spawnSync(process.execPath, [launcher, "run", "with-service", "http://127.0.0.1:9", "--ready-timeout", "1ms", "--", process.execPath, "-e", "process.exit(0)"], { cwd: consumer, env: wrapperEnv, encoding: "utf8" });
    ensure(serviceSyntax.status !== 2, `${manager} service wrapper parsing failed`);
    const installed = JSON.parse(readFileSync(path.join(consumer, "node_modules", native.name, "package.json"), "utf8"));
    ensure(installed.version === metadata().version, "Installed native version mismatch");
    event("consumer_smoke", { manager, target: target.suffix, version: installed.version });
  }
} finally {
  rmSync(directory, { recursive: true, force: true });
}
