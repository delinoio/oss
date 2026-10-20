// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { lowerTsx } from "./lower.mjs";

if (process.argv.length !== 4)
  throw new Error(
    "Usage: node run.mjs <dependency-directory> <new-output-directory>",
  );
if (
  process.platform !== "darwin" ||
  process.arch !== "arm64" ||
  process.versions.node.split(".")[0] !== "24"
)
  throw new Error("Use Node 24 on macOS arm64.");
const dependencyRoot = resolve(process.argv[2]);
const output = resolve(process.argv[3]);
const fixtures = dirname(fileURLToPath(import.meta.url));
const require = createRequire(join(dependencyRoot, "package.json"));
const ts = require("typescript");
if (
  ts.version !== "5.9.3" ||
  require("scriptc/package.json").version !== "0.3.5"
)
  throw new Error("Select TypeScript 5.9.3 and ScriptC 0.3.5.");
const compiler = join(dependencyRoot, "node_modules/scriptc/bin/scriptc.exe");
await mkdir(output);
const expected = "typenative-custom-tsx-v1:0,1";
const results = [];
let passed = false;
let failure = null;
function run(name, command, args, timeout = 120_000, extraEnv = {}) {
  process.stderr.write(`custom-tsx: ${name}\n`);
  const result = spawnSync(command, args, {
    cwd: output,
    env: { ...process.env, ...extraEnv },
    encoding: "utf8",
    timeout,
    maxBuffer: 16 * 1024 * 1024,
  });
  const record = {
    name,
    command,
    args,
    status: result.status,
    signal: result.signal,
    error: result.error?.code ?? null,
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
  };
  results.push(record);
  return record;
}
function success(result) {
  if (result.status !== 0 || result.error)
    throw new Error(`${result.name} failed; see result.json`);
}
function execution(result) {
  success(result);
  if (result.stdout.trim() !== expected || result.stderr.trim() !== "")
    throw new Error(`${result.name} output mismatch; see result.json`);
}
try {
  const version = run("compiler-version", compiler, ["--version"]);
  success(version);
  if (version.stdout.trim() !== "0.3.5")
    throw new Error("Compiler version mismatch");
  for (const file of ["Cargo.toml", "Cargo.lock", "host.rs"])
    await copyFile(join(fixtures, file), join(output, file));
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
        allowJs: true,
        checkJs: false,
      },
    }),
  );
  const source = await readFile(join(fixtures, "app.tsx"), "utf8");
  const lowered = lowerTsx(ts, source);
  const transformed = ts.transpileModule(lowered, {
    fileName: "app.ts",
    reportDiagnostics: true,
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
    },
  });
  if (transformed.diagnostics.length)
    throw new Error("TSX transform diagnostics");
  await writeFile(join(output, "app.ts"), lowered);
  const reference = await readFile(join(fixtures, "reference.js"), "utf8");
  await writeFile(
    join(output, "reference-app.js"),
    reference + "\n" + transformed.outputText,
  );
  execution(
    run("node-reference", process.execPath, [join(output, "reference-app.js")]),
  );
  const referenceBuild = run("native-reference-build", compiler, [
    "build",
    "reference-app.js",
    "--print",
    "diagnostics",
    "--no-keep-llvm",
    "-o",
    "reference-native",
  ]);
  success(referenceBuild);
  const referenceDiagnostics = JSON.parse(referenceBuild.stdout);
  if (
    referenceDiagnostics.success !== true ||
    referenceDiagnostics.diagnostics.length
  )
    throw new Error("Native reference compiler diagnostics");
  execution(
    run("native-reference-execute", join(output, "reference-native"), []),
  );
  const build = run(
    "blitz-host-build",
    "cargo",
    [
      "rustc",
      "--locked",
      "--manifest-path",
      join(output, "Cargo.toml"),
      "--target-dir",
      join(output, "target"),
      "--",
      "--print",
      "native-static-libs",
    ],
    900_000,
  );
  success(build);
  const linkLine = build.stderr.match(/native-static-libs: (.+)/)?.[1];
  if (!linkLine) throw new Error("Missing Rust native link requirements");
  const tokens = linkLine.trim().split(/\s+/);
  const frameworks = [];
  const systemLibraries = [];
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i] === "-framework") frameworks.push(tokens[++i]);
    else if (tokens[i].startsWith("-l"))
      systemLibraries.push(tokens[i].slice(2));
    else throw new Error(`Unsupported native link option: ${tokens[i]}`);
  }
  await writeFile(
    join(output, "ffi.json"),
    JSON.stringify(
      {
        ffi_format: 7,
        functions: [
          {
            name: "nativeInit",
            symbol: "tn_init",
            params: ["string"],
            returns: "void",
          },
          {
            name: "nativeText",
            symbol: "tn_text",
            params: ["i32", "string"],
            returns: "void",
          },
          {
            name: "nativeSnapshot",
            symbol: "tn_snapshot",
            params: ["i32"],
            returns: "i32",
          },
        ],
        libraries: ["./target/debug/libtypenative_custom_tsx_probe.a"],
        system_libraries: [...new Set(systemLibraries)],
        frameworks: [...new Set(frameworks)],
      },
      null,
      2,
    ),
  );
  const compiled = run("native-blitz-build", compiler, [
    "build",
    "app.ts",
    "--ffi",
    "ffi.json",
    "--print",
    "diagnostics",
    "--no-keep-llvm",
    "-o",
    "app-native",
  ]);
  success(compiled);
  const diagnostics = JSON.parse(compiled.stdout);
  if (
    diagnostics.schema !== "scriptc-diagnostics" ||
    diagnostics.compilerVersion !== "0.3.5" ||
    diagnostics.success !== true ||
    diagnostics.diagnostics.length
  )
    throw new Error("Native compiler diagnostics");
  const bytes = await readFile(join(output, "app-native"));
  if (
    bytes.readUInt32LE(0) !== 0xfeedfacf ||
    bytes.readUInt32LE(4) !== 0x0100000c
  )
    throw new Error("Expected arm64 Mach-O");
  execution(
    run("native-blitz-execute", join(output, "app-native"), [], 120_000, {
      TN_PROBE_OUTPUT: output,
    }),
  );
  const before = await readFile(join(output, "frame-0.png"));
  const after = await readFile(join(output, "frame-1.png"));
  if (
    !before
      .subarray(0, 8)
      .equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])) ||
    before.equals(after)
  )
    throw new Error("Expected distinct PNG frames");
  const graph = run("host-dependency-graph", "cargo", [
    "tree",
    "--locked",
    "--prefix",
    "none",
  ]);
  success(graph);
  if (
    /^(boa_engine|v8|deno_core|rquickjs|quick-js|mozjs|javascriptcore-rs)\s/m.test(
      graph.stdout,
    )
  )
    throw new Error("Unexpected scripting engine in host graph");
  passed = true;
} catch (error) {
  failure = error.message;
  process.stderr.write(`${error.message}\n`);
} finally {
  const revision = spawnSync("git", ["-C", fixtures, "rev-parse", "HEAD"], {
    encoding: "utf8",
  });
  const report = {
    passed,
    failure,
    sourceRevision: revision.stdout?.trim(),
    node: process.version,
    versions: {
      scriptc: "0.3.5",
      typescript: "5.9.3",
      blitz: "1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01",
    },
    results,
    scope: "same-process native FFI and offscreen pixels",
    unverified: [
      "native window",
      "OS input",
      "JSX components and reconciliation",
      "hot code updates",
      "other platforms",
      "complete embedded-engine absence",
    ],
  };
  await writeFile(
    join(output, "result.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  process.stdout.write(
    JSON.stringify({ passed, report: join(output, "result.json") }) + "\n",
  );
  process.exitCode = passed ? 0 : 1;
}
