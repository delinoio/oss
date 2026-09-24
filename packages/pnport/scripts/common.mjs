import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const packageRoot = fileURLToPath(new URL("..", import.meta.url));
export const root = path.resolve(packageRoot, "../..");
export const registry = "https://registry.npmjs.org";

export function ensure(condition, message) {
  if (!condition) throw new Error(message);
}

export function sourceText(file) {
  return readFileSync(path.join(root, file), "utf8").replaceAll("\r\n", "\n");
}

export function metadata(read = sourceText) {
  const npm = JSON.parse(read("packages/pnport/package.json"));
  const versions = ["pnport", "pnport-core", "pnport-preload"].map((name) => {
    const section = read(`crates/${name}/Cargo.toml`).match(/^\[package\]\s*\n([\s\S]*?)(?=^\[|$(?![\s\S]))/mu)?.[1];
    ensure(section?.includes(`name = "${name}"\n`), `Missing ${name} Cargo identity`);
    return section.match(/^version = "([^"]+)"$/mu)?.[1];
  });
  ensure(npm.name === "@delino/pnport" && versions.every((value) => value === npm.version) && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(npm.version), "pnport Cargo/npm version mismatch");
  return { version: npm.version };
}

export function revision() {
  return execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
}

export function npm(args, options = {}) {
  const command = process.platform === "win32" ? process.execPath : "npm";
  const prefix = process.platform === "win32" ? [path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js")] : [];
  return execFileSync(command, [...prefix, ...args], { encoding: "utf8", stdio: "pipe", ...options });
}

export function event(action, details) {
  console.log(JSON.stringify({ event: `pnport_${action}`, ...details }));
}

export function isMain(url) {
  return process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(url);
}
