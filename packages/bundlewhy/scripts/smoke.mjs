import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { createRequire } from "node:module";
import path from "node:path";
import { parseArgs } from "node:util";
import { buildPackage } from "./package.mjs";
import {
  ensure,
  event,
  metadata,
  npm,
  packageRoot,
  targets,
} from "./common.mjs";

// Compare fixture source/configuration bytes before and after the installed CLI
// runs. Build output must stay in the disposable consumer's run directory.
function treeDigest(directory) {
  const hash = createHash("sha256");
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true }).sort(
      (a, b) => a.name.localeCompare(b.name),
    )) {
      if (entry.name === "node_modules") continue;
      const file = path.join(current, entry.name);
      if (entry.isDirectory()) walk(file);
      else
        hash.update(path.relative(directory, file)).update(readFileSync(file));
    }
  };
  walk(directory);
  return hash.digest("hex");
}

const { values } = parseArgs({
  options: { binary: { type: "string" }, target: { type: "string" } },
});
const target = targets.find((item) => item.rust === values.target);
ensure(
  target && values.binary,
  "Supply the native release --target and --binary",
);
const directory = mkdtempSync(
  path.join(tmpdir(), "bundlewhy installed consumer "),
);
const env = { ...process.env };
delete env.BUNDLEWHY_BINARY;
try {
  const output = path.join(directory, "packed");
  const native = buildPackage({ target, binary: values.binary, output });
  const main = buildPackage({ output });
  ensure(
    buildPackage({ target, binary: values.binary, output }).integrity ===
      native.integrity,
    "Native packaging is not deterministic",
  );
  ensure(
    buildPackage({ output }).integrity === main.integrity,
    "Launcher packaging is not deterministic",
  );
  for (const manager of ["npm", "pnpm"]) {
    const consumer = path.join(directory, manager);
    mkdirSync(consumer);
    const nativeTarball = `file:${path.join(output, "tarballs", native.filename)}`;
    writeFileSync(
      path.join(consumer, "package.json"),
      JSON.stringify(
        {
          name: "bundlewhy-smoke",
          private: true,
          scripts: { check: "bundlewhy --version" },
          dependencies: {
            [main.name]: `file:${path.join(output, "tarballs", main.filename)}`,
            [native.name]: nativeTarball,
          },
          // pnpm resolves exact optional dependencies separately; pin this candidate
          // so the offline install cannot select a registry version instead.
          ...(manager === "pnpm"
            ? { pnpm: { overrides: { [native.name]: nativeTarball } } }
            : {}),
        },
        null,
        2,
      ),
    );
    const run = (args) =>
      manager === "npm"
        ? npm(args, { cwd: consumer, env })
        : execFileSync(process.execPath, [process.env.npm_execpath, ...args], {
            cwd: consumer,
            encoding: "utf8",
            env,
          });
    if (manager === "pnpm")
      ensure(
        /pnpm\.(c?js|mjs)$/u.test(process.env.npm_execpath ?? ""),
        "Run through pnpm --filter @delino/bundlewhy test:package",
      );
    run([
      "install",
      "--offline",
      "--ignore-scripts",
      ...(manager === "npm"
        ? ["--no-audit", "--no-fund"]
        : ["--config.confirmModulesPurge=false"]),
    ]);
    ensure(
      run(["run", "check"]).includes(`bundlewhy ${metadata().version}`),
      "Installed executable version mismatch",
    );
    const launcher = path.join(
      consumer,
      "node_modules/@delino/bundlewhy/bin/bundlewhy.cjs",
    );
    const cli = (args) =>
      execFileSync(process.execPath, [launcher, ...args], {
        cwd: consumer,
        env,
        encoding: "utf8",
        timeout: 120000,
      });
    ensure(cli(["--help"]).includes("analyze"), "Installed help is missing");
    ensure(
      cli(["engines"]).includes("8.3.4"),
      "Installed compatibility matrix is missing",
    );
    const invalid = spawnSync(process.execPath, [launcher, "analyze"], {
      cwd: consumer,
      env,
      encoding: "utf8",
    });
    ensure(invalid.status === 2, "Launcher did not preserve usage exit code");
    for (const bundler of ["vite", "rspack"]) {
      const project = path.join(packageRoot, "fixtures", `${bundler}-app`);
      const before = treeDigest(project);
      const runs = path.join(consumer, `${bundler} runs`);
      cli(["analyze", "--project", project, "--out-dir", runs, "--no-serve"]);
      const saved = path.join(
        runs,
        readdirSync(runs).find((name) => /^[a-f0-9-]{36}$/u.test(name)) ??
          "missing",
      );
      const { analysis } = JSON.parse(
        readFileSync(path.join(saved, "analysis.json"), "utf8"),
      );
      ensure(
        analysis.status === "RUN_STATUS_COMPLETE" &&
          analysis.diagnostics.length > 0,
        "Installed fixture analysis is incomplete or empty",
      );
      ensure(
        treeDigest(project) === before,
        "Installed analysis changed fixture source/configuration",
      );
      const report = path.join(consumer, `${bundler} report.html`);
      cli(["export", saved, "--output", report]);
      const html = readFileSync(report, "utf8");
      ensure(
        html.includes('id="bundlewhy-report"') &&
          html.includes("<script") &&
          !html.includes("bundlewhy UI is not built"),
        "Release binary does not embed the interactive UI",
      );
    }
    const installedNative = path.dirname(
      createRequire(launcher).resolve(`${native.name}/package.json`),
    );
    ensure(
      realpathSync(installedNative).startsWith(
        realpathSync(consumer) + path.sep,
      ),
      "Native dependency must belong to the disposable consumer",
    );
    rmSync(installedNative, { recursive: true, force: true });
    const missing = spawnSync(process.execPath, [launcher, "--version"], {
      cwd: consumer,
      env,
      encoding: "utf8",
    });
    ensure(
      missing.status === 1 &&
        missing.stderr.includes("BW_PLATFORM_PACKAGE_MISSING"),
      "Missing native dependency must fail with reinstall guidance",
    );
    event("consumer_smoke", {
      manager,
      target: target.rust,
      version: metadata().version,
    });
  }
} finally {
  rmSync(directory, { recursive: true, force: true });
}
