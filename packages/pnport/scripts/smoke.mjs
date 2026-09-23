import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import { createRequire } from "node:module";
import { ensure, event, metadata, npm, revision, root } from "./common.mjs";
import { buildPackage } from "./package.mjs";
import { companion, nativeManifest } from "./manifests.mjs";

const require = createRequire(import.meta.url);
const { targets, selectTarget } = require("../src/platforms.cjs");
const { values } = parseArgs({ options: { binary: { type: "string" }, preload: { type: "string" }, target: { type: "string" } } });
const target = values.target ? targets.find(({ rust }) => rust === values.target) : selectTarget();
ensure(target, "Unsupported pnport smoke target");
if (!values.binary || !values.preload) execFileSync("cargo", target.os === "darwin" ? ["build", "--locked", "--release", "-p", "pnport", "-p", "fspy_preload_unix", "--features", "fspy_preload_unix/pnport"] : ["build", "--locked", "--release", "-p", "pnport", "-p", "pnport-preload"], { cwd: root, stdio: "inherit" });
const binary = path.resolve(root, values.binary ?? path.join("target/release", target.binary));
const library = path.resolve(root, values.preload ?? path.join("target/release", target.os === "darwin" ? "libfspy_preload_unix.dylib" : target.os === "win32" ? "pnport_preload.dll" : "libpnport_preload.so"));
const temporary = mkdtempSync(path.join(tmpdir(), "pnport-package-"));
try {
  const output = path.join(temporary, "packed");
  const native = buildPackage({ target, binary, preload: library, output });
  const launcher = buildPackage({ output });
  ensure(buildPackage({ target, binary, preload: library, output }).integrity === native.integrity, "Native npm package is not reproducible");
  ensure(buildPackage({ output }).integrity === launcher.integrity, "Launcher npm package is not reproducible");
  const resolutions = { [native.name]: `file:${path.join(output, "tarballs", native.filename)}` };
  // Yarn resolves every optional package before it filters incompatible hosts.
  // Local inert fixtures fill those five slots while the selected package is
  // always the real two-file artifact under test; no fixture is published.
  for (const other of targets.filter((candidate) => candidate !== target)) {
    const stub = path.join(temporary, "stubs", other.suffix);
    mkdirSync(path.join(stub, "bin"), { recursive: true });
    writeFileSync(path.join(stub, "package.json"), JSON.stringify(nativeManifest(other.suffix, metadata().version, revision())) + "\n");
    writeFileSync(path.join(stub, "bin", other.binary), "inert test fixture\n");
    writeFileSync(path.join(stub, "bin", companion(other)), "inert test fixture\n");
    resolutions[other.name] = `file:${stub}`;
  }
  for (const manager of ["npm", "yarn"]) {
    const consumer = path.join(temporary, manager);
    mkdirSync(consumer, { recursive: true });
    const localNative = `file:${path.join(output, "tarballs", native.filename)}`;
    const localLauncher = `file:${path.join(output, "tarballs", launcher.filename)}`;
    const manifest = { name: "pnport-smoke", private: true, packageManager: "yarn@4.18.0", dependencies: { [launcher.name]: localLauncher, [native.name]: localNative }, resolutions };
    writeFileSync(path.join(consumer, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
    const installEnv = { ...process.env, YARN_ENABLE_SCRIPTS: "0", npm_config_ignore_scripts: "true" };
    if (manager === "yarn") {
      writeFileSync(path.join(consumer, ".yarnrc.yml"), "nodeLinker: pnp\nenableScripts: false\n");
      npm(["exec", "--yes", "--package", "@yarnpkg/cli-dist@4.18.0", "--", "yarn", "install"], { cwd: consumer, env: installEnv });
      ensure(existsSync(path.join(consumer, ".pnp.cjs")) && !existsSync(path.join(consumer, "node_modules")), "Yarn PnP consumer generated physical node_modules");
      // Yarn's PnP loader resolves the launcher and its unplugged native binary.
      const result = npm(["exec", "--yes", "--package", "@yarnpkg/cli-dist@4.18.0", "--", "yarn", "pnport", "--version"], { cwd: consumer, env: { ...installEnv, YARN_ENABLE_NETWORK: "0", npm_config_offline: "true" } });
      ensure(result.includes(`pnport ${metadata().version}`), "Installed Yarn PnP launcher failed");
    } else {
      npm(["install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund"], { cwd: consumer, env: installEnv });
      const command = path.join(consumer, "node_modules", "@delino", "pnport", "bin", "pnport.cjs");
      const result = spawnSync(process.execPath, [command, "--version"], { cwd: consumer, env: { ...installEnv, npm_config_offline: "true" }, encoding: "utf8" });
      ensure(result.status === 0 && result.stdout.trim() === `pnport ${metadata().version}`, "Installed npm launcher failed");
    }
    event("consumer_smoke", { manager, target: target.suffix, version: metadata().version });
  }
} finally { rmSync(temporary, { recursive: true, force: true }); }
