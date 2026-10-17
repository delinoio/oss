import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  hostTarget,
  isNativeExecutable,
  runTool,
  Target,
  versions,
} from "../dist/core.js";

const start = performance.now();
const target = hostTarget();
const require = createRequire(import.meta.url);
let compiler = process.env.TN_SCRIPTC || "scriptc";
if (!isAbsolute(compiler) && /[/\\]/.test(compiler))
  compiler = resolve(compiler);
let directory;
let phase = "prerequisites";
function report(status, details = {}) {
  process.stdout.write(
    JSON.stringify({
      schemaVersion: 1,
      check: "react-core-subset",
      versions,
      target: target ?? null,
      phase,
      durationMs: Math.round(performance.now() - start),
      status,
      fullReactConformance: "unverified",
      rendererAcceptance: "unverified",
      engineAbsence: "unverified",
      ...details,
    }) + "\n",
  );
}
try {
  if (!target) throw new Error("TN_HOST_UNSUPPORTED");
  const version = runTool(compiler, ["--version"]);
  if (
    version.failed ||
    version.status !== 0 ||
    version.stdout.trim() !== versions.scriptc
  ) {
    throw new Error("TN_COMPILER_UNAVAILABLE_OR_MISMATCHED");
  }
  if (require("react/package.json").version !== versions.react)
    throw new Error("TN_REACT_VERSION_MISMATCH");
  directory = await mkdtemp(join(tmpdir(), "typenative-react-"));
  const source = fileURLToPath(
    new URL("fixtures/react-core.ts", import.meta.url),
  );
  const artifact = join(
    directory,
    target === Target.Windows ? "react-probe.exe" : "react-probe",
  );
  phase = "compile";
  const result = runTool(compiler, [
    "build",
    source,
    "--npm-static",
    "react",
    "--print",
    "diagnostics",
    "--no-keep-llvm",
    "-o",
    artifact,
  ]);
  let diagnostics;
  try {
    diagnostics = JSON.parse(result.stdout);
  } catch {
    throw new Error("TN_COMPILER_DIAGNOSTICS_INVALID");
  }
  if (
    diagnostics.schema !== "scriptc-diagnostics" ||
    diagnostics.schemaVersion !== 1 ||
    diagnostics.compilerVersion !== versions.scriptc
  ) {
    throw new Error("TN_COMPILER_DIAGNOSTICS_INVALID");
  }
  if (result.failed || result.status !== 0 || diagnostics.success !== true) {
    // Only expose compiler codes and numeric locations for this fixed fixture.
    // Raw compiler messages can include installation paths and source content.
    report("failed", {
      code: "TN_REACT_COMPILE_FAILED",
      diagnostics: (Array.isArray(diagnostics.diagnostics)
        ? diagnostics.diagnostics
        : []
      ).map((item) => ({
        code:
          typeof item?.code === "string" && /^SC\d+$/.test(item.code)
            ? item.code
            : "UNKNOWN",
        fixture: item?.file === source ? "react-core.ts" : null,
        line: Number.isInteger(item?.line) ? item.line : null,
        column: Number.isInteger(item?.column) ? item.column : null,
      })),
    });
    process.exitCode = 1;
  } else {
    phase = "execute";
    const bytes = await readFile(artifact);
    if (!isNativeExecutable(bytes, target))
      throw new Error("TN_NATIVE_ARTIFACT_INVALID");
    const executed = runTool(artifact, [], directory);
    if (
      executed.failed ||
      executed.status !== 0 ||
      executed.stdout.trim() !== "typenative-react-core-probe-v1"
    ) {
      throw new Error("TN_REACT_EXECUTION_FAILED");
    }
    report("passed", {
      bytes: bytes.length,
      sha256: createHash("sha256").update(bytes).digest("hex"),
    });
  }
} catch (error) {
  const code =
    error instanceof Error && /^TN_[A-Z_]+$/.test(error.message)
      ? error.message
      : "TN_NATIVE_PROBE_FAILED";
  report("failed", { code });
  process.exitCode = 1;
} finally {
  if (directory) await rm(directory, { recursive: true, force: true });
}
