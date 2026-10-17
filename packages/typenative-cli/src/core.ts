import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";

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
  const magic = Buffer.from(bytes.subarray(0, 4)).toString("hex");
  if (target === Target.MacOS) return magic === "cffaedfe";
  if (target === Target.Linux) return magic === "7f454c46";
  if (target === Target.Windows) return magic.startsWith("4d5a");
  return false;
}

export async function probeNative(
  compiler: string,
  target: Target,
  runner: Runner = runTool,
) {
  if (target !== hostTarget())
    throw new DiagnosticError(
      Code.Host,
      "The native probe requires the current desktop target. Cross-platform execution is unverified.",
    );
  const directory = await mkdtemp(join(tmpdir(), "typenative-probe-"));
  try {
    const source = join(directory, "probe.ts");
    const artifact = join(
      directory,
      target === Target.Windows ? "probe.exe" : "probe",
    );
    await writeFile(source, 'console.log("typenative-native-probe-v1");\n', {
      flag: "wx",
    });
    const compiled = runner(
      compiler,
      ["build", source, "--no-keep-llvm", "-o", artifact],
      directory,
    );
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
    const executed = runner(artifact, [], directory);
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
    await rm(directory, { recursive: true, force: true });
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
