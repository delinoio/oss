import { readFile } from "node:fs/promises";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dirname, "..");
const appNames = [
  "binpm-docs",
  "delidev-app",
  "devhud",
  "mpapp",
  "nodeup-docs",
  "public-docs",
];

for (const appName of appNames) {
  const packagePath = path.join(repoRoot, "apps", appName, "package.json");
  const packageJson = JSON.parse(await readFile(packagePath, "utf8"));
  if (packageJson.scripts?.dev === undefined) {
    throw new Error(`Root development contract: apps/${appName} must define a dev script`);
  }
}

const rootPackage = JSON.parse(await readFile(path.join(repoRoot, "package.json"), "utf8"));
if (rootPackage.scripts?.predev !== "./scripts/generate-proto.sh") {
  throw new Error("Root development contract: predev must generate protos");
}
if (rootPackage.scripts?.dev !== "./scripts/dev.sh") {
  throw new Error("Root development contract: dev must delegate to scripts/dev.sh");
}

const devScript = await readFile(path.join(repoRoot, "scripts/dev.sh"), "utf8");
for (const requiredText of [
  'DERUN_STATE_ROOT="$repo_root/.derun-state"',
  'GOMODCACHE="$repo_root/.gomodcache"',
  'go -C "$repo_root" run ./cmds/derun run -- turbo dev "$@"',
]) {
  if (!devScript.includes(requiredText)) {
    throw new Error(`Root development contract: scripts/dev.sh is missing ${requiredText}`);
  }
}

console.log(`Development contract covers ${appNames.length} app workspaces: ${appNames.join(", ")}`);
