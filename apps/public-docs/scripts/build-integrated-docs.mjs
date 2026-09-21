import { cp, mkdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";

const publicDocsRoot = path.resolve(".");
const repositoryRoot = path.resolve(publicDocsRoot, "../..");
const outputDirectory = path.join(publicDocsRoot, "doc_build");
const projects = [
  ["runmoor-docs", "runmoor"],
  ["nodeup-docs", "nodeup"],
  ["binpm-docs", "binpm"],
  ["async-commit-hook-docs", "async-commit-hook"],
];

function run(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: repositoryRoot,
      stdio: "inherit",
      shell: process.platform === "win32",
    });
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(new Error(`${command} ${args.join(" ")} exited with ${signal ?? code}`));
    });
  });
}

await Promise.all(projects.map(([packageName]) => run("pnpm", ["--filter", packageName, "build"])));

for (const [packageName, slug] of projects) {
  const sourceDirectory = path.join(repositoryRoot, "apps", packageName, "doc_build");
  const destinationDirectory = path.join(outputDirectory, slug);
  await rm(destinationDirectory, { recursive: true, force: true });
  await mkdir(destinationDirectory, { recursive: true });
  await cp(sourceDirectory, destinationDirectory, { recursive: true });
}

// Cloudflare Pages reads redirects from the publication root. Keep the async
// compatibility route at the aggregate boundary instead of relying on a
// package-local `_redirects` file nested below a project subpath.
await writeFile(
  path.join(outputDirectory, "_redirects"),
  "/async-commit-hook/docs /async-commit-hook/docs/ 301\n",
);

console.log(`Integrated ${projects.length} documentation apps into ${path.relative(repositoryRoot, outputDirectory)}.`);
