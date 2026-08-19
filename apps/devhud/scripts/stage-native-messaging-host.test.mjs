import assert from "node:assert/strict";
import test from "node:test";

import { rustHostTriple } from "./stage-native-messaging-host.mjs";

test("extracts only a bounded Rust host triple", () => {
  assert.equal(rustHostTriple("rustc 1.90.0\nhost: aarch64-apple-darwin\n"), "aarch64-apple-darwin");
  assert.throws(() => rustHostTriple("rustc 1.90.0\n"), /determine/u);
});
