// SPDX-License-Identifier: Apache-2.0
// Pure status checks: no server, Worker, native process or account is launched.
import assert from "node:assert/strict";
import test from "node:test";
import { verifyServerStatus } from "./environment.mjs";

const serverId = "01900000-0000-7000-8000-000000000001";
const original = { serverId, version: "0.1.0", protocolVersion: 2, stopping: false };
for (const failure of ["server-verification-failed", "paired-verification-failed"]) {
  test(`${failure} admits only the original current server`, () => {
    assert.doesNotThrow(() => verifyServerStatus(original, serverId, failure));
    for (const changed of [
      { serverId: "01900000-0000-7000-8000-000000000002" },
      { serverId: "" }, { version: "0.2.0" }, { version: "" },
      { protocolVersion: 0 }, { protocolVersion: 1 },
      { protocolVersion: 3 }, { protocolVersion: "2" },
      { stopping: true },
    ]) assert.throws(() => verifyServerStatus({ ...original, ...changed }, serverId, failure), error => error.code === failure);
  });
}
