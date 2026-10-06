import { spawnSync } from "node:child_process";
import { realpathSync } from "node:fs";
import * as nativePaths from "node:path";
import { dirname } from "node:path";

export const GoMode = Object.freeze({ Full: "full", Affected: "affected" });
const modulePath = "github.com/delinoio/oss";
const within = (value, prefix) => value === prefix || value.startsWith(`${prefix}/`);
const shared = new Set(["go.mod", "go.sum", "go.work", "go.work.sum", ".gitattributes", ".npmrc", ".nvmrc", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "turbo.json"]);

// These tests compile a command in a subprocess, so Go's import graph alone
// cannot describe their dependency on that command's entire source graph.
// Keep this inventory synchronized with command-building integration fixtures.
export const commandConsumers = Object.freeze([
  { command: "cmds/delidev-cli", consumers: ["cmds/delidev-cli/internal/cli", "cmds/delidev-cli/internal/server"] },
  { command: "cmds/async-commit-hook", consumers: ["cmds/async-commit-hook/integration"] },
  { command: "cmds/runmoor", consumers: ["cmds/runmoor/internal/runmoor"] },
]);

// These frontend sources become real Go embed bytes before discovery. Their
// ownership is outside the Go directory tree and must remain an explicit edge.
const embeddedSources = [
  { inputs: ["apps/devhud-admin", "packages/devhud-api-client"], owner: "servers/devhud-api/internal/adminassets" },
  { inputs: ["apps/async-commit-hook", "packages/async-commit-hook-api-client"], owner: "cmds/async-commit-hook/internal/webassets" },
];

export function parseInventory(output, root, { paths = nativePaths, canonicalize = paths.resolve } = {}) {
  const checkout = canonicalize(root);
  const records = [];
  let start = 0, depth = 0, quoted = false, escaped = false;
  for (let index = 0; index < output.length; index++) {
    const char = output[index];
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') quoted = false;
    } else if (char === '"') {
      if (depth === 0) throw new Error("Unexpected Go inventory output");
      quoted = true;
    }
    else if (char === "{") { if (depth++ === 0) start = index; }
    else if (char === "}") {
      if (--depth < 0) throw new Error("Invalid Go inventory JSON");
      if (depth === 0) records.push(JSON.parse(output.slice(start, index + 1)));
    } else if (depth === 0 && !/\s/u.test(char)) throw new Error("Unexpected Go inventory output");
  }
  if (depth !== 0 || quoted || records.length === 0) throw new Error("Empty or incomplete Go inventory");
  const seen = new Set();
  const inventory = [];
  for (const record of records) {
    if (typeof record.ImportPath !== "string" || !within(record.ImportPath, modulePath) || typeof record.Dir !== "string" || record.Error || record.DepsErrors?.length || (record.ForTest !== undefined && typeof record.ForTest !== "string")) throw new Error("Invalid Go package discovery");
    if (!paths.isAbsolute(record.Dir)) throw new Error("Go package directory must be absolute");
    const nativeRelative = paths.relative(checkout, canonicalize(record.Dir));
    const directory = nativeRelative.replaceAll("\\", "/") || ".";
    if (paths.isAbsolute(nativeRelative) || directory.startsWith("../") || directory === "..") throw new Error("Go package is outside the checkout");
    // -test resolves test embed files on original records, but also emits test
    // binaries and rewritten packages. Those records must never own CI shards
    // or turn test-only embed bytes into production dependency seeds.
    if (record.ForTest || record.ImportPath.endsWith(".test")) continue;
    if (/\s/u.test(record.ImportPath) || seen.has(record.ImportPath)) throw new Error("Invalid Go package discovery");
    seen.add(record.ImportPath);
    const imports = ["Imports", "TestImports", "XTestImports"].flatMap((key) => {
      const values = record[key] ?? [];
      if (!Array.isArray(values) || values.some((value) => typeof value !== "string")) throw new Error("Invalid Go import inventory");
      return values;
    });
    const embeds = (key) => {
      const values = record[key] ?? [];
      if (!Array.isArray(values) || values.some((value) => typeof value !== "string")) throw new Error("Invalid Go embed inventory");
      return [...new Set(values.map((value) => {
        const file = value.replaceAll("\\", "/");
        if (paths.isAbsolute(value) || /^[A-Za-z]:/u.test(file) || file.split("/").some((part) => !part || part === "." || part === "..")) throw new Error("Go embed file must be package-relative");
        return directory === "." ? file : `${directory}/${file}`;
      }))];
    };
    inventory.push({ path: record.ImportPath, directory, imports: [...new Set(imports)], embedFiles: embeds("EmbedFiles"), testEmbedFiles: embeds("TestEmbedFiles"), xTestEmbedFiles: embeds("XTestEmbedFiles") });
  }
  if (inventory.length === 0) throw new Error("Empty Go package inventory");
  return inventory;
}

function domain(path) {
  const parts = path.split("/");
  if (["cmds", "servers"].includes(parts[0]) && parts.length > 1) return parts.slice(0, 2).join("/");
  if (parts[0] === "protos" && parts[1] === "gen" && parts[2] === "go" && parts.length > 3) return parts.slice(0, 4).join("/");
  return null;
}

export function selectAffected(inventory, changes) {
  if (inventory.length === 0) throw new Error("Empty Go inventory");
  const all = inventory.map((item) => item.path).sort();
  const reasons = [];
  const source = new Set(), tests = new Set();
  const selectDomain = (path) => {
    const owner = domain(path);
    const candidates = owner ? inventory.filter((item) => within(item.directory, owner)) : inventory;
    // A removed/new domain with no current owner cannot safely narrow callers.
    for (const item of candidates.length ? candidates : inventory) source.add(item.path);
    reasons.push({ path, reason: owner && candidates.length ? "domain-fallback" : "full-fallback" });
  };
  for (const { status, path } of changes) {
    if (shared.has(path) || path.startsWith("scripts/ci/go-") || path.startsWith(".github/")) return { packages: all, reasons: [{ path, reason: "shared-input" }] };
    for (const edge of embeddedSources) {
      if (edge.inputs.some((input) => within(path, input))) {
        const target = inventory.find((item) => item.directory === edge.owner);
        if (!target) throw new Error("Missing generated Go embed owner");
        source.add(target.path);
      }
    }
    let embedded = false;
    for (const item of inventory) {
      if (item.embedFiles?.includes(path)) { source.add(item.path); embedded = true; }
      if (item.testEmbedFiles?.includes(path) || item.xTestEmbedFiles?.includes(path)) { tests.add(item.path); embedded = true; }
    }
    const owner = inventory.filter((item) => item.directory === dirname(path).replaceAll("\\", "/") || within(path, `${item.directory}/testdata`)).sort((a, b) => b.directory.length - a.directory.length)[0];
    if (status === "D" || !["A", "M"].includes(status)) { if (path.endsWith(".go") || domain(path)) selectDomain(path); continue; }
    if (owner && (path.endsWith("_test.go") || within(path, `${owner.directory}/testdata`))) tests.add(owner.path);
    else if (owner && path.endsWith(".go")) source.add(owner.path);
    else if (!embedded && (path.endsWith(".go") || domain(path))) selectDomain(path);
  }
  let changed = true;
  while (changed) {
    changed = false;
    for (const item of inventory) {
      if (!source.has(item.path) && item.imports.some((dependency) => source.has(dependency))) { source.add(item.path); changed = true; }
    }
    for (const { command, consumers } of commandConsumers) {
      if (!source.has(`${modulePath}/${command}`)) continue;
      for (const item of inventory.filter((item) => consumers.includes(item.directory))) {
        if (!source.has(item.path)) { source.add(item.path); changed = true; }
      }
    }
  }
  return { packages: [...new Set([...source, ...tests])].sort(), reasons };
}

function execute(run, command, args, cwd) {
  const result = run(command, args, { cwd, shell: false, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 64 * 1024 * 1024 });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} discovery failed (${result.status ?? result.signal ?? "unknown"})`);
  return result.stdout;
}

export function affectedGoPackages({ base, head, cwd = process.cwd(), run = spawnSync, log = console.log } = {}) {
  for (const value of [base, head]) {
    if (!/^[0-9a-f]{40}$/u.test(value ?? "") || /^0+$/u.test(value)) throw new Error("Go comparison requires two nonzero 40-character commit SHAs");
    execute(run, "git", ["cat-file", "-e", `${value}^{commit}`], cwd);
  }
  const root = execute(run, "git", ["rev-parse", "--show-toplevel"], cwd).trim();
  if (!root) throw new Error("Missing Go checkout root");
  const fields = execute(run, "git", ["diff", "--name-status", "--no-renames", "-z", base, head, "--"], cwd).split("\0");
  if (fields.pop() !== "" || fields.length % 2) throw new Error("Invalid Go change inventory");
  const changes = [];
  for (let index = 0; index < fields.length; index += 2) {
    if (!/^[AMDTU]$/u.test(fields[index]) || !fields[index + 1]) throw new Error("Invalid Go change record");
    changes.push({ status: fields[index], path: fields[index + 1] });
  }
  // Git and Go can report different drive casing or long/8.3 spellings for the
  // same Windows directory. Compare native filesystem identities, then reject
  // parent traversal and different-drive absolute results from path.relative.
  const inventory = parseInventory(execute(run, "go", ["list", "-mod=readonly", "-test", "-json", "./..."], cwd), root, { canonicalize: realpathSync.native });
  const selection = selectAffected(inventory, changes);
  log(JSON.stringify({ event: "ci_go_affected", base, head, packageCount: selection.packages.length, packages: selection.packages, reasons: selection.reasons }));
  return { ...selection, inventory, root };
}

export function selectionOptions(env = process.env) {
  const mode = env.CI_GO_MODE ?? GoMode.Full;
  if (!Object.values(GoMode).includes(mode)) throw new Error("Unknown Go validation mode");
  return { mode, base: env.CI_GO_BASE, head: env.CI_GO_HEAD };
}
