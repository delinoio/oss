import { cp, mkdir } from "node:fs/promises";
import path from "node:path";

const publicDocsRoot = path.resolve(".");
const repositoryRoot = path.resolve(publicDocsRoot, "../..");
const publicRoot = path.join(publicDocsRoot, "docs", "public");

const assets = [
  ["scripts/install/nodeup.sh", "nodeup/install.sh"],
  ["scripts/install/nodeup.ps1", "nodeup/install.ps1"],
  ["scripts/install/binpm.sh", "binpm/install.sh"],
  ["scripts/install/binpm.ps1", "binpm/install.ps1"],
  ["scripts/install/async-commit-hook.sh", "async-commit-hook/install.sh"],
  ["scripts/install/async-commit-hook.ps1", "async-commit-hook/install.ps1"],
];

for (const [source, destination] of assets) {
  const sourcePath = path.join(repositoryRoot, source);
  const destinationPath = path.join(publicRoot, destination);
  await mkdir(path.dirname(destinationPath), { recursive: true });
  await cp(sourcePath, destinationPath);
}

console.log(`Prepared ${assets.length} public installer assets.`);
