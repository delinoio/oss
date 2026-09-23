import { execFileSync } from "node:child_process";
import path from "node:path";
import { ensure, event, isMain, packageRoot, revision, root } from "./common.mjs";
import { stage } from "./github-release.mjs";

export function main() {
  ensure(process.argv.slice(2).every((arg) => arg === "--publish") && process.argv.length <= 3, "Unknown Homebrew argument");
  const candidate = stage(path.join(packageRoot, "dist"), path.join(packageRoot, "dist/github"), revision());
  const args = ["--project", "pnport", "--version", candidate.plan.version];
  for (const [suffix, flag] of [["darwin-x64", "darwin-amd64"], ["darwin-arm64", "darwin-arm64"], ["linux-x64-gnu", "linux-amd64"], ["linux-arm64-gnu", "linux-arm64"]]) {
    const name = `pnport-${suffix}.tar.gz`;
    const bytes = candidate.files.get(name);
    ensure(bytes, `Missing Homebrew archive ${name}`);
    const checksum = candidate.files.get("SHA256SUMS").toString("utf8").split("\n").find((line) => line.endsWith(`  ${name}`))?.slice(0, 64);
    ensure(/^[a-f0-9]{64}$/u.test(checksum ?? ""), "Invalid Homebrew archive checksum");
    args.push(`--${flag}-url`, `https://github.com/delinoio/oss/releases/download/${candidate.plan.tag}/${name}`, `--${flag}-sha256`, checksum);
  }
  if (process.argv.includes("--publish")) {
    ensure(process.env.GITHUB_REPOSITORY === "delinoio/oss" && process.env.GITHUB_REF === `refs/tags/${candidate.plan.tag}` && process.env.GITHUB_SHA === candidate.plan.revision && process.env.HOMEBREW_TAP_GH_TOKEN, "Homebrew publication requires exact tag and tap-only token");
  } else args.push("--dry-run");
  execFileSync("bash", [path.join(root, "scripts/release/update-homebrew.sh"), ...args], { cwd: root, stdio: "inherit" });
  event("homebrew", { tag: candidate.plan.tag, published: process.argv.includes("--publish") });
}

if (isMain(import.meta.url)) main();
