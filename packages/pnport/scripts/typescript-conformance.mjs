// Download/install preparation is deliberately separate from offline execution.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, delimiter, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { performance } from "node:perf_hooks";
import { createHash } from "node:crypto";
import { version as osVersion } from "node:os";
import { npm } from "./common.mjs";

const repository = fileURLToPath(new URL("../../../", import.meta.url));
const fixture = fileURLToPath(new URL("../test/fixtures/typescript/", import.meta.url));
const [mode, destination, suppliedBinary] = process.argv.slice(2);
assert(["prepare", "run"].includes(mode) && destination, "Usage: typescript-conformance.mjs prepare|run <temporary-directory> [pnport-binary]");
const directory = resolve(destination);
const formats = ["inline", "split"];
const yarn = "4.18.0";
const compiler = "7.1.0-dev.20260812.1";
const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}`, YARN_ENABLE_TELEMETRY: "0", YARN_ENABLE_SCRIPTS: "0" };
delete env.NODE_OPTIONS;
function execute(program, args, cwd, overrides = {}) {
  const result = spawnSync(program, args, { cwd, env, encoding: "utf8", timeout: 120_000, maxBuffer: 8 * 1024 * 1024, ...overrides });
  assert.ifError(result.error);
  return result;
}
function successful(result) {
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  return result;
}
function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}
if (mode === "prepare") {
  assert(!existsSync(directory), "Preparation requires a new directory to protect existing projects.");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  for (const format of formats) {
    const root = join(directory, format);
    cpSync(fixture, root, { recursive: true, filter: (path) => !path.split(/[\\/]/).some((part) => [".yarn", "lib"].includes(part)) && !/\.pnp\.|\.tsbuildinfo$/.test(path) });
    writeFileSync(join(root, ".yarnrc.yml"), `nodeLinker: pnp\nenableScripts: false\nenableGlobalCache: false\npnpEnableInlining: ${format === "inline"}\ncacheFolder: ${JSON.stringify(join(directory, "external-cache"))}\n`);
    npm(["exec", "--yes", "--package", `@yarnpkg/cli-dist@${yarn}`, "--", "yarn", "install", "--immutable"], { cwd: root, env });
    assert(!existsSync(join(root, "node_modules")));
  }
  writeFileSync(join(directory, "prepared.json"), JSON.stringify({ yarn, compiler, platform: process.platform, arch: process.arch }, null, 2));
  console.log(JSON.stringify({ event: "pnport_conformance_prepared", yarn, compiler, directory }));
} else {
  const prepared = JSON.parse(readFileSync(join(directory, "prepared.json"), "utf8"));
  assert.deepEqual(prepared, { yarn, compiler, platform: process.platform, arch: process.arch });
  assert(["darwin", "win32", "linux"].includes(process.platform) && ["x64", "arm64"].includes(process.arch), "Unsupported native conformance host.");
  const binary = realpathSync(suppliedBinary ? resolve(repository, suppliedBinary) : join(repository, "target/debug/pnport"));
  const samples = [];
  for (const format of formats) {
    const root = join(directory, format);
    const source = join(root, "packages/app/src/index.ts");
    const original = readFileSync(source, "utf8");
    const cache = join(directory, `pnport-cache-${format}`);
    const run = (...args) => execute(binary, ["--cache-dir", cache, "run", "--", ...args], root);
    const unplugged = join(root, ".yarn/unplugged");
    const platform = `${process.platform}-${process.arch}`;
    const platformPackage = readdirSync(unplugged).find((name) => name.startsWith(`@typescript-typescript-${platform}-`));
    assert(platformPackage);
    const native = join(unplugged, platformPackage, "node_modules/@typescript", `typescript-${platform}`, process.platform === "win32" ? "lib/tsc.exe" : "lib/tsc");
    assert(existsSync(native), "The official native TypeScript compiler is missing.");
    const originalDigest = sha256(native);
    if (process.platform === "darwin") {
      successful(execute("/usr/bin/codesign", ["--verify", "--strict", native], root));
      const signature = successful(execute("/usr/bin/codesign", ["-d", "--entitlements", ":-", native], root));
      for (const entitlement of ["allow-dyld-environment-variables", "disable-library-validation"]) {
        assert.match(signature.stdout + signature.stderr, new RegExp(`<key>com\\.apple\\.security\\.cs\\.${entitlement}</key>\\s*<true\\s*/>`));
      }
    } else if (process.platform === "linux") {
      const image = readFileSync(native);
      assert.equal(image.subarray(0, 4).toString("hex"), "7f454c46");
      assert.equal(image[4], 2, "The compiler must be a 64-bit ELF executable.");
      assert.equal(image.readUInt16LE(18), process.arch === "arm64" ? 183 : 62);
      const offset = Number(image.readBigUInt64LE(32));
      const stride = image.readUInt16LE(54);
      const count = image.readUInt16LE(56);
      assert(count > 0 && stride >= 56 && offset + count * stride <= image.length);
      for (let index = 0; index < count; index++) {
        assert.notEqual(image.readUInt32LE(offset + index * stride), 3,
          "Linux conformance must exercise the static syscall backend.");
      }
    }
    for (const workspace of ["core", "app"]) {
      rmSync(join(root, "packages", workspace, "lib"), { recursive: true, force: true });
      rmSync(join(root, "packages", workspace, "tsconfig.tsbuildinfo"), { force: true });
    }
    const start = performance.now();
    successful(run("tsc", "-b"));
    const coldMs = performance.now() - start;
    const output = join(root, "packages/app/lib/index.js");
    assert(existsSync(output) && existsSync(join(root, "packages/app/lib/index.d.ts")));
    const mtime = statSync(output).mtimeMs;
    const warm = performance.now();
    successful(run("tsc", "-b"));
    const warmMs = performance.now() - warm;
    assert.equal(statSync(output).mtimeMs, mtime, "An unchanged incremental build must preserve output.");
    // Run the negative control after building the project references, so a
    // missing declaration output cannot masquerade as a PnP resolution failure.
    const unsupported = execute(native, ["--noEmit", "-p", "packages/app"], root);
    assert.equal(unsupported.status, 1);
    assert.match(unsupported.stdout + unsupported.stderr, /TS2688: Cannot find type definition file for 'node'/);
    successful(run("tsc", "--noEmit", "-p", "packages/app"));
    successful(run(native, "--noEmit", "-p", "packages/app"));
    writeFileSync(source, `${original}\nexport const invalid: number = "type-error-canary";\n`);
    try {
      const failure = run("tsc", "--noEmit", "-p", "packages/app");
      assert.equal(failure.status, 1);
      assert.match(failure.stdout + failure.stderr, /TS2322/);
    } finally { writeFileSync(source, original); }
    successful(run("tsc", "--noEmit", "-p", "packages/app"));
    assert(!existsSync(join(root, "node_modules")));
    assert.equal(sha256(native), originalDigest, "Never rewrite or re-sign the official compiler.");
    successful(execute(binary, ["--cache-dir", cache, "cache", "clean"], root));
    samples.push({ format, coldMs, warmMs, nativeSha256: originalDigest });
  }
  const hostVersion = process.platform === "darwin"
    ? successful(execute("/usr/bin/sw_vers", ["-productVersion"], directory)).stdout.trim()
    : process.platform === "linux"
      ? readFileSync("/etc/os-release", "utf8").match(/^PRETTY_NAME="?([^"\n]+)"?/m)?.[1]
      : osVersion();
  const evidence = { event: "pnport_typescript_conformance", compiler, yarn, platform: process.platform, arch: process.arch, osVersion: hostVersion, pnportSha256: sha256(binary), samples };
  writeFileSync(join(directory, "typescript-evidence.json"), JSON.stringify(evidence, null, 2));
  console.log(JSON.stringify(evidence));
}
