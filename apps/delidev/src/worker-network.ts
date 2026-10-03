// SPDX-License-Identifier: Apache-2.0
import type { PairingAuthority } from "./pairing-grant";
import { object } from "./documents";
export enum ProxyMode { Direct = "direct", Http = "http", Https = "https", Socks5 = "socks5" }
export enum NativeRouteState { Unsupported = "unsupported", NotApplied = "not-applied", Unverified = "unverified", Observed = "observed", Failed = "failed", Stale = "stale" }
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const generation = (value: unknown): value is string => typeof value === "string" && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) < 1n << 63n;
const only = (value: Record<string, unknown>, keys: readonly string[]) => Object.keys(value).every(key => keys.includes(key));
export interface WorkerRecipient { version: 1; authority: { server_id: string; endpoint: string; machine_id: string; device_id: string; pairing_id: string }; key_id: string; recipient: string }
export function workerRecipient(value: unknown, selected: PairingAuthority, machine?: string): WorkerRecipient | undefined {
  const row = object(value), authority = object(row.authority);
  if (!only(row, ["version", "authority", "key_id", "recipient"]) || !only(authority, ["server_id", "endpoint", "machine_id", "device_id", "pairing_id"]) || row.version !== 1 || !id.test(String(row.key_id)) || !/^age1[a-z0-9]{58}$/.test(String(row.recipient)) || [authority.server_id, authority.machine_id, authority.device_id, authority.pairing_id].some(value => typeof value !== "string" || !id.test(value)) || authority.endpoint !== selected.endpoint || authority.server_id !== selected.serverId || machine && authority.machine_id !== machine) return;
  return row as unknown as WorkerRecipient;
}
export interface WorkerRouteStatus { version: 1; machine_id: string; desired_generation: string; effective_generation: string; native_generation: string; control_state: NativeRouteState; native_state: NativeRouteState; route_id?: string; native_execution_id?: string }
export function workerRouteStatus(raw: Uint8Array, machine: string): WorkerRouteStatus | undefined {
  if (raw.length > 4096) return;
  try {
    const row = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw)));
    if (!only(row, ["version", "machine_id", "desired_generation", "effective_generation", "native_generation", "control_state", "native_state", "route_id", "native_execution_id"]) || row.version !== 1 || row.machine_id !== machine || !id.test(machine) || ![row.desired_generation, row.effective_generation, row.native_generation].every(generation) || ![row.control_state, row.native_state].every(value => Object.values(NativeRouteState).includes(value as NativeRouteState)) || [row.route_id, row.native_execution_id].some(value => value !== undefined && (typeof value !== "string" || !id.test(value)))) return;
    return row as unknown as WorkerRouteStatus;
  } catch { return; }
}
export const ciphertextBase64 = (raw: Uint8Array) => btoa(Array.from(raw, byte => String.fromCharCode(byte)).join(""));
export function encryptedInput(value: string): Uint8Array | undefined {
  if (!value || value.length > 131072 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) return;
  try { const decoded = atob(value); if (decoded.length > 96 << 10 || btoa(decoded) !== value) return; return Uint8Array.from(decoded, char => char.charCodeAt(0)); } catch { return; }
}
