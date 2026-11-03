// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { version } from "../../packages/ccow/scripts/package.mjs";

// Bind a release to main, the committed npm source version and an exact source
// revision before any build, packaging or publication step.
export function releaseIdentity(requested, revision, ref) {
  assert.equal(ref, "refs/heads/main", "Release must use main");
  assert.equal(
    requested,
    version,
    "Requested version must match the npm source version",
  );
  assert.match(revision, /^[0-9a-f]{40}$/u);
  assert(!/^0+$/u.test(revision));
  return { version, revision, tag: `ccow@v${version}` };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  console.log(JSON.stringify(releaseIdentity(...process.argv.slice(2))));
