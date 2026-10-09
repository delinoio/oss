// SPDX-License-Identifier: Apache-2.0
// Pure status checks: no server, Worker, native process or account is launched.
import assert from "node:assert/strict";
import test from "node:test";
import { Environment, verifyServerStatus } from "./environment.mjs";

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


test("health status observes the original protocol-2 transport without activating native controls", async () => {
  let response = original, reads = 0;
  const endpoint = "http://127.0.0.1:12345", token = "synthetic-in-memory";
  // A closed in-memory unary transport exercises Environment.status itself.
  // No generated client build, socket, server or native process is needed.
  const api = {
    SystemService: { methods: [{ localName: "getStatus", methodKind: "unary" }] },
    createDeliDevTransport(options) {
      assert.equal(options.origin, endpoint); assert.equal(options.getToken(), token);
      return { async unary(method, _signal, timeoutMs, _headers, input) {
        assert.equal(method.localName, "getStatus"); assert.equal(timeoutMs, 2000); assert.deepEqual(input, {});
        reads++; return { message: response };
      } };
    },
  };
  const environment = new Environment({ root: "/synthetic-qa", api });
  for (const action of ["launchServer", "startWorker", "registerWorker", "verifyWorker"]) environment[action] = () => assert.fail("status activated a native control");
  environment.processes.run = environment.processes.spawn = () => assert.fail("status launched a process");
  assert.deepEqual(await environment.status(), { state: "checking", attempts: 0, retry_ms: 0 }); assert.equal(reads, 0);
  environment.ownerTransport = {}; environment.endpoint = endpoint; environment.serverId = serverId;
  environment.clientCredential = { token }; environment.server = { child: { exitCode: null, signalCode: null } };
  assert.deepEqual(await environment.status(), { state: "ready", attempts: 0, retry_ms: 0 });
  for (const changed of [{ protocolVersion: 1 }, { protocolVersion: 3 }, { serverId: "foreign" }, { stopping: true }]) {
    response = { ...original, ...changed };
    assert.deepEqual(await environment.status(), { state: "blocked", attempts: 0, retry_ms: 0, failure: "qa-connection-unavailable" });
  }
  assert.equal(reads, 5); assert.equal(environment.phase, "preparing"); assert.equal(environment.closing, false);
});
