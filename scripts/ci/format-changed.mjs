// SPDX-License-Identifier: Apache-2.0
import { execFileSync, spawnSync } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const require = createRequire(import.meta.url);
const extensions = new Set([
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".ts",
  ".tsx",
  ".mts",
  ".cts",
  ".css",
  ".json",
  ".jsonc",
  ".json5",
  ".yaml",
  ".yml",
  ".md",
  ".mdx",
]);
const Mode = Object.freeze({ Check: "check", Write: "write" });
const git = (cwd, ...args) =>
  execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });

function revision(cwd, value) {
  if (!/^[0-9a-f]{40}$/u.test(value ?? "") || /^0+$/u.test(value)) {
    throw new Error(
      "Formatting requires a nonzero 40-character comparison SHA",
    );
  }
  git(cwd, "cat-file", "-e", `${value}^{commit}`);
  return value;
}

export function allFormatFiles(cwd) {
  return git(cwd, "ls-files", "-z")
    .split("\0")
    .filter((name) => {
      if (!extensions.has(extname(name))) return false;
      try {
        return lstatSync(resolve(cwd, name)).isFile();
      } catch (error) {
        if (error.code === "ENOENT") return false;
        throw error;
      }
    });
}

export function stagedFormatFiles(cwd) {
  return git(
    cwd,
    "diff",
    "--cached",
    "--name-only",
    "--no-renames",
    "--diff-filter=ACMRT",
    "-z",
    "--",
  )
    .split("\0")
    .filter((name) => {
      if (!extensions.has(extname(name))) return false;
      try {
        return lstatSync(resolve(cwd, name)).isFile();
      } catch (error) {
        if (error.code === "ENOENT") return false;
        throw error;
      }
    })
    .sort();
}

export function changedFormatFiles(cwd, base, head, local = false) {
  revision(cwd, base);
  revision(cwd, head);
  const names = git(
    cwd,
    "diff",
    "--name-only",
    "--no-renames",
    "-z",
    base,
    head,
    "--",
  ).split("\0");
  if (local) {
    names.push(
      ...git(
        cwd,
        "diff",
        "--name-only",
        "--no-renames",
        "-z",
        head,
        "--",
      ).split("\0"),
    );
    names.push(
      ...git(cwd, "ls-files", "--others", "--exclude-standard", "-z").split(
        "\0",
      ),
    );
  }
  // The checkout may be a PR merge commit. Select paths from the planner range,
  // then check the actual validation tree; never replace it with head contents.
  const present = new Set(
    git(
      cwd,
      "ls-files",
      "--cached",
      "--others",
      "--exclude-standard",
      "-z",
    ).split("\0"),
  );
  return [...new Set(names)]
    .filter((name) => {
      if (!name || !extensions.has(extname(name)) || !present.has(name))
        return false;
      try {
        return lstatSync(resolve(cwd, name)).isFile();
      } catch (error) {
        if (error.code === "ENOENT" && local) return false;
        throw error;
      }
    })
    .sort();
}

export function runFormatter(cwd, paths, mode = Mode.Check) {
  if (!Object.values(Mode).includes(mode))
    throw new Error("Unknown formatting mode");
  if (!paths.length) return 0;
  const manifestPath = require.resolve("rstack/package.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  const entry = resolve(dirname(manifestPath), manifest.bin.rs);
  let status = 0;
  // Bound argv size and prefix paths so leading dashes and negation characters
  // cannot become CLI flags or exclusion patterns. Rstack resolves files first.
  for (let index = 0; index < paths.length; index += 50) {
    const result = spawnSync(
      process.execPath,
      [
        entry,
        "fmt",
        `--${mode}`,
        "--no-cache",
        "--no-error-on-unmatched-pattern",
        ...paths.slice(index, index + 50).map((name) => `./${name}`),
      ],
      { cwd, stdio: "inherit", shell: false },
    );
    if (result.error) throw result.error;
    status = Math.max(status, result.status ?? 2);
  }
  return status;
}

// CI is enabled when the variable is set to anything except an empty value or
// an explicit, case-insensitive "false". Other values, including "0", stay
// enabled so ambiguous CI environments keep the stricter comparison rules.
export function ciEnabled(env = process.env) {
  const value = env.CI?.trim().toLowerCase();
  return value !== undefined && value !== "" && value !== "false";
}

export function main(env = process.env) {
  const { values } = parseArgs({
    options: {
      write: { type: "boolean" },
      all: { type: "boolean" },
      staged: { type: "boolean" },
      base: { type: "string" },
      head: { type: "string" },
    },
  });
  const cwd = process.cwd();
  const ci = ciEnabled(env);
  const explicit =
    values.base !== undefined ||
    values.head !== undefined ||
    env.TURBO_SCM_BASE !== undefined ||
    env.TURBO_SCM_HEAD !== undefined;
  if (values.staged && (ci || values.all || explicit))
    throw new Error(
      "Staged formatting requires local execution without a comparison or --all",
    );
  if (ci && !explicit)
    throw new Error("CI formatting requires the planner comparison");
  const head = values.staged
    ? null
    : explicit
      ? (values.head ?? env.TURBO_SCM_HEAD)
      : git(cwd, "rev-parse", "HEAD").trim();
  const base = values.staged
    ? null
    : explicit
      ? (values.base ?? env.TURBO_SCM_BASE)
      : git(cwd, "merge-base", "origin/main", head).trim();
  if (values.all && ci) throw new Error("CI formatting must use changed files");
  const paths = values.staged
    ? stagedFormatFiles(cwd)
    : values.all
      ? allFormatFiles(cwd)
      : changedFormatFiles(cwd, base, head, !explicit);
  const mode = values.write ? Mode.Write : Mode.Check;
  console.log(
    JSON.stringify({
      event: "ci_format",
      base,
      head,
      validationRevision: values.staged
        ? null
        : git(cwd, "rev-parse", "HEAD").trim(),
      staged: values.staged ?? false,
      mode,
      candidates: paths.length,
      local: !explicit,
    }),
  );
  const status = runFormatter(cwd, paths, mode);
  console.log(
    JSON.stringify({
      event: "ci_format_result",
      status,
      candidates: paths.length,
    }),
  );
  return status;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    process.exitCode = main();
  } catch (error) {
    console.error(
      JSON.stringify({ event: "ci_format_error", message: error.message }),
    );
    process.exitCode = 2;
  }
}
