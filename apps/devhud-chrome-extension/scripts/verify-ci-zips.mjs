import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
assert.deepEqual(readFileSync("artifacts/devhud-chrome-web-store.zip"), readFileSync("artifacts/devhud-chrome-github-validation.zip"));
console.log("Chrome Web Store and GitHub validation ZIPs are identical");
