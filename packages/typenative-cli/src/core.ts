import { spawn, spawnSync } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, delimiter, isAbsolute, join, resolve } from "node:path";

export const versions = Object.freeze({
  framework: "0.0.0-dev.0",
  react: "19.3.1",
  scriptc: "0.3.5",
  blitz: "1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01",
});

export enum Target {
  MacOS = "macos-arm64",
  Windows = "windows-x64",
  Linux = "linux-x64",
  IOS = "ios-arm64-simulator",
  Android = "android-arm64-emulator",
}

export enum Code {
  Ok = "TN_OK",
  Arguments = "TN_ARGUMENTS",
  Create = "TN_CREATE_FAILED",
  Tool = "TN_TOOL_UNAVAILABLE",
  Version = "TN_VERSION_MISMATCH",
  Host = "TN_HOST_UNSUPPORTED",
  Compile = "TN_PROBE_COMPILE_FAILED",
  Artifact = "TN_PROBE_ARTIFACT_INVALID",
  Execute = "TN_PROBE_EXECUTION_FAILED",
  Pipeline = "TN_NATIVE_PIPELINE_UNAVAILABLE",
  Internal = "TN_INTERNAL",
  Interrupted = "TN_PROBE_INTERRUPTED",
}

export class DiagnosticError extends Error {
  constructor(
    public readonly code: Code,
    message: string,
  ) {
    super(message);
  }
}

export interface CommandResult {
  status: number | null;
  stdout: string;
  failed: boolean;
}
export type Runner = (
  command: string,
  args: string[],
  cwd?: string,
) => CommandResult;

// Capture bounded output for checks only. Tool output can contain paths or secrets
// and must not be copied into default diagnostics.
export const runTool: Runner = (command, args, cwd) => {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    shell: false,
    timeout: 120_000,
    maxBuffer: 1024 * 1024,
    killSignal: "SIGKILL",
    windowsHide: true,
  });
  return {
    status: result.status,
    stdout: result.stdout ?? "",
    failed: Boolean(result.error || result.signal),
  };
};

export type ProbeRunner = (
  command: string,
  args: string[],
  cwd?: string,
  signal?: AbortSignal,
) => CommandResult | Promise<CommandResult>;

// Probe children must not block signal handlers while compilation or execution
// is running. On POSIX, stop the process group, including compiler helpers.
export const runProbeTool: ProbeRunner = (command, args, cwd, signal) =>
  new Promise((resolveResult) => {
    if (signal?.aborted)
      return resolveResult({ status: null, stdout: "", failed: true });
    const child = spawn(command, args, {
      cwd,
      shell: false,
      windowsHide: true,
      detached: process.platform !== "win32",
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let size = 0;
    let failed = false;
    const stop = () => {
      failed = true;
      if (!child.pid) return;
      try {
        if (process.platform !== "win32") process.kill(-child.pid, "SIGKILL");
        else child.kill("SIGKILL");
      } catch {
        child.kill("SIGKILL");
      }
    };
    const timer = setTimeout(stop, 120_000);
    signal?.addEventListener("abort", stop, { once: true });
    const capture = (data: Buffer, retain: boolean) => {
      size += data.length;
      if (size > 1024 * 1024) stop();
      else if (retain) stdout += data.toString("utf8");
    };
    child.stdout.on("data", (data: Buffer) => capture(data, true));
    child.stderr.on("data", (data: Buffer) => capture(data, false));
    child.on("error", () => {
      failed = true;
    });
    child.on("close", (status, killedBy) => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", stop);
      resolveResult({ status, stdout, failed: failed || killedBy !== null });
    });
  });

export function resolveExecutable(command: string): string {
  if (isAbsolute(command)) return command;
  if (/[/\\]/.test(command)) return resolve(command);
  const extensions = process.platform === "win32" ? ["", ".exe", ".com"] : [""];
  for (const entry of (process.env.PATH ?? "").split(delimiter)) {
    for (const extension of extensions) {
      const candidate = resolve(entry || ".", command + extension);
      try {
        accessSync(candidate, constants.X_OK);
        if (statSync(candidate).isFile()) return candidate;
      } catch {
        /* Continue to the next PATH entry. */
      }
    }
  }
  return command;
}

export function hostTarget(
  platform = process.platform,
  arch = process.arch,
): Target | undefined {
  if (platform === "darwin" && arch === "arm64") return Target.MacOS;
  if (platform === "win32" && arch === "x64") return Target.Windows;
  if (platform === "linux" && arch === "x64") return Target.Linux;
  return undefined;
}

export interface Check {
  tool: string;
  code: Code;
  ok: boolean;
  message: string;
}

export function inspectTools(
  target: Target,
  compiler: string,
  runner: Runner = runTool,
): Check[] {
  const spec: [string, string, string[]][] = [
    ["scriptc", compiler, ["--version"]],
    ["rustc", "rustc", ["--version"]],
    ["cargo", "cargo", ["--version"]],
  ];
  switch (target) {
    case Target.MacOS:
    case Target.IOS:
      spec.push(["xcode", "xcodebuild", ["-version"]]);
      spec.push([
        "apple-sdk",
        "xcrun",
        [
          "--sdk",
          target === Target.IOS ? "iphonesimulator" : "macosx",
          "--show-sdk-path",
        ],
      ]);
      break;
    case Target.Linux:
      spec.push(["cc", "cc", ["--version"]]);
      break;
    case Target.Windows:
      spec.push(["clang", "clang", ["--version"]]);
      break;
    case Target.Android:
      spec.push(
        ["adb", "adb", ["version"]],
        ["emulator", "emulator", ["-version"]],
      );
      break;
  }
  return spec.map(([tool, executable, args]) => {
    const result = runner(executable, args);
    if (result.failed || result.status !== 0)
      return {
        tool,
        code: Code.Tool,
        ok: false,
        message: `Install and configure ${tool}, then rerun doctor.`,
      };
    if (tool === "scriptc" && result.stdout.trim() !== versions.scriptc)
      return {
        tool,
        code: Code.Version,
        ok: false,
        message: `Select ScriptC ${versions.scriptc}; automatic upgrades are disabled.`,
      };
    return {
      tool,
      code: Code.Ok,
      ok: true,
      message: `${tool} prerequisite check passed.`,
    };
  });
}

export function isNativeExecutable(bytes: Uint8Array, target: Target): boolean {
  const header = Buffer.from(bytes);
  // These checks identify the selected native architecture, not loader validity.
  // Thin Mach-O: mach_header_64 with CPU_TYPE_ARM64.
  if (target === Target.MacOS)
    return (
      header.length >= 32 &&
      header.readUInt32LE(0) === 0xfeedfacf &&
      header.readUInt32LE(4) === 0x0100000c
    );
  // ELF64, little-endian, current format version and EM_X86_64.
  if (target === Target.Linux)
    return (
      header.length >= 64 &&
      header.subarray(0, 4).toString("hex") === "7f454c46" &&
      header[4] === 2 &&
      header[5] === 1 &&
      header[6] === 1 &&
      header.readUInt16LE(18) === 62
    );
  if (target === Target.Windows) {
    if (header.length < 64 || header.readUInt16LE(0) !== 0x5a4d) return false;
    const offset = header.readUInt32LE(0x3c);
    // e_lfanew points to the PE signature, COFF header and PE32+ magic.
    return (
      offset >= 64 &&
      offset <= header.length - 26 &&
      header.readUInt32LE(offset) === 0x00004550 &&
      header.readUInt16LE(offset + 4) === 0x8664 &&
      header.readUInt16LE(offset + 24) === 0x20b
    );
  }
  return false;
}

export async function probeNative(
  compiler: string,
  target: Target,
  runner: ProbeRunner = runProbeTool,
) {
  if (target !== hostTarget())
    throw new DiagnosticError(
      Code.Host,
      "The native probe requires the current desktop target. Cross-platform execution is unverified.",
    );
  const controller = new AbortController();
  const interrupt = () => controller.abort();
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", interrupt);
  let directory: string | undefined;
  const checkInterrupted = () => {
    if (controller.signal.aborted)
      throw new DiagnosticError(
        Code.Interrupted,
        "Native probe interrupted; temporary artifacts were removed.",
      );
  };
  try {
    directory = await mkdtemp(join(tmpdir(), "typenative-probe-"));
    checkInterrupted();
    const source = join(directory, "probe.ts");
    const artifact = join(
      directory,
      target === Target.Windows ? "probe.exe" : "probe",
    );
    await writeFile(source, 'console.log("typenative-native-probe-v1");\n', {
      flag: "wx",
    });
    const compiled = await runner(
      compiler,
      ["build", source, "--no-keep-llvm", "-o", artifact],
      directory,
      controller.signal,
    );
    checkInterrupted();
    if (compiled.failed || compiled.status !== 0)
      throw new DiagnosticError(
        Code.Compile,
        "ScriptC could not compile the fixed native probe. Check the compiler and platform SDK installation.",
      );
    let bytes: Buffer;
    try {
      bytes = await readFile(artifact);
    } catch {
      throw new DiagnosticError(
        Code.Artifact,
        "ScriptC did not produce a readable native probe artifact.",
      );
    }
    if (!isNativeExecutable(bytes, target))
      throw new DiagnosticError(
        Code.Artifact,
        "The probe output does not have the expected native executable header.",
      );
    checkInterrupted();
    const executed = await runner(artifact, [], directory, controller.signal);
    checkInterrupted();
    if (
      executed.failed ||
      executed.status !== 0 ||
      executed.stdout.trim() !== "typenative-native-probe-v1"
    ) {
      throw new DiagnosticError(
        Code.Execute,
        "The native probe failed or returned unexpected output.",
      );
    }
    return {
      sha256: createHash("sha256").update(bytes).digest("hex"),
      bytes: bytes.length,
    };
  } finally {
    try {
      if (directory) await rm(directory, { recursive: true, force: true });
    } finally {
      process.removeListener("SIGINT", interrupt);
      process.removeListener("SIGTERM", interrupt);
    }
  }
}

export async function createProject(destination: string) {
  const directory = resolve(destination);
  const name = basename(directory);
  if (!/^[a-z][a-z0-9-]*$/.test(name))
    throw new DiagnosticError(
      Code.Arguments,
      "Use a directory name that starts with a lowercase letter and contains lowercase letters, digits or hyphens.",
    );
  // Reserve the directory exclusively. Never merge with an existing directory or
  // symlink, and never recursively remove a directory that another actor can edit.
  try {
    await mkdir(directory);
  } catch {
    throw new DiagnosticError(
      Code.Create,
      "Cannot create the project. Select a new directory with an existing writable parent.",
    );
  }
  const files: Record<string, string> = {
    "package.json":
      JSON.stringify(
        {
          name,
          version: "0.0.0",
          private: true,
          type: "module",
          dependencies: { react: versions.react },
        },
        null,
        2,
      ) + "\n",
    "typenative.json":
      JSON.stringify(
        { schemaVersion: 1, versions, entry: "App.tsx" },
        null,
        2,
      ) + "\n",
    "tsconfig.json":
      JSON.stringify(
        {
          compilerOptions: {
            target: "ES2023",
            module: "ESNext",
            moduleResolution: "Bundler",
            jsx: "react-jsx",
            strict: true,
          },
          include: ["App.tsx"],
        },
        null,
        2,
      ) + "\n",
    "App.tsx":
      'import { useState } from "react";\n\nexport default function App() {\n  const [count, setCount] = useState(0);\n  return <button onClick={() => setCount(count + 1)}>Count: {count}</button>;\n}\n',
    ".gitignore": "node_modules/\n.typenative/\ndist/\n",
    "README.md":
      "# TypeNative source scaffold\n\nThis is a source scaffold, not a runnable native application yet. The React renderer, Blitz host and native development pipeline are not implemented. The SDK is not published. Do not use a browser or JavaScript-engine fallback as native validation.\n\nRun `tn doctor` to inspect selected prerequisites. Run `tn doctor --probe` for a fixed console-program compiler check. Neither command validates this React component. Platform execution and React conformance remain unverified.\n",
  };
  try {
    for (const [file, content] of Object.entries(files))
      await writeFile(join(directory, file), content, { flag: "wx" });
  } catch {
    throw new DiagnosticError(
      Code.Create,
      "Scaffolding failed. Inspect the partially created directory before retrying; no existing files were removed.",
    );
  }
}
