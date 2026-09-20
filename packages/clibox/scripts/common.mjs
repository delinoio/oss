import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const packageRoot = fileURLToPath(new URL("..", import.meta.url));
export const root = path.resolve(packageRoot, "../..");
export const registry = "https://registry.npmjs.org";
export const repository = "git+https://github.com/delinoio/oss.git";

export function ensure(condition, message) {
  if (!condition) throw new Error(message);
}

export function metadata(read = (file) => readFileSync(path.join(root, file), "utf8")) {
  const manifest = JSON.parse(read("packages/clibox/package.json"));
  const cargo = read("crates/clibox/Cargo.toml").match(/^\[package\]\s*\n([\s\S]*?)(?=^\[|$(?![\s\S]))/mu)?.[1];
  const version = cargo?.match(/^version = "([^"]+)"$/mu)?.[1];
  ensure(cargo?.includes('name = "clibox"\n') && manifest.name === "@delino/clibox", "Source package identity mismatch");
  ensure(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(version ?? "") && manifest.version === version, "Cargo/npm version mismatch or invalid version");
  return { version, description: manifest.description };
}

export function revision() {
  return execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
}

export function npm(args, options = {}) {
  // npm.cmd cannot be spawned without a shell on Windows. Use npm's Node entry
  // point in the standard Node installation, keeping all dynamic argv literal.
  const command = process.platform === "win32" ? process.execPath : "npm";
  const prefix = process.platform === "win32" ? [path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js")] : [];
  return execFileSync(command, [...prefix, ...args], { encoding: "utf8", stdio: "pipe", ...options });
}

export function event(action, details) {
  console.log(JSON.stringify({ event: `clibox_${action}`, ...details }));
}

export function isMain(url) {
  return process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(url);
}
