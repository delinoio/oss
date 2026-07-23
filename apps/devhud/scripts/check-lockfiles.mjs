import { resolve } from "node:path";

import { run, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";

await runPackageManager(
  [
    "--dir",
    repositoryRoot,
    "install",
    "--frozen-lockfile",
    "--ignore-scripts",
  ],
  { cwd: repositoryRoot, stdio: "ignore" },
);
await run(
  cargo,
  [
    "metadata",
    "--manifest-path",
    resolve(appRoot, "src-tauri/Cargo.toml"),
    "--locked",
    "--format-version",
    "1",
  ],
  { cwd: repositoryRoot, stdio: "ignore" },
);

console.log(
  JSON.stringify({
    check: "devhud-lockfiles",
    status: "passed",
  }),
);
