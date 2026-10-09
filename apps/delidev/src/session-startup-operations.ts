// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object } from "./documents";
import { SessionProgressPhase } from "./session-progress";
export interface StartupOperationRow { operation: number; state: "pending" | "running" | "completed"; ordinal?: number; count?: number }
export interface StartupOperations { workspace: StartupOperationRow[]; native: StartupOperationRow[] }
const uuid = (x: unknown): x is string => typeof x === "string" && /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(x);
const integer = (x: unknown): x is number => Number.isSafeInteger(x) && Number(x) >= 0;
interface Step { workspace_operation?: number; native_phase?: number; repository_id?: string; repository_ordinal?: number; repository_count?: number; sequence?: number; state?: number }
function attempt(value: unknown, job: unknown, execution?: unknown): Step[] | undefined {
 const a = object(value);
 if (!uuid(job) || a.job_id !== job || a.execution_id !== execution || !integer(a.assignment_revision) || a.assignment_revision < 1 || !integer(a.last_sequence) || a.last_sequence < 1 || ![a.machine_id,a.instance_id,a.device_id,a.server_epoch].every(uuid) || !Array.isArray(a.steps) || !a.steps.length || a.steps.length > (execution ? 6 : 403) || Object.keys(a).some(k => !["job_id","execution_id","assignment_revision","machine_id","instance_id","device_id","server_epoch","last_sequence","steps"].includes(k))) return;
 const keys = new Set<string>(), steps: Step[] = [];
 for (const value of a.steps) {
  const s = object(value), op = execution ? s.native_phase : s.workspace_operation;
  if (!integer(op) || op < 1 || op > (execution ? 6 : 7) || (execution ? s.workspace_operation != null : s.native_phase != null) || Object.keys(s).some(k => !["workspace_operation","native_phase","repository_id","repository_ordinal","repository_count","sequence","state"].includes(k))) return;
  if (s.state != null && ![1,2].includes(Number(s.state)) || s.sequence != null && (!integer(s.sequence) || s.sequence < 1 || s.sequence > a.last_sequence) || (s.state == null) !== (s.sequence == null)) return;
  if (s.repository_id != null) { if (execution || !uuid(s.repository_id) || !integer(s.repository_ordinal) || !integer(s.repository_count) || s.repository_count > 100 || s.repository_ordinal < 1 || s.repository_ordinal > s.repository_count || op < 2 || op > 5) return; }
  else if (s.repository_ordinal != null || s.repository_count != null || !execution && ![1,6,7].includes(op)) return;
  const key = `${op}:${s.repository_id ?? ""}`; if (keys.has(key)) return; keys.add(key); steps.push(s as Step);
 }
 return steps;
}
/** Current original metadata only; descriptive input/response completion is ignored. */
export function startupOperations(resource: Resource | undefined, phase: SessionProgressPhase | undefined): StartupOperations | undefined {
 if (!resource || !phase) return;
 const d = document(resource), p = object(d.startup_progress), preparation = object(d.preparation), execution = object(d.current_execution ?? d.initial_execution), active = object(d.execution);
 if (!Object.keys(p).length || Object.keys(p).some(k => !["workspace","native"].includes(k))) return;
 const workspace = p.workspace == null ? [] : attempt(p.workspace, preparation.job_id);
 const native = p.native == null ? [] : attempt(p.native, active.job_id ?? object(d.startup).job_id ?? object(p.native).job_id, execution.id);
 if (!workspace || !native) return;
 const all = [...workspace,...native], latest = Math.max(0,...all.map(s => s.sequence ?? 0));
 function aggregate(steps: Step[], kind: "workspace_operation" | "native_phase"): StartupOperationRow[] {
  return [...new Set(steps.map(s => s[kind]!))].sort((a,b) => a-b).map(operation => {
   const group = steps.filter(s => s[kind] === operation), current = group.find(s => s.sequence === latest && s.state === 1);
   const completed = group.every(s => s.state === 2);
   return { operation, state: completed ? "completed" : current ? "running" : "pending", ...(current?.repository_id ? { ordinal:current.repository_ordinal,count:current.repository_count } : {}) };
  });
 }
 const workspaceRows = aggregate(workspace,"workspace_operation");
 for (const row of workspaceRows) if (phase !== SessionProgressPhase.Preparing && row.state === "running") row.state = "pending";
 const nativeRows = native.length ? aggregate(native,"native_phase") : [1,2,3,4,5,6].map(operation => ({ operation, state: "pending" as StartupOperationRow["state"] }));
 for (const row of nativeRows) if ((phase === SessionProgressPhase.Preparing || phase === SessionProgressPhase.Queued || phase === SessionProgressPhase.Response && row.operation < 5) && row.state === "running") row.state = "pending";
 // The original accepted input/transcript projection alone proves input delivery.
 for (const row of nativeRows) if (row.operation >= 5) row.state = row.operation === 5 && phase === SessionProgressPhase.Response ? "completed" : row.operation === 6 && phase === SessionProgressPhase.Response ? "running" : row.operation === 5 && phase === SessionProgressPhase.Starting && native.some(s => s.native_phase === 5 && s.state === 1) ? "running" : "pending";
 nativeRows.unshift({ operation:0, state:phase === SessionProgressPhase.Queued ? "running" : phase === SessionProgressPhase.Starting || phase === SessionProgressPhase.Response ? "completed" : "pending" });
 return { workspace:workspaceRows,native:nativeRows };
}

/** Retained failure context remains descriptive and never animates or offers actions. */
export function observedStartupOperation(resource: Resource | undefined): { native: boolean; operation: number; ordinal?: number; count?: number } | undefined {
 if (!resource) return;
 const d = document(resource), p = object(d.startup_progress), failure = object(object(d.startup).failure);
 const n = attempt(p.native, object(d.startup).job_id, object(d.startup).execution_id);
 const w = attempt(p.workspace, object(d.preparation).job_id);
 const native = Object.keys(failure).length > 0;
 const steps = native ? n : w;
 if (!steps?.length) return;
 const current = [...steps].sort((a,b) => (b.sequence ?? 0) - (a.sequence ?? 0))[0];
 if (current.state !== 1) return;
 return { native, operation:(native ? current.native_phase : current.workspace_operation)!,ordinal:current.repository_ordinal,count:current.repository_count };
}

/** Original Worker presence from a successful server-owned read, never report age. */
export function startupWorkerCurrent(session: Resource | undefined, machine: Resource | undefined, observedAt: number, successfulRead: boolean): boolean {
 if (!session || !machine || !successfulRead || !Number.isFinite(observedAt) || machine.kind !== EntityKind.MACHINE || machine.schemaVersion !== 1 || machine.revision < 1n) return false;
 const d = document(session), m = document(machine), p = object(d.startup_progress), selected = object(d.current_execution ?? d.initial_execution);
 const native = object(p.native), preparation = object(p.workspace);
 const a = native.execution_id === selected.id ? native : preparation;
 const lastSeen = typeof m.last_seen === "string" ? Date.parse(m.last_seen) : NaN;
 return uuid(a.instance_id) && a.machine_id === machine.id && d.machine_id === machine.id && object(m.network).instance_id === a.instance_id && m.disabled === false && Number.isFinite(lastSeen) && lastSeen > 0 && observedAt >= lastSeen && observedAt - lastSeen < 60_000;
}
