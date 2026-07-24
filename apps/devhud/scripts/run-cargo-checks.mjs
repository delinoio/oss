import { resolve } from "node:path";

import { run, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(import.meta.dirname, "../../..");
const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";

await runPackageManager(["run", "build"], { cwd: appRoot });
await run(cargo, ["fmt", "--all", "--", "--check"], {
  cwd: repositoryRoot,
});
await run(
  cargo,
  [
    "clippy",
    "-p",
    "devhud",
    "--all-targets",
    "--features",
    "desktop-cef",
    "--locked",
    "--",
    "-D",
    "warnings",
  ],
  { cwd: repositoryRoot },
);
await run(cargo, ["test", "-p", "devhud", "--locked"], {
  cwd: repositoryRoot,
});
