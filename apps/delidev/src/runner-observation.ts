// SPDX-License-Identifier: Apache-2.0
import { EntityKind, isEntityId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object } from "./documents";

/** Closed presentation evidence only. It never grants execution authority. */
export type RunnerExclusion = "unavailable" | "disabled" | "capability" | "missing" | "duplicate" | "unchecked" | "permission" | "installationFailed" | "version" | "protocol";
export interface RunnerObservation { cause?: RunnerExclusion; detectedVersion?: string }
export function validRunnerObservation(resource?: Resource): resource is Resource {
  return Boolean(resource && resource.kind === EntityKind.MACHINE && isEntityId(resource.id) && resource.revision > 0n && supportsResourceSchema(resource) && resource.documentJson.byteLength <= 1 << 20 && Object.keys(document(resource)).length);
}
const version = (value: unknown): value is string => typeof value === "string" && value.length <= 256 && /^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]{1,64})?(?:\+[a-zA-Z0-9.-]{1,64})?$/.test(value);
export function claudeRunnerObservation(resource?: Resource): RunnerObservation {
  if (!validRunnerObservation(resource)) return { cause: "unavailable" };
  const data = document(resource);
  if (typeof data.disabled !== "boolean" || !Array.isArray(data.worker_capabilities) || !data.worker_capabilities.every(value => typeof value === "string") || !Array.isArray(data.installations)) return { cause: "unavailable" };
  if (data.disabled) return { cause: "disabled" };
  if (!data.worker_capabilities.includes("native-claude-subscriptions-v1")) return { cause: "capability" };
  const installations = items(data.installations).map(object).filter(row => row.harness === "claude-code");
  if (!installations.length) return { cause: "missing" };
  if (installations.length !== 1) return { cause: "duplicate" };
  return installationObservation(installations[0], "2.1.236");
}
export function installationObservation(installation: Record<string, unknown>, requiredVersion?: string): RunnerObservation {
  switch (installation.state) {
    case "missing": return { cause: "missing" };
    case "permission-denied": return { cause: "permission" };
    case "failed": return { cause: "installationFailed" };
    case "unchecked": return { cause: "unchecked" };
    case "incompatible": return { cause: "version" };
    case "detected": break;
    default: return { cause: "unavailable" };
  }
  if (!version(installation.version)) return { cause: "unavailable" };
  const detectedVersion = installation.version;
  if (requiredVersion && detectedVersion !== requiredVersion) return { cause: "version", detectedVersion };
  if (installation.problem) return { cause: "unavailable", detectedVersion };
  const protocol = object(installation.protocol);
  if (installation.protocol_verified !== true || protocol.state !== "verified" || protocol.problem) return { cause: "protocol", detectedVersion };
  return { detectedVersion };
}
