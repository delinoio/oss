import { copyFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const appDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = path.resolve(appDirectory, "../..");
const outputDirectory = path.join(appDirectory, "doc_build");
const installers = [
  ["scripts/install/nodeup.sh", "install.sh"],
  ["scripts/install/nodeup.ps1", "install.ps1"],
];

await mkdir(outputDirectory, { recursive: true });
for (const [source, destination] of installers) {
  const sourceFile = path.join(repositoryRoot, source);
  await copyFile(sourceFile, path.join(outputDirectory, destination));
  console.log(`nodeup-docs: copied ${source} to ${path.relative(appDirectory, path.join(outputDirectory, destination))}`);
}
