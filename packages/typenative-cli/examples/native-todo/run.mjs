// SPDX-License-Identifier: Apache-2.0
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import { dirname, join, resolve, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { lowerTsx } from "../../test/experiments/custom-tsx/lower.mjs";
import { runBuildTool } from "./build-tool.mjs";
import { sha256, snapshotBuildInputs } from "./build-inputs.mjs";
import { externalBuildPath } from "./build-paths.mjs";

if (
  process.argv.length < 4 ||
  process.argv.length > 5 ||
  (process.argv[4] !== undefined && process.argv[4] !== "--build-only")
)
  throw new Error(
    "Usage: node run.mjs <dependency-directory> <new-output-directory> [--build-only]",
  );
if (
  process.platform !== "darwin" ||
  process.arch !== "arm64" ||
  process.versions.node.split(".")[0] !== "24"
)
  throw new Error(
    "Use Node 24 on macOS arm64 with Rust and the macOS SDK installed.",
  );
const sources = await realpath(dirname(fileURLToPath(import.meta.url)));
const repository = await realpath(resolve(sources, "../../../.."));
const dependencies = await externalBuildPath(repository, process.argv[2]);
const output = await externalBuildPath(repository, process.argv[3]);
const targetRoot = await externalBuildPath(
  repository,
  process.env.CARGO_TARGET_DIR ?? join(output, "target"),
);
const require = createRequire(join(dependencies, "package.json"));
const ts = require("typescript");
if (
  ts.version !== "5.9.3" ||
  require("scriptc/package.json").version !== "0.3.5"
)
  throw new Error(
    "Prepare exact dependencies: scriptc@0.3.5 and typescript@5.9.3.",
  );
const compiler = join(dependencies, "node_modules/scriptc/bin/scriptc.exe");
const cargoTarget = "aarch64-apple-darwin";
await mkdir(output);
const controller = new AbortController();
function stop() {
  controller.abort();
}
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
const steps = [];
let sourceRevision = null;
let sourceDirty = null;
let sourceHashes = {};
let generatedHashes = {};
async function run(name, command, args, timeout = 120_000) {
  if (controller.signal.aborted) throw new Error("Interrupted");
  process.stderr.write(`typenative-todo: ${name}\n`);
  const start = performance.now();
  const { stdout, stderr, ...result } = await runBuildTool(command, args, {
    cwd: output,
    signal: controller.signal,
    timeoutMs: timeout,
  });
  const record = {
    name,
    command,
    args,
    ...result,
    durationMs: Math.round(performance.now() - start),
  };
  steps.push(record);
  await writeFile(join(output, `${name}.stdout`), stdout);
  await writeFile(join(output, `${name}.stderr`), stderr);
  if (result.status !== 0 || result.aborted || result.timedOut)
    throw new Error(`${name} failed; inspect ${output}/${name}.stderr`);
  return { stdout, stderr };
}
let passed = false;
let failure = null;
try {
  const revision = await run("source-revision", "git", [
    "-C",
    repository,
    "rev-parse",
    "HEAD",
  ]);
  sourceRevision = revision.stdout.trim();
  const prefix = relative(repository, sources);
  const sourcePaths = [
    "Cargo.toml",
    "Cargo.lock",
    "host.rs",
    "validation.rs",
    "app.tsx",
    "run.mjs",
    "build-tool.mjs",
    "build-inputs.mjs",
    "build-paths.mjs",
  ].map((file) => join(prefix, file));
  sourcePaths.push(
    "packages/typenative-cli/test/experiments/custom-tsx/lower.mjs",
  );
  const status = await run("source-status", "git", [
    "-C",
    repository,
    "status",
    "--porcelain=v1",
    "--untracked-files=all",
    "--",
    ...sourcePaths,
  ]);
  sourceDirty = status.stdout.trim().length > 0;
  const inputs = await snapshotBuildInputs(repository, sourcePaths);
  sourceHashes = inputs.hashes;
  const version = await run("compiler-version", compiler, ["--version"]);
  if (version.stdout.trim() !== "0.3.5")
    throw new Error("Unexpected compiler version");
  for (const file of ["Cargo.toml", "Cargo.lock", "host.rs", "validation.rs"])
    await writeFile(join(output, file), inputs.files.get(join(prefix, file)));
  await writeFile(
    join(output, "package.json"),
    JSON.stringify({ private: true, type: "module" }),
  );
  await writeFile(
    join(output, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        target: "ES2022",
        module: "NodeNext",
        moduleResolution: "NodeNext",
        strictNullChecks: true,
      },
    }),
  );
  const lowered = lowerTsx(
    ts,
    inputs.files.get(join(prefix, "app.tsx")).toString("utf8"),
  );
  generatedHashes = { "app.ts": sha256(lowered) };
  await writeFile(join(output, "app.ts"), lowered);
  const rust = await run(
    "host-build",
    "cargo",
    [
      "rustc",
      "--locked",
      "--target",
      cargoTarget,
      "--target-dir",
      targetRoot,
      "--",
      "--print",
      "native-static-libs",
    ],
    900_000,
  );
  const link = rust.stderr.match(/native-static-libs: (.+)/)?.[1];
  if (!link) throw new Error("Missing Rust static link requirements");
  const tokens = link.trim().split(/\s+/);
  const frameworks = [];
  const systemLibraries = [];
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i] === "-framework") frameworks.push(tokens[++i]);
    else if (tokens[i].startsWith("-l"))
      systemLibraries.push(tokens[i].slice(2));
    else throw new Error(`Unsupported linker token: ${tokens[i]}`);
  }
  const manifest = {
    ffi_format: 7,
    functions: [
      {
        name: "nativeRun",
        symbol: "tn_todo_run",
        params: [
          "string",
          {
            callback: {
              id: "event",
              params: ["i32", "string", { context: "event" }],
              returns: "void",
              lifetime: "call",
            },
          },
          { context: "event" },
        ],
        returns: "void",
      },
      {
        name: "nativeUpdate",
        symbol: "tn_todo_update",
        params: ["i32", "string"],
        returns: "void",
      },
      {
        name: "nativeClearInput",
        symbol: "tn_todo_clear_input",
        params: [],
        returns: "void",
      },
    ],
    libraries: [
      join(targetRoot, cargoTarget, "debug/libtypenative_todo_window.a"),
    ],
    system_libraries: [...new Set(systemLibraries)],
    frameworks: [...new Set(frameworks)],
  };
  await writeFile(join(output, "ffi.json"), JSON.stringify(manifest, null, 2));
  const compiled = await run("app-build", compiler, [
    "build",
    "app.ts",
    "--ffi",
    "ffi.json",
    "--print",
    "diagnostics",
    "--no-keep-llvm",
    "-o",
    "typenative-todo",
  ]);
  const diagnostics = JSON.parse(compiled.stdout);
  if (diagnostics.success !== true || diagnostics.diagnostics.length !== 0)
    throw new Error("ScriptC reported diagnostics; inspect app-build.stdout");
  const bytes = await readFile(join(output, "typenative-todo"));
  if (
    bytes.readUInt32LE(0) !== 0xfeedfacf ||
    bytes.readUInt32LE(4) !== 0x0100000c
  )
    throw new Error("Expected a native arm64 Mach-O application");
  const selfTest = await run(
    "native-self-test",
    join(output, "typenative-todo"),
    ["--self-test", output],
  );
  if (selfTest.stdout.trim() !== "typenative-todo-self-test:passed")
    throw new Error("Native self-test output mismatch");
  const graph = await run("host-dependencies", "cargo", [
    "tree",
    "--locked",
    "--target",
    cargoTarget,
    "--prefix",
    "none",
  ]);
  if (
    /^(boa_engine|v8|deno_core|rquickjs|quick-js|mozjs|javascriptcore-rs)\s/m.test(
      graph.stdout,
    )
  )
    throw new Error("Unexpected scripting-engine dependency");
  passed = true;
} catch (error) {
  failure = error.message;
  process.exitCode = 1;
  process.stderr.write(`${failure}\n`);
} finally {
  await writeFile(
    join(output, "build-result.json"),
    JSON.stringify(
      {
        passed,
        failure,
        sourceRevision,
        sourceDirty,
        sourceHashes,
        generatedHashes,
        node: process.version,
        versions: {
          scriptc: "0.3.5",
          typescript: "5.9.3",
          blitz: "1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01",
        },
        target: "macos-arm64",
        steps,
        unverified: [
          "OS input",
          "screen reader",
          "Korean IME",
          "other platforms",
          "full binary engine audit",
        ],
      },
      null,
      2,
    ) + "\n",
  );
  process.removeListener("SIGINT", stop);
  process.removeListener("SIGTERM", stop);
}
if (passed) {
  process.stdout.write(
    JSON.stringify({
      executable: join(output, "typenative-todo"),
      report: join(output, "build-result.json"),
    }) + "\n",
  );
  if (process.argv[4] !== "--build-only") {
    // Node is a build/launch tool only. The application is the native executable.
    const app = spawn(join(output, "typenative-todo"), [], {
      stdio: "inherit",
    });
    const forward = (signal) => app.kill(signal);
    process.on("SIGINT", forward);
    process.on("SIGTERM", forward);
    app.on("error", (error) => {
      process.stderr.write(error.message + "\n");
      process.exitCode = 1;
    });
    app.on("close", (code) => {
      process.removeListener("SIGINT", forward);
      process.removeListener("SIGTERM", forward);
      process.exitCode = code ?? 1;
    });
  }
}
