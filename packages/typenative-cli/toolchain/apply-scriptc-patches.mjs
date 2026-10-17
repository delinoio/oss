import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const defaultManifest = fileURLToPath(new URL("scriptc.json", import.meta.url));

// Patch only an explicitly selected clean checkout. Never fetch, install, reset,
// or update a compiler implicitly; native runtime artifacts need a separate build.
export function applyCompilerPatch(
  checkout,
  { checkOnly = false, manifestPath = defaultManifest } = {},
) {
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  if (
    manifest.schemaVersion !== 1 ||
    !/^[0-9a-f]{40}$/.test(manifest.revision) ||
    !/^[0-9a-f]{64}$/.test(manifest.sha256) ||
    typeof manifest.patch !== "string" ||
    !/^[a-z0-9-]+\.patch$/.test(manifest.patch)
  )
    throw new Error("TN_COMPILER_PATCH_MANIFEST_INVALID");
  const patch = readFileSync(resolve(dirname(manifestPath), manifest.patch));
  if (createHash("sha256").update(patch).digest("hex") !== manifest.sha256)
    throw new Error("TN_COMPILER_PATCH_INTEGRITY");
  const git = (args, input) =>
    execFileSync("git", ["-C", resolve(checkout), ...args], {
      encoding: "utf8",
      input,
      stdio: ["pipe", "pipe", "pipe"],
    });
  if (git(["rev-parse", "HEAD"]).trim() !== manifest.revision)
    throw new Error("TN_COMPILER_SOURCE_REVISION_MISMATCH");
  if (git(["status", "--porcelain", "--untracked-files=no"]).trim() !== "")
    throw new Error("TN_COMPILER_SOURCE_DIRTY");
  git(["apply", "--check", "-"], patch);
  if (!checkOnly) git(["apply", "-"], patch);
  return {
    code: checkOnly ? "TN_COMPILER_PATCH_CHECKED" : "TN_COMPILER_PATCH_APPLIED",
    revision: manifest.revision,
    patchSha256: manifest.sha256,
  };
}

if (
  process.argv[1] &&
  pathToFileURL(resolve(process.argv[1])).href === import.meta.url
) {
  try {
    const args = process.argv.slice(2);
    const checkOnly = args[0] === "--check";
    if (checkOnly) args.shift();
    if (args.length !== 1 || args[0].startsWith("-"))
      throw new Error("TN_ARGUMENTS");
    process.stdout.write(
      `${JSON.stringify(applyCompilerPatch(args[0], { checkOnly }))}\n`,
    );
  } catch (error) {
    const code =
      error instanceof Error && /^TN_[A-Z_]+$/.test(error.message)
        ? error.message
        : "TN_COMPILER_PATCH_FAILED";
    process.stderr.write(
      `${code}: Select a clean checkout at the pinned revision and verify the patch files.\n`,
    );
    process.exitCode = 1;
  }
}
