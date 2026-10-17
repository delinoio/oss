import assert from "node:assert/strict";
import test from "node:test";
import { isNativeExecutable, Target } from "../dist/core.js";
import { nativeHeader } from "./native-header.mjs";

for (const target of [Target.MacOS, Target.Linux, Target.Windows]) {
  test(`artifact architecture checks reject wrong and truncated ${target} headers`, () => {
    const header = nativeHeader(target);
    assert.equal(isNativeExecutable(header, target), true);
    for (let length = 0; length < header.length; length++)
      assert.equal(
        isNativeExecutable(header.subarray(0, length), target),
        false,
      );
    const wrong = Buffer.from(header);
    if (target === Target.MacOS) wrong.writeUInt32LE(0x01000007, 4);
    if (target === Target.Linux) wrong.writeUInt16LE(183, 18);
    if (target === Target.Windows) wrong.writeUInt16LE(0xaa64, 132);
    assert.equal(isNativeExecutable(wrong, target), false);
  });
}
test("reject wrong ELF encoding and invalid PE header offsets or signatures", () => {
  for (const [offset, value] of [
    [4, 1],
    [5, 2],
    [6, 0],
  ]) {
    const bytes = nativeHeader(Target.Linux);
    bytes[offset] = value;
    assert.equal(isNativeExecutable(bytes, Target.Linux), false);
  }
  for (const offset of [0, 63, 153, 0xffffffff]) {
    const bytes = nativeHeader(Target.Windows);
    bytes.writeUInt32LE(offset, 0x3c);
    assert.equal(isNativeExecutable(bytes, Target.Windows), false);
  }
  for (const offset of [128, 152]) {
    const bytes = nativeHeader(Target.Windows);
    bytes[offset] = 0;
    assert.equal(isNativeExecutable(bytes, Target.Windows), false);
  }
});
