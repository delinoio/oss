// SPDX-License-Identifier: Apache-2.0
export enum NativeShellDelivery { Acknowledged = "acknowledged", Uncertain = "uncertain", Rejected = "rejected" }
export enum NativeShellProcessStatus { Running = "inProgress", Completed = "completed", Failed = "failed", Declined = "declined" }
export enum NativeShellJobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
export interface NativeShellProcess { item_id: string; process_id?: string; command: string; cwd: string; status: NativeShellProcessStatus; output: string; aggregated_output?: string; exit_code?: number }
export interface NativeShellObservation { version: 1; action_id: string; native_thread_id: string; native_turn_id?: string; delivery: NativeShellDelivery; terminal: boolean; cleanup_verified: boolean; processes: NativeShellProcess[]; sequence: number }
const encoder = new TextEncoder();
export function shellText(value: unknown, limit: number, required = false): value is string {
  return typeof value === "string" && (!required || Boolean(value.trim())) && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= limit;
}
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const keys = (value: Record<string, unknown>, allowed: readonly string[]) => Object.keys(value).every(key => allowed.includes(key));
export function readNativeShellObservation(value: unknown): NativeShellObservation | undefined {
  if (!record(value) || !keys(value, ["version", "action_id", "native_thread_id", "native_turn_id", "delivery", "terminal", "cleanup_verified", "processes", "sequence"]) || value.version !== 1 || !shellText(value.action_id, 1024, true) || !shellText(value.native_thread_id, 1024, true) || value.native_turn_id !== undefined && !shellText(value.native_turn_id, 1024, true) || !Object.values(NativeShellDelivery).includes(value.delivery as NativeShellDelivery) || typeof value.terminal !== "boolean" || typeof value.cleanup_verified !== "boolean" || value.cleanup_verified && !value.terminal || !Number.isSafeInteger(value.sequence) || (value.sequence as number) < 1 || (value.sequence as number) > 100000 || !Array.isArray(value.processes) || value.processes.length > 128) return;
  let bytes = 0;
  const identities = new Set<string>();
  for (const process of value.processes) {
    if (!record(process) || !keys(process, ["item_id", "process_id", "command", "cwd", "status", "output", "aggregated_output", "exit_code"]) || !shellText(process.item_id, 1024, true) || identities.has(process.item_id) || process.process_id !== undefined && !shellText(process.process_id, 1024, true) || !shellText(process.command, 65536, true) || !shellText(process.cwd, 4096) || !shellText(process.output, 262144) || process.aggregated_output !== undefined && !shellText(process.aggregated_output, 262144) || !Object.values(NativeShellProcessStatus).includes(process.status as NativeShellProcessStatus) || process.exit_code !== undefined && !Number.isSafeInteger(process.exit_code) || value.terminal && process.status === NativeShellProcessStatus.Running) return;
    identities.add(process.item_id);
    bytes += encoder.encode(process.command).length + encoder.encode(process.output).length + (typeof process.aggregated_output === "string" ? encoder.encode(process.aggregated_output).length : 0);
    if (bytes > 524288) return;
  }
  return value as unknown as NativeShellObservation;
}
