// Regenerates the committed Rust test fixtures from real fixture builds.
//
// Runs each collector adapter against its fixture project, then writes the
// collection and the JavaScript chunks with source maps into
// crates/bundlewhy/tests/fixtures/<bundler>/ with machine-specific absolute
// paths replaced by stable placeholders that the Rust tests substitute.

import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const repoRoot = path.resolve(packageRoot, "../..");
const PROJECT_PLACEHOLDER = "/__bundlewhy_project__";
const BUILD_PLACEHOLDER = "/__bundlewhy_build__";

const targets = [
  {
    bundler: "vite",
    project: "vite-app",
    config: "vite.config.js",
    supportedVersion: "8.3.4",
  },
  {
    bundler: "rspack",
    project: "rspack-app",
    config: "rspack.config.mjs",
    supportedVersion: "2.2.9",
  },
];

function replaceAll(text, from, to) {
  return text.split(from).join(to);
}

for (const target of targets) {
  const projectRoot = path.join(packageRoot, "fixtures", target.project);
  const work = fs.mkdtempSync(
    path.join(os.tmpdir(), `bundlewhy-fixture-${target.bundler}-`),
  );
  const buildDir = path.join(work, "build");
  const collectPath = path.join(work, "collect.json");
  const requestPath = path.join(work, "request.json");
  fs.writeFileSync(
    requestPath,
    JSON.stringify({
      projectRoot,
      configFile: path.join(projectRoot, target.config),
      configName: null,
      buildDir,
      collectPath,
      supportedVersion: target.supportedVersion,
      allowUnsupportedEngine: false,
    }),
  );
  const result = spawnSync(
    process.execPath,
    [
      path.join(repoRoot, "crates/bundlewhy/adapters", `${target.bundler}.mjs`),
      requestPath,
    ],
    {
      cwd: projectRoot,
      encoding: "utf8",
    },
  );
  if (result.status !== 0) {
    console.error(result.stdout, result.stderr);
    process.exit(1);
  }
  const outRoot = path.join(
    repoRoot,
    "crates/bundlewhy/tests/fixtures",
    target.bundler,
  );
  fs.rmSync(outRoot, { recursive: true, force: true });
  fs.mkdirSync(path.join(outRoot, "build"), { recursive: true });
  const realBuild = fs.realpathSync(buildDir);
  let collection = fs.readFileSync(collectPath, "utf8");
  for (const [from, to] of [
    [realBuild, BUILD_PLACEHOLDER],
    [buildDir, BUILD_PLACEHOLDER],
    [projectRoot, PROJECT_PLACEHOLDER],
    [path.relative(process.cwd(), projectRoot), "fixtures/app"],
  ]) {
    collection = replaceAll(collection, JSON.stringify(from).slice(1, -1), to);
  }
  fs.writeFileSync(
    path.join(outRoot, "collect.json"),
    `${JSON.stringify(JSON.parse(collection), null, 1)}\n`,
  );
  for (const file of fs.readdirSync(buildDir, { recursive: true })) {
    const source = path.join(buildDir, file);
    if (!fs.statSync(source).isFile() || !/\.(js|map)$/.test(file)) continue;
    const destination = path.join(outRoot, "build", file);
    fs.mkdirSync(path.dirname(destination), { recursive: true });
    if (file.endsWith(".map")) {
      // Map sources are relative to the temporary build directory. Store them
      // as placeholder-absolute paths so tests can relocate them.
      const map = JSON.parse(fs.readFileSync(source, "utf8"));
      map.sources = (map.sources ?? []).map((entry) => {
        if (/^[a-z]+:/.test(entry)) return entry;
        // The bundler may have computed the relative path from either the
        // symlinked or the real temporary directory.
        const absolute = [buildDir, realBuild]
          .map((base) =>
            path.resolve(path.dirname(path.join(base, file)), entry),
          )
          .find((candidate) => candidate.startsWith(projectRoot));
        return absolute
          ? PROJECT_PLACEHOLDER + absolute.slice(projectRoot.length)
          : entry;
      });
      fs.writeFileSync(destination, JSON.stringify(map));
    } else {
      fs.copyFileSync(source, destination);
    }
  }
  fs.rmSync(work, { recursive: true, force: true });
  console.log(`refreshed ${path.relative(repoRoot, outRoot)}`);
}
