// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { validateProvisioning } from "./signing-input.mjs";
const profile = {
  uuid: "12345678-1234-1234-1234-123456789012",
  team: "TEAM",
  identity: "TEAM.io.delino.delidev.mobile",
  getTaskAllow: "false",
  betaReports: "true",
  devices: false,
  allDevices: false,
  expiration: "2027-01-01T00:00:00Z",
};
test("signing fixture refuses expired, foreign, development and ad hoc profiles before keychain writes", () => {
  assert.equal(validateProvisioning(profile, "TEAM", 0), profile.uuid);
  for (const patch of [
    { expiration: "1970-01-01T00:00:00Z" },
    { expiration: "invalid" },
    { identity: "TEAM.foreign" },
    { getTaskAllow: "true" },
    { betaReports: "false" },
    { devices: true },
    { allDevices: true },
  ])
    assert.throws(() =>
      validateProvisioning(
        { ...profile, ...patch },
        "TEAM",
        Date.UTC(2026, 9, 9),
      ),
    );
});
