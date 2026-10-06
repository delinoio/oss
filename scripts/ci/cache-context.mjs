import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

export function cacheContext(cwd = process.cwd()) {
  const read = (path) => { try { return readFileSync(resolve(cwd, path), "utf8").trim(); } catch { return "unavailable"; } };
  return JSON.stringify({ os: process.platform, arch: process.arch, node: process.version,
    packageManager: (() => { try { return JSON.parse(read("package.json")).packageManager ?? "unavailable"; } catch { return "unavailable"; } })(),
    nodePin: read(".nvmrc") });
}

export function toolCacheContexts(cwd = process.cwd(), run = spawnSync) {
  const version = (command, args, env = process.env) => {
    const result = run(command, args, { encoding: "utf8", shell: false, env });
    return result.status === 0 ? result.stdout.trim() : "unavailable";
  };
  let rustPin;
  try { rustPin = readFileSync(resolve(cwd, "rust-toolchain"), "utf8").trim(); } catch { rustPin = "unavailable"; }
  // Metadata queries must never download compilers in JS-only jobs. rustup run
  // requires an installed toolchain unless --install is explicitly supplied.
  const rust = rustPin === "unavailable" ? "unavailable" : version("rustup", ["run", rustPin, "rustc", "--version"]);
  return {
    CI_GO_CACHE_CONTEXT: version("go", ["version"], { ...process.env, GOTOOLCHAIN: "local" }),
    CI_RUST_CACHE_CONTEXT: rust,
    CI_PROTO_CACHE_CONTEXT: version("buf", ["--version"]),
  };
}

export function cachePolicy(env = process.env) {
  if (!env.CI) return undefined;
  // PRs can only read. Forks and failed OIDC exchanges have no remote authority.
  if (!env.TURBO_TOKEN || !env.TURBO_TEAM || env.TURBO_REMOTE_CACHE_AUTH !== "true") return "local:rw";
  const main = env.GITHUB_REF === "refs/heads/main" && ["push", "workflow_dispatch"].includes(env.GITHUB_EVENT_NAME);
  return main ? "local:rw,remote:rw" : "local:rw,remote:r";
}
