"use strict";
const { createHash } = require("node:crypto");
const { readFileSync, lstatSync, realpathSync } = require("node:fs");
const { createRequire } = require("node:module");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { selectTarget } = require("./platforms.cjs");

function resolveBinary(
  manifestPath = path.join(__dirname, "../package.json"),
  target = selectTarget(),
) {
  if (!target)
    throw new Error(
      "Unsupported platform. CacheCow requires darwin-arm64, linux-x64, or windows-x64.",
    );
  const main = JSON.parse(readFileSync(manifestPath, "utf8"));
  if (
    main.name !== "@delino/ccow" ||
    !/^[0-9a-f]{40}$/.test(main.gitHead ?? "") ||
    !/^\d+\.\d+\.\d+$/.test(main.version ?? "") ||
    main.optionalDependencies?.[target.name] !== main.version
  )
    throw new Error(
      "Invalid CacheCow launcher identity. Reinstall @delino/ccow.",
    );
  let dependencyPath;
  try {
    dependencyPath = createRequire(manifestPath).resolve(
      `${target.name}/package.json`,
    );
  } catch {
    throw new Error(
      `Missing ${target.name}@${main.version}. Reinstall @delino/ccow with optional dependencies enabled.`,
    );
  }
  const native = JSON.parse(readFileSync(dependencyPath, "utf8"));
  if (
    native.name !== target.name ||
    native.version !== main.version ||
    native.gitHead !== main.gitHead ||
    native.ccow?.target !== target.suffix ||
    native.ccow?.fixture !== main.ccow?.fixture
  )
    throw new Error(
      "CacheCow release identity mismatch. Reinstall matching launcher and native packages.",
    );
  const directory = realpathSync(path.dirname(dependencyPath));
  const binary = path.join(directory, "bin", target.binary);
  if (
    !lstatSync(binary).isFile() ||
    realpathSync(binary) !== binary ||
    realpathSync(path.dirname(binary)) !== path.join(directory, "bin")
  )
    throw new Error(
      "Unsafe CacheCow executable path. Reinstall the native package.",
    );
  const digest = createHash("sha256")
    .update(readFileSync(binary))
    .digest("hex");
  if (digest !== native.ccow.sha256)
    throw new Error(
      "CacheCow executable checksum mismatch. Reinstall the native package.",
    );
  return binary;
}

async function launch(
  binary,
  args,
  { parent = process, spawnChild = spawn } = {},
) {
  if (Number(parent.versions.node.split(".")[0]) < 24)
    throw new Error("CacheCow npm requires Node.js 24 or newer.");
  if (parent.platform !== "win32") {
    // Replacing Node preserves PID, terminal delivery, exit signals, and native
    // process ownership. No wrapper remains to orphan a managed process tree.
    parent.execve(binary, [binary, ...args], parent.env);
    throw new Error("Native process replacement returned unexpectedly.");
  }
  return new Promise((resolve, reject) => {
    const child = spawnChild(binary, args, {
      stdio: "inherit",
      shell: false,
      windowsHide: false,
    });
    // Windows broadcasts console events to both processes. Node's child.kill
    // forcibly terminates the native handler, so let the native CLI handle them.
    const ignore = () => {};
    parent.on("SIGINT", ignore);
    parent.on("SIGBREAK", ignore);
    const cleanup = () => {
      parent.removeListener("SIGINT", ignore);
      parent.removeListener("SIGBREAK", ignore);
    };
    child.once("error", (error) => {
      cleanup();
      reject(error);
    });
    child.once("exit", (code, signal) => {
      cleanup();
      resolve({ code: code ?? 126, signal });
    });
  });
}
module.exports = { resolveBinary, launch };
