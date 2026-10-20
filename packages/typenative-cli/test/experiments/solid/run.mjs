// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { copyFile, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Dependencies are explicitly prepared outside the workspace; this runner never installs tools.
const dependencyRoot = resolve(process.argv[2] ?? "");
if (process.argv.length !== 3)
  throw new Error("Usage: node run.mjs <dependency-directory>");
if (process.versions.node.split(".")[0] !== "24")
  throw new Error("Use Node 24 for this experiment.");
if (process.platform !== "darwin" || process.arch !== "arm64") {
  throw new Error("This experiment currently checks macOS arm64 only.");
}
const require = createRequire(join(dependencyRoot, "package.json"));
for (const [name, version] of Object.entries({
  scriptc: "0.3.5",
  "solid-js": "1.9.17",
  esbuild: "0.28.2",
  typescript: "5.9.3",
})) {
  if (require(`${name}/package.json`).version !== version)
    throw new Error(`Select ${name}@${version}`);
}
const compiler = join(dependencyRoot, "node_modules/scriptc/bin/scriptc.exe");
const results = [];
const directory = await mkdtemp(join(dependencyRoot, "probe-"));
const fixtures = dirname(fileURLToPath(import.meta.url));
function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: directory,
    encoding: "utf8",
    timeout: 120_000,
    maxBuffer: 8 * 1024 * 1024,
  });
  return {
    status: result.status,
    signal: result.signal,
    error: result.error?.code ?? null,
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
  };
}
async function native(name, source, expected, extra = []) {
  const artifact = join(directory, name);
  const compiled = run(compiler, [
    "build",
    source,
    ...extra,
    "--print",
    "diagnostics",
    "--no-keep-llvm",
    "-o",
    artifact,
  ]);
  const diagnostics = JSON.parse(compiled.stdout);
  if (
    diagnostics.schema !== "scriptc-diagnostics" ||
    diagnostics.compilerVersion !== "0.3.5"
  )
    throw new Error("Unexpected compiler diagnostics");
  let executed = null;
  let arm64MachO = false;
  if (
    compiled.status === 0 &&
    !compiled.error &&
    diagnostics.success === true &&
    diagnostics.diagnostics.length === 0
  ) {
    const bytes = await readFile(artifact);
    arm64MachO =
      bytes.length >= 8 &&
      bytes.readUInt32LE(0) === 0xfeedfacf &&
      bytes.readUInt32LE(4) === 0x0100000c;
    if (arm64MachO) executed = run(artifact, []);
  }
  results.push({
    name,
    compileStatus: compiled.status,
    compilerSuccess: diagnostics.success,
    diagnostics: diagnostics.diagnostics.map(
      ({ code, message, line, column }) => ({ code, message, line, column }),
    ),
    arm64MachO,
    executed,
    passed:
      executed !== null &&
      executed.status === 0 &&
      !executed.error &&
      executed.stdout.trim() === expected &&
      executed.stderr.trim() === "",
  });
}
try {
  if (run(compiler, ["--version"]).stdout.trim() !== "0.3.5")
    throw new Error("Compiler version mismatch");
  await writeFile(
    join(directory, "package.json"),
    JSON.stringify({ private: true, type: "module" }),
  );
  await writeFile(
    join(directory, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        target: "ES2022",
        module: "NodeNext",
        moduleResolution: "NodeNext",
        allowJs: true,
        checkJs: false,
        strictNullChecks: true,
        skipLibCheck: true,
      },
    }),
  );
  for (const source of ["control.ts", "signal.ts", "renderer.ts"])
    await copyFile(join(fixtures, source), join(directory, source));
  const expected = "typenative-solid-signal-v1:0,1";
  const reference = run(process.execPath, [join(directory, "signal.ts")]);
  results.push({
    name: "node-reference",
    ...reference,
    passed:
      reference.status === 0 &&
      reference.stdout.trim() === expected &&
      reference.stderr.trim() === "",
  });
  await native("control", "control.ts", "typenative-solid-control-v1");
  await native("direct", "signal.ts", expected, ["--npm-static", "solid-js"]);
  // Syntax minification removes empty if bodies that ScriptC's typecheck rejects.
  // This is a standard bundle, not a patched or substituted Solid implementation.
  await require("esbuild").build({
    entryPoints: [join(directory, "signal.ts")],
    outfile: join(directory, "bundled.js"),
    bundle: true,
    platform: "neutral",
    format: "esm",
    minifySyntax: true,
  });
  const bundledReference = run(process.execPath, [
    join(directory, "bundled.js"),
  ]);
  results.push({
    name: "node-bundled-reference",
    ...bundledReference,
    passed:
      bundledReference.status === 0 &&
      bundledReference.stdout.trim() === expected &&
      bundledReference.stderr.trim() === "",
  });
  await native("bundled", "bundled.js", expected);
  await require("esbuild").build({
    entryPoints: [join(directory, "renderer.ts")],
    outfile: join(directory, "renderer.js"),
    bundle: true,
    platform: "neutral",
    conditions: ["browser"],
    format: "esm",
    minifySyntax: true,
  });
  const rendererReference = run(process.execPath, [
    join(directory, "renderer.js"),
  ]);
  const rendererExpected = "typenative-solid-renderer-v1:0,1";
  results.push({
    name: "node-renderer-reference",
    ...rendererReference,
    passed:
      rendererReference.status === 0 &&
      rendererReference.stdout.trim() === rendererExpected &&
      rendererReference.stderr.trim() === "",
  });
  await native("renderer", "renderer.js", rendererExpected);
} finally {
  await rm(directory, { recursive: true, force: true });
}
console.log(
  JSON.stringify(
    {
      platform: "macos-arm64",
      node: process.version,
      versions: {
        scriptc: "0.3.5",
        solid: "1.9.17",
        esbuild: "0.28.2",
        typescript: "5.9.3",
      },
      results,
      blitz: "not-tested",
      engineAbsence: "unverified",
    },
    null,
    2,
  ),
);
process.exitCode =
  results.length === 7 && results.every((result) => result.passed) ? 0 : 1;
