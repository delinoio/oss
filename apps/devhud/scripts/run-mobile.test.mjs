import assert from "node:assert/strict";
import test from "node:test";

import { mobileCargoArguments } from "./run-mobile.mjs";

test("builds only contracted mobile commands and architectures", () => {
  assert.deepEqual(mobileCargoArguments(["ios", "build", "--target", "x86_64"]).slice(-4), ["ios", "build", "--target", "x86_64"]);
  assert.deepEqual(mobileCargoArguments(["android", "build", "--target", "armv7"]).slice(-4), ["android", "build", "--target", "armv7"]);
  assert.throws(() => mobileCargoArguments(["android", "build", "--target", "i686"]), /unsupported android target/u);
  assert.throws(() => mobileCargoArguments(["desktop", "build"]), /Usage/u);
});

test("does not permit callers to replace pinned platform configuration", () => {
  assert.throws(() => mobileCargoArguments(["ios", "build", "--config", "other.json"]), /overrides are not allowed/u);
});
