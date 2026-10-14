import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const script = fileURLToPath(new URL("./acceptance.mjs", import.meta.url));
const runId = "01234567-89ab-7cde-8f01-23456789abcd";

function executable(file, source) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (process.platform === "win32") {
    const script = `${file}.cjs`;
    fs.writeFileSync(script, source);
    const shim = `${file}.cmd`;
    fs.writeFileSync(
      shim,
      `@echo off\r\n"${process.execPath}" "${script}" %*\r\n`,
    );
    return shim;
  }
  fs.writeFileSync(file, `#!/usr/bin/env node\n${source}`, { mode: 0o755 });
  return file;
}

function runCase(mode) {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "bundlewhy-acceptance-test-"),
  );
  try {
    const packageScript = path.join(
      root,
      "packages/bundlewhy/scripts/acceptance.mjs",
    );
    const project = path.join(root, "project");
    const web = path.join(root, "apps/bundlewhy-web");
    const bin = path.join(root, "mock-bin");
    fs.mkdirSync(path.dirname(packageScript), { recursive: true });
    fs.copyFileSync(script, packageScript);
    fs.mkdirSync(project);
    fs.writeFileSync(path.join(project, "package.json"), "{}\n");
    fs.mkdirSync(web, { recursive: true });
    fs.writeFileSync(path.join(web, "keep.txt"), "unrelated output\n");

    executable(
      path.join(bin, "pnpm"),
      `const fs = require("node:fs");
fs.mkdirSync("apps/bundlewhy-web/dist", { recursive: true });
fs.writeFileSync("apps/bundlewhy-web/dist/index.html", "generated");
if (process.env.BW_ACCEPTANCE_TEST_MODE === "build-fail") {
  console.error("deliberate build failure");
  process.exitCode = 17;
}`,
    );
    executable(path.join(bin, "cargo"), "process.exitCode = 0;");
    const binary = executable(
      path.join(root, "target/debug/bundlewhy"),
      `const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
const mode = process.env.BW_ACCEPTANCE_TEST_MODE;
if (args.includes("analyze")) {
  const outDir = args[args.indexOf("--out-dir") + 1];
  const runDir = path.join(outDir, "${runId}");
  fs.mkdirSync(runDir, { recursive: true });
  if (mode !== "read-fail") {
    const result = mode === "parse-fail"
      ? "{invalid json"
      : JSON.stringify({ analysis: {
          status: mode === "analysis-fail" ? "RUN_STATUS_FAILED" : "RUN_STATUS_COMPLETE",
          diagnostics: [],
        } });
    fs.writeFileSync(path.join(runDir, "analysis.json"), result);
  }
  console.error("bundlewhy run ${runId}");
  if (mode === "analysis-fail") process.exitCode = 1;
} else if (args.includes("export")) {
  if (mode === "export-fail") {
    console.error("deliberate export failure");
    process.exitCode = 19;
  } else {
    const output = args[args.indexOf("--output") + 1];
    fs.writeFileSync(output, '<script id="bundlewhy-report"></script>');
  }
}`,
    );

    const result = spawnSync(
      process.execPath,
      [packageScript, "--project", project, "--bundler", "vite"],
      {
        cwd: root,
        encoding: "utf8",
        env: {
          ...process.env,
          BW_ACCEPTANCE_TEST_MODE: mode,
          BUNDLEWHY_ACCEPTANCE_MOCK_BIN: bin,
          BUNDLEWHY_ACCEPTANCE_MOCK_BINARY: binary,
          PATH: `${bin}${path.delimiter}${process.env.PATH ?? ""}`,
        },
      },
    );
    assert.equal(fs.existsSync(path.join(web, "dist")), false);
    assert.equal(
      fs.readFileSync(path.join(web, "keep.txt"), "utf8"),
      "unrelated output\n",
    );
    return result;
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

test("cleans generated web output after successful acceptance", () => {
  const result = runCase("success");
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout).records[0].problems, []);
});

for (const [mode, diagnostic] of [
  ["build-fail", "deliberate build failure"],
  ["export-fail", "deliberate export failure"],
  ["read-fail", "ENOENT"],
  ["parse-fail", "SyntaxError"],
]) {
  test(`cleans generated web output after ${mode}`, () => {
    const result = runCase(mode);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(diagnostic));
  });
}

test("cleans generated web output after accumulated analysis problems", () => {
  const result = runCase("analysis-fail");
  assert.equal(result.status, 1, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout).records[0].problems, [
    "exit 1",
    "status RUN_STATUS_FAILED",
  ]);
});
