// End-to-end acceptance for bundlewhy fixtures.
//
// Builds the UI and the Rust binary, analyzes each fixture with
// `bundlewhy analyze --no-serve`, verifies expected rules, verifies that no
// fixture file changed, exports HTML and records duration and peak memory.
// Usage: node scripts/acceptance.mjs [--skip-build] [--project <dir> --bundler <name>]

import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const repoRoot = path.resolve(packageRoot, "../..");
const args = process.argv.slice(2);
const option = (name) => {
  const index = args.indexOf(name);
  return index === -1 ? null : args[index + 1];
};

const EXPECTED = {
  vite: [
    "vite/side-effect-retained",
    "vite/commonjs-wrapped",
    "vite/ineffective-dynamic-import",
    "bundle/duplicate-package",
    "minifier/unresolved-env-condition",
    "minifier/direct-eval-blocks-mangling",
  ],
  rspack: [
    "rspack/side-effect-retained",
    "rspack/module-concatenation-bailout",
    "rspack/module-in-multiple-chunks",
    "bundle/duplicate-package",
    "minifier/unresolved-env-condition",
    "minifier/direct-eval-blocks-mangling",
  ],
};

function run(command, commandArgs, options = {}) {
  const shell =
    options.shell ??
    (process.platform === "win32" &&
      path.extname(command).toLowerCase() === ".cmd");
  const result = spawnSync(command, commandArgs, {
    cwd: repoRoot,
    encoding: "utf8",
    shell,
    ...options,
  });
  if (result.status !== 0 && !options.allowFailure) {
    process.stderr.write(result.stdout ?? "");
    process.stderr.write(result.stderr ?? "");
    throw new Error(
      `${command} ${commandArgs.join(" ")} exited with ${result.status}`,
    );
  }
  return result;
}

// Tests provide command paths for platform-specific mock shims. Production
// runs keep using the host's pnpm and cargo commands.
function commandPath(name) {
  const mockBin = process.env.BUNDLEWHY_ACCEPTANCE_MOCK_BIN;
  return mockBin
    ? path.join(mockBin, process.platform === "win32" ? `${name}.cmd` : name)
    : name;
}

function treeDigest(dir) {
  const hash = crypto.createHash("sha256");
  const walk = (current) => {
    for (const entry of fs
      .readdirSync(current, { withFileTypes: true })
      .sort((a, b) => a.name.localeCompare(b.name))) {
      if (entry.name === "node_modules") continue;
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else hash.update(path.relative(dir, full)).update(fs.readFileSync(full));
    }
  };
  walk(dir);
  return hash.digest("hex");
}

const binary =
  process.env.BUNDLEWHY_ACCEPTANCE_MOCK_BINARY ??
  path.join(
    repoRoot,
    "target/debug",
    process.platform === "win32" ? "bundlewhy.exe" : "bundlewhy",
  );
let outDir;
let failed = false;
let abortError;
try {
  if (!args.includes("--skip-build")) {
    run(commandPath("pnpm"), ["--filter", "bundlewhy-web", "build"], {
      shell: process.platform === "win32",
    });
    run(commandPath("cargo"), ["build", "-p", "bundlewhy"]);
  }

  const targets = option("--project")
    ? [
        {
          bundler: option("--bundler"),
          project: path.resolve(option("--project")),
          expected: [],
        },
      ]
    : [
        {
          bundler: "vite",
          project: path.join(packageRoot, "fixtures/vite-app"),
          expected: EXPECTED.vite,
        },
        {
          bundler: "rspack",
          project: path.join(packageRoot, "fixtures/rspack-app"),
          expected: EXPECTED.rspack,
        },
      ];

  outDir = fs.mkdtempSync(path.join(os.tmpdir(), "bundlewhy-acceptance-"));
  const records = [];
  for (const target of targets) {
    const before = treeDigest(target.project);
    const started = Date.now();
    const timeTool =
      process.platform === "darwin"
        ? ["/usr/bin/time", ["-l"]]
        : process.platform === "linux"
          ? ["/usr/bin/time", ["-v"]]
          : null;
    const commandArgs = [
      "--no-color",
      "analyze",
      "--project",
      target.project,
      "--out-dir",
      outDir,
      "--no-serve",
      ...(target.bundler ? ["--bundler", target.bundler] : []),
    ];
    const result =
      timeTool && fs.existsSync(timeTool[0])
        ? run(timeTool[0], [...timeTool[1], binary, ...commandArgs], {
            allowFailure: true,
          })
        : run(binary, commandArgs, { allowFailure: true });
    const durationMs = Date.now() - started;
    const memory =
      /(\d+)\s+maximum resident set size/.exec(result.stderr) ??
      /Maximum resident set size \(kbytes\): (\d+)/.exec(result.stderr);
    const peakBytes = memory
      ? Number(memory[1]) * (process.platform === "darwin" ? 1 : 1024)
      : null;
    const runId = /bundlewhy run ([0-9a-f-]{36})/.exec(result.stderr)?.[1];
    const after = treeDigest(target.project);
    const problems = [];
    if (result.status !== 0) problems.push(`exit ${result.status}`);
    if (before !== after) problems.push("project files changed");
    let analysis = null;
    if (runId) {
      analysis = JSON.parse(
        fs.readFileSync(path.join(outDir, runId, "analysis.json"), "utf8"),
      ).analysis;
      if (analysis.status !== "RUN_STATUS_COMPLETE")
        problems.push(`status ${analysis.status}`);
      const rules = new Set(
        analysis.diagnostics.map((diagnostic) => diagnostic.rule),
      );
      for (const rule of target.expected)
        if (!rules.has(rule)) problems.push(`missing ${rule}`);
      const report = path.join(outDir, `${runId}.html`);
      run(binary, [
        "--no-color",
        "export",
        path.join(outDir, runId),
        "--output",
        report,
      ]);
      const html = fs.readFileSync(report, "utf8");
      if (
        !html.includes('id="bundlewhy-report"') ||
        /<(script|link)[^>]+(src|href)=["']https?:/i.test(html)
      )
        problems.push("export is not self-contained");
    } else {
      problems.push("no run id");
    }
    records.push({
      bundler: target.bundler,
      project: path.relative(repoRoot, target.project),
      durationMs,
      peakMemoryBytes: peakBytes,
      diagnostics: analysis?.diagnostics.length ?? null,
      status: analysis?.status ?? null,
      problems,
    });
    failed ||= problems.length > 0;
  }

  process.stdout.write(
    `${JSON.stringify({ platform: `${process.platform}-${process.arch}`, node: process.version, records }, null, 2)}\n`,
  );
} catch (error) {
  abortError = error;
} finally {
  // A cleanup failure must not hide the original acceptance failure.
  for (const directory of [
    outDir,
    path.join(repoRoot, "apps/bundlewhy-web/dist"),
  ]) {
    if (!directory) continue;
    try {
      fs.rmSync(directory, { recursive: true, force: true });
    } catch (error) {
      if (abortError)
        process.stderr.write(`Cleanup failed for ${directory}: ${error}\n`);
      else abortError = error;
    }
  }
}
if (abortError) throw abortError;
process.exitCode = failed ? 1 : 0;
