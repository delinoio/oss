import { parseArgs } from "node:util";
import assert from "node:assert/strict";
import { identity, verifyRelease } from "./package.mjs";
const { values } = parseArgs({
  options: {
    directory: { type: "string" },
    revision: { type: "string" },
    fixture: { type: "boolean", default: false },
  },
});
assert(
  values.directory && values.revision,
  "Usage: --directory <artifacts> --revision <expected-source-sha> [--fixture]",
);
console.log(
  JSON.stringify(
    verifyRelease(values.directory, identity(values.revision, values.fixture)),
  ),
);
