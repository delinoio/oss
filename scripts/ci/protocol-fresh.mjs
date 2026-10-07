import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
const cwd = fileURLToPath(new URL("../../", import.meta.url));
function run(command, args) {
  const result = spawnSync(command === "node" ? process.execPath : command, args, { cwd, stdio: "inherit", shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
// Freshness must regenerate on every invocation, even when generation is cached.
// Invoke the pinned JS bin through Node because Windows exposes it as a .cmd shim.
run("node", ["node_modules/@bufbuild/buf/bin/buf", "generate"]);
const paths = ["protos/gen", "packages/devhud-api-client/src/gen", "packages/async-commit-hook-api-client/src/gen", "packages/delidev-api-client/src/gen"];
run("git", ["diff", "--exit-code", "--", ...paths]);
const untracked = spawnSync("git", ["ls-files", "--others", "--exclude-standard", "--", ...paths], { cwd, encoding: "utf8" });
if (untracked.error) throw untracked.error;
if (untracked.status !== 0 || untracked.stdout.trim()) throw new Error("Generated protocol files are untracked or could not be inspected");
