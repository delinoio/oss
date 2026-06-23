import { copyFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(appDir, "../..");
const outputDir = path.join(appDir, "doc_build");

const installers = [
  {
    source: path.join(repoRoot, "scripts/install/binpm.sh"),
    destination: path.join(outputDir, "install.sh"),
  },
  {
    source: path.join(repoRoot, "scripts/install/binpm.ps1"),
    destination: path.join(outputDir, "install.ps1"),
  },
];

await mkdir(outputDir, { recursive: true });

for (const { source, destination } of installers) {
  await copyFile(source, destination);
  console.log(
    `binpm-docs: copied ${path.relative(repoRoot, source)} to ${path.relative(
      appDir,
      destination,
    )}`,
  );
}
