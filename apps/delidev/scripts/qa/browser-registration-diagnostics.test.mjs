// SPDX-License-Identifier: Apache-2.0
import assert, { AssertionError } from "node:assert/strict";
import test from "node:test";
import { RegistrationStep, registrationDiagnostics } from "./browser-registration-diagnostics.mjs";

class TimeoutError extends Error {}

test("records only known constructors and closed actions while preserving original rejection", async () => {
  for (const [error, errorClass] of [
    [new TimeoutError("private selector /private/path token"), "timeout"],
    [new AssertionError({ message: "private assertion", actual: "credential", expected: "native content" }), "assertion"],
    [Object.assign(new Error("private native content"), { name: "TimeoutError", code: "private-code" }), "unknown"],
  ]) {
    const diagnostics = registrationDiagnostics(TimeoutError);
    await assert.rejects(diagnostics.step(1, RegistrationStep.ObserveInspection, () => { throw error; }), rejected => rejected === error);
    assert.deepEqual(diagnostics.records(), [{ environment: 1, substage: "observe-inspection", errorClass }]);
    assert(!JSON.stringify(diagnostics.records()).includes("private"));
  }
});

test("keeps independently failed original pages ordered and retains their first failure", async () => {
  const diagnostics = registrationDiagnostics(TimeoutError);
  await Promise.allSettled([2, 1].map(environment => diagnostics.step(environment, RegistrationStep.FillPath, () => { throw new TimeoutError(); })));
  await assert.rejects(diagnostics.step(1, RegistrationStep.ObserveRow, () => { throw new Error(); }));
  const expected = [1, 2].map(environment => ({ environment, substage: "fill-path", errorClass: "timeout" }));
  assert.deepEqual(diagnostics.records(), expected);
  diagnostics.records()[0].substage = "private";
  assert.deepEqual(diagnostics.records(), expected);
});

test("passes through successful observations without failure evidence or replay", async () => {
  const diagnostics = registrationDiagnostics(TimeoutError);
  let calls = 0;
  assert.equal(await diagnostics.step(1, RegistrationStep.Inspect, () => { calls++; return 42; }), 42);
  assert.equal(calls, 1);
  assert.deepEqual(diagnostics.records(), []);
});

test("refuses arbitrary diagnostic values before invoking the action", async () => {
  const diagnostics = registrationDiagnostics(TimeoutError);
  for (const [environment, substage] of [[3, RegistrationStep.Save], [1, "/private/path"]]) {
    await assert.rejects(diagnostics.step(environment, substage, () => assert.fail("must not invoke")), TypeError);
  }
  assert.deepEqual(diagnostics.records(), []);
});
