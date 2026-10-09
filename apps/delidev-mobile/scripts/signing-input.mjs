// SPDX-License-Identifier: Apache-2.0
import { Identity } from "./beta.mjs";
export function validateProvisioning(value, team, now = Date.now()) {
  if (
    !/^[A-Fa-f0-9-]{36}$/.test(value.uuid) ||
    value.team !== team ||
    value.identity !== `${team}.${Identity}` ||
    value.getTaskAllow !== "false" ||
    value.betaReports !== "true" ||
    value.devices ||
    value.allDevices ||
    !Number.isFinite(Date.parse(value.expiration)) ||
    Date.parse(value.expiration) <= now
  )
    throw new Error("App Store provisioning identity or lifetime mismatch");
  return value.uuid;
}
