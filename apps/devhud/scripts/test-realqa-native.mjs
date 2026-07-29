import { resolve } from "node:path";

import { run } from "./process.mjs";

const repositoryRoot = resolve(import.meta.dirname, "../../..");

await run(
  "cargo",
  [
    "test",
    "-p",
    "devhud",
    "--features",
    "linux-capture-backend",
    "realqa_capture",
  ],
  { cwd: repositoryRoot },
);
