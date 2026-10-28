const test = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const {
  artifact,
  verify,
} = require("../../packaging/tairouter/npm/install.cjs");
test("all official targets resolve; Windows ARM is rejected", () => {
  for (const platform of ["linux", "darwin"])
    for (const arch of ["x64", "arm64"])
      assert.match(artifact(platform, arch), /^tairouter-/);
  assert.equal(artifact("win32", "x64"), "tairouter-windows-amd64.exe");
  assert.throws(() => artifact("win32", "arm64"), /Unsupported/);
});
test("modified and missing digests fail closed", () => {
  const bytes = Buffer.from("synthetic binary");
  const digest = crypto.createHash("sha256").update(bytes).digest("hex");
  verify(bytes, digest);
  assert.throws(() => verify(Buffer.from("tampered"), digest), /mismatch/);
  assert.throws(() => verify(bytes, undefined), /Missing/);
});
