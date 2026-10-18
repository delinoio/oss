// SPDX-License-Identifier: Apache-2.0
// Build-time notice assembly. This never runs in the desktop application.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../..", import.meta.url));
const output = path.join(root, "apps/fixkey/src-tauri/gen/licenses");
rmSync(output, { recursive: true, force: true });
mkdirSync(output, { recursive: true });
const standardSources = JSON.parse(
  readFileSync(
    path.join(root, "apps/fixkey/src-tauri/licenses/sources.json"),
    "utf8",
  ),
);
for (const [name, record] of Object.entries(standardSources).filter(
  ([name]) => name !== "schemaVersion",
)) {
  const bytes = readFileSync(
    path.join(root, `apps/fixkey/src-tauri/licenses/${name}.txt`),
  );
  if (createHash("sha256").update(bytes).digest("hex") !== record.sha256)
    throw new Error("fixkey-standard-license-checksum-mismatch");
}
const target =
  process.env.TAURI_ENV_TARGET_TRIPLE ||
  execFileSync("rustc", ["-vV"], { encoding: "utf8" }).match(
    /^host: (.+)$/mu,
  )?.[1];
if (!target) throw new Error("fixkey-license-target-unavailable");
const metadata = JSON.parse(
  execFileSync(
    "cargo",
    [
      "metadata",
      "--locked",
      "--format-version",
      "1",
      "--features",
      "fixkey/desktop",
      "--filter-platform",
      target,
    ],
    { cwd: root, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 },
  ),
);
const packages = new Map(metadata.packages.map((pkg) => [pkg.id, pkg]));
const graph = new Map(metadata.resolve.nodes.map((node) => [node.id, node]));
const start = metadata.packages.find(
  (pkg) => pkg.name === "fixkey" && pkg.source === null,
);
if (!start) throw new Error("fixkey-license-owner-missing");
const lockEntries = readFileSync(path.join(root, "Cargo.lock"), "utf8")
  .split("[[package]]")
  .map((block) =>
    Object.fromEntries(
      [
        ...block.matchAll(/^(name|version|source|checksum) = "([^"]+)"$/gmu),
      ].map((match) => [match[1], match[2]]),
    ),
  );
function registryChecksum(pkg) {
  const matches = lockEntries.filter(
    (entry) =>
      entry.name === pkg.name &&
      entry.version === pkg.version &&
      entry.source === pkg.source,
  );
  if (
    matches.length !== 1 ||
    !/^[a-f0-9]{64}$/u.test(matches[0].checksum ?? "")
  )
    throw new Error("fixkey-license-source-checksum-missing");
  return matches[0].checksum;
}
const pending = [start.id],
  seen = new Set(),
  inventory = [];
function standardLicense(pkg, selected) {
  return (
    pkg?.license &&
    !/\bAND\b/u.test(pkg.license) &&
    pkg.license
      .replace(/[()]/gu, "")
      .split(/\s+OR\s+|\//u)
      .map((value) => value.trim())
      .includes(selected)
  );
}
function notices(directory, explicit, owner, pkg) {
  let candidates = readdirSync(directory, { withFileTypes: true })
    .filter(
      (entry) =>
        entry.isFile() &&
        /^(unlicen[cs]e|licen[cs]e|copying|notice|copyright)([._-]|$)/iu.test(
          entry.name,
        ),
    )
    .map((entry) => path.join(directory, entry.name));
  if (explicit) candidates.push(path.resolve(directory, explicit));
  if (!candidates.length && owner)
    candidates = [path.join(root, "LICENSE"), path.join(root, "NOTICE")];
  // Some registry packages declare a standard SPDX license but omit its
  // text (notably generated objc2 frameworks). Keep their declared license,
  // author attribution in the inventory and reproduce the
  // pinned standard text. Never substitute the repository license for MIT.
  if (!candidates.length && standardLicense(pkg, "MIT"))
    candidates = [path.join(root, "apps/fixkey/src-tauri/licenses/MIT.txt")];
  if (!candidates.length && standardLicense(pkg, "Apache-2.0"))
    candidates = [path.join(root, "LICENSE")];
  if (!candidates.length && pkg?.license === "MPL-2.0")
    candidates = [
      path.join(root, "apps/fixkey/src-tauri/licenses/MPL-2.0.txt"),
    ];
  if (!candidates.length)
    throw new Error("fixkey-dependency-license-text-missing");
  return [...new Set(candidates)].map((file) => {
    const bytes = readFileSync(file);
    const name = `${createHash("sha256").update(bytes).digest("hex")}.txt`;
    writeFileSync(path.join(output, name), bytes);
    return name;
  });
}
while (pending.length) {
  const id = pending.pop();
  if (seen.has(id)) continue;
  seen.add(id);
  const pkg = packages.get(id),
    node = graph.get(id);
  if (!pkg || !node) throw new Error("fixkey-license-graph-incomplete");
  const directory = path.dirname(pkg.manifest_path);
  let source = directory;
  // Git crates may inherit license files from their repository root. Registry
  // crates must supply their own packaged license text; never borrow a sibling.
  if (
    pkg.source?.startsWith("git+") &&
    !readdirSync(source).some((name) => /^licen[cs]e([._-]|$)/iu.test(name))
  ) {
    for (let level = 0; level < 5; level++) {
      source = path.dirname(source);
      if (
        readdirSync(source).some((name) => /^licen[cs]e([._-]|$)/iu.test(name))
      )
        break;
    }
  }
  if (pkg.license?.includes("MPL-2.0") && pkg.source?.startsWith("registry+")) {
    const original = path.join(
      path.dirname(path.dirname(path.dirname(directory))),
      "cache",
      path.basename(path.dirname(directory)),
      `${pkg.name}-${pkg.version}.crate`,
    );
    const sourceBytes = readFileSync(original),
      name = `source-${pkg.name}-${pkg.version}.crate`;
    const expected = registryChecksum(pkg);
    if (createHash("sha256").update(sourceBytes).digest("hex") !== expected)
      throw new Error("fixkey-license-source-checksum-mismatch");
    writeFileSync(path.join(output, name), sourceBytes);
  }
  inventory.push({
    name: pkg.name,
    version: pkg.version,
    license: pkg.license,
    authors: pkg.authors,
    source: pkg.source ?? "repository-owned",
    sourceArchive: pkg.source?.startsWith("registry+")
      ? `https://static.crates.io/crates/${pkg.name}/${pkg.name}-${pkg.version}.crate`
      : undefined,
    notices: (() => {
      try {
        return notices(
          source,
          pkg.license_file ? path.resolve(directory, pkg.license_file) : null,
          pkg.source === null,
          pkg,
        );
      } catch (error) {
        throw new Error(`${error.message}: ${pkg.name}@${pkg.version}`);
      }
    })(),
  });
  for (const dependency of node.deps)
    if (dependency.dep_kinds.some((kind) => kind.kind !== "dev"))
      pending.push(dependency.pkg);
}
const nodeSeen = new Set();
function nodePackage(file) {
  const directory = path.dirname(file);
  if (nodeSeen.has(directory)) return;
  nodeSeen.add(directory);
  const pkg = JSON.parse(readFileSync(file, "utf8"));
  inventory.push({
    name: pkg.name,
    version: pkg.version,
    license: pkg.license,
    source: `https://www.npmjs.com/package/${pkg.name}/v/${pkg.version}`,
    notices: notices(directory, null, false),
  });
  const require = createRequire(file);
  for (const name of Object.keys(pkg.dependencies ?? {})) {
    let manifest;
    try {
      manifest = require.resolve(`${name}/package.json`);
    } catch {
      let entry = path.dirname(require.resolve(name));
      while (!existsSync(path.join(entry, "package.json"))) {
        const parent = path.dirname(entry);
        if (parent === entry)
          throw new Error("fixkey-js-license-owner-missing");
        entry = parent;
      }
      manifest = path.join(entry, "package.json");
    }
    nodePackage(manifest);
  }
}
const appManifest = path.join(root, "apps/fixkey/package.json"),
  app = JSON.parse(readFileSync(appManifest, "utf8")),
  require = createRequire(appManifest);
for (const name of Object.keys(app.dependencies))
  nodePackage(require.resolve(`${name}/package.json`));
inventory.sort((a, b) =>
  `${a.name}@${a.version}`.localeCompare(`${b.name}@${b.version}`),
);
writeFileSync(
  path.join(output, "inventory.json"),
  JSON.stringify(
    { schemaVersion: 1, target, dependencies: inventory },
    null,
    2,
  ) + "\n",
);
console.log(
  JSON.stringify({
    component: "fixkey.licenses",
    target,
    dependencies: inventory.length,
    result: "passed",
  }),
);
