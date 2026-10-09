// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import test from "node:test";
import { terminatePosixProcessGroup } from "../../../../scripts/spawn-dev-server.mjs";

// Simulate only the Linux proc observation boundary. Never signal a real PID
// or read host process state; restore the built-in bindings after each case.
async function observeProc(mock, code) {
  const platform = Object.getOwnPropertyDescriptor(process, "platform");
  const signals = [];
  Object.defineProperty(process, "platform", { ...platform, value: "linux" });
  mock.method(process, "kill", (pid, signal) => {
    assert.equal(pid, -123);
    signals.push(signal);
    return true;
  });
  mock.method(fs, "readdirSync", path => {
    assert.equal(path, "/proc");
    return ["11", "12"];
  });
  mock.method(fs, "readFileSync", path => {
    if (path === "/proc/11/stat") throw Object.assign(new Error("Synthetic proc failure"), { code });
    assert.equal(path, "/proc/12/stat");
    return "12 (retained zombie) Z 1 123 123";
  });
  syncBuiltinESMExports();
  try {
    await terminatePosixProcessGroup({ pid: 123 }, "SIGTERM");
    return signals;
  } finally {
    mock.restoreAll();
    syncBuiltinESMExports();
    Object.defineProperty(process, "platform", platform);
  }
}

for (const code of ["ENOENT", "ESRCH"]) {
  test(`joins the original group when a proc stat task disappears with ${code}`, async ({ mock }) => {
    assert.deepEqual(await observeProc(mock, code), ["SIGTERM", 0]);
  });
}
test("keeps proc access denial uncertain without escalating or adopting another group", async ({ mock }) => {
  await assert.rejects(observeProc(mock, "EACCES"), { code: "EACCES" });
});
