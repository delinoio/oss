import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import test from "node:test";

import { waitForChildClose } from "./platform-smoke-child.mjs";

test("returns the exit code after a child closes", async () => {
  const child = spawn(process.execPath, ["-e", "process.exit(7)"], {
    stdio: "ignore",
    windowsHide: true,
  });

  assert.equal(await waitForChildClose(child, "normal", 5_000), 7);
});

test("waits for a timed-out child to close before rejecting", async () => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1_000)"], {
    stdio: "ignore",
    windowsHide: true,
  });
  await once(child, "spawn");
  let closed = false;
  child.once("close", () => {
    closed = true;
  });

  await assert.rejects(waitForChildClose(child, "hung", 50), /hung smoke timed out/u);
  assert.equal(closed, true);
});
