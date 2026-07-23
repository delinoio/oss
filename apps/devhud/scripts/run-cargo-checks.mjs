import { resolve } from "node:path";

import { run } from "./process.mjs";

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";

await run(cargo, ["fmt", "--all", "--", "--check"], {
  cwd: repositoryRoot,
});
await run(
  cargo,
  [
    "clippy",
    "-p",
    "devhud-probe",
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
await run(cargo, ["test", "-p", "devhud-probe", "--locked"], {
  cwd: repositoryRoot,
});
