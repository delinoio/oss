import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

export function cacheContext(cwd = process.cwd()) {
  const version = (command, args) => {
    const result = spawnSync(command, args, { encoding: "utf8", shell: false });
    return result.status === 0 ? result.stdout.trim() : "unavailable";
  };
  const read = (path) => { try { return readFileSync(resolve(cwd, path), "utf8").trim(); } catch { return "unavailable"; } };
  return JSON.stringify({ os: process.platform, arch: process.arch, node: process.version,
    packageManager: (() => { try { return JSON.parse(read("package.json")).packageManager ?? "unavailable"; } catch { return "unavailable"; } })(),
    go: version("go", ["version"]), rust: version("rustc", ["--version"]), buf: version("buf", ["--version"]),
    nodePin: read(".nvmrc"), rustPin: read("rust-toolchain") });
}

export function cachePolicy(env = process.env) {
  if (!env.CI) return undefined;
  // PRs can only read. Forks and failed OIDC exchanges have no remote authority.
  if (!env.TURBO_TOKEN || !env.TURBO_TEAM || env.TURBO_REMOTE_CACHE_AUTH !== "true") return "local:rw";
  const main = env.GITHUB_REF === "refs/heads/main" && ["push", "workflow_dispatch"].includes(env.GITHUB_EVENT_NAME);
  return main ? "local:rw,remote:rw" : "local:rw,remote:r";
}
