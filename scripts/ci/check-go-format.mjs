import { execFileSync, spawnSync } from "node:child_process";

const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
const scopes = process.argv.slice(2);
if (scopes.length === 0) throw new Error("at least one repository-relative Go scope is required");

const tracked = execFileSync("git", ["ls-files", "--", ...scopes], { cwd: root, encoding: "utf8" })
  .split("\n")
  .filter((path) => path.endsWith(".go"));
if (tracked.length === 0) throw new Error(`no tracked Go files found under ${scopes.join(", ")}`);

const result = spawnSync("gofmt", ["-l", ...tracked], { cwd: root, encoding: "utf8" });
if (result.error) throw result.error;
if (result.status !== 0) throw new Error(result.stderr.trim() || "gofmt failed");
if (result.stdout.trim() !== "") {
  process.stderr.write(`${result.stdout.trim()}\n`);
  throw new Error("Go files are not formatted; run gofmt on the paths above");
}
process.stderr.write(`[ci.go-format] verified ${tracked.length} tracked Go files\n`);
