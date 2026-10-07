// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceService, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { document } from "./documents";
import { CreationPreferenceProblem, creationPreferenceProblemMessage, type CreationPreferenceScope } from "./session-creation-preferences";
import type { ReadLocalWorkerProof } from "./local-worker";
import { copy } from "./localization";

export enum RunnerWorkflow { Schedule = "schedule", Checkout = "checkout", RemoteRepository = "remote-repository", NativeObservation = "native-observation", ClaudeLogin = "claude-login", ServerRemediation = "server-remediation", RepositoryRemediation = "repository-remediation", ImportTarget = "import-target" }
export interface RunnerPreferenceSnapshot { revision: number; scope: CreationPreferenceScope; machine_id: string | null; problem: CreationPreferenceProblem | null }
export interface RunnerPreferenceBridge { read(kind: RunnerWorkflow): Promise<unknown>; update(kind: RunnerWorkflow, machine: string, revision: number): Promise<unknown> }
const native: RunnerPreferenceBridge = { read: kind => isTauri() ? invoke("read_runner_device_preferences", { kind }) : Promise.resolve(undefined), update: (kind, machineId, expectedRevision) => invoke("update_runner_device_preferences", { kind, machineId, expectedRevision }) };
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function parseRunnerPreference(value: unknown): RunnerPreferenceSnapshot {
 const row = value as RunnerPreferenceSnapshot;
 if (!row || typeof row !== "object" || Object.keys(row).sort().join(",") !== "machine_id,problem,revision,scope" || !Number.isInteger(row.revision) || row.revision < 1 || row.revision > 0xffffffff || !row.scope || Object.keys(row.scope).sort().join(",") !== "device_id,server_id" || !id.test(row.scope.server_id) || !id.test(row.scope.device_id) || row.machine_id !== null && !id.test(row.machine_id) || row.problem !== null && !Object.values(CreationPreferenceProblem).includes(row.problem)) throw new Error("Invalid Runner preference snapshot");
 return row;
}
interface Memory { machine?: string; pending?: string; needsInspection?: boolean; scope?: CreationPreferenceScope; revision?: number }
interface Authority { bridge: RunnerPreferenceBridge; local?: ReadLocalWorkerProof; memory: Map<RunnerWorkflow, Memory>; queue: Promise<void>; alive: boolean; scope?: CreationPreferenceScope }
const context = createContext<Authority | undefined>(undefined);
export function RunnerPreferenceProvider({ children, readLocalWorker, bridge = native, scope }: { scope?: CreationPreferenceScope; children: ReactNode; readLocalWorker?: ReadLocalWorkerProof; bridge?: RunnerPreferenceBridge }) {
 const authority = useMemo<Authority>(() => ({ bridge, local: readLocalWorker, memory: new Map(), queue: Promise.resolve(), alive: true, scope }), [bridge, scope?.server_id, scope?.device_id]);
 useEffect(() => { authority.alive = true; return () => { authority.alive = false; }; }, [authority]);
 authority.local = readLocalWorker;
 return <context.Provider value={authority}>{children}</context.Provider>;
}
export const eligibleRunner = (row: Resource) => row.kind === EntityKind.MACHINE && row.revision > 0n && supportsResourceSchema(row) && document(row).disabled !== true && document(row).enabled !== false;
const definite = (error: unknown) => error instanceof ConnectError && [Code.NotFound, Code.PermissionDenied].includes(error.code);

// A suggestion is metadata only. Owners keep their original pending requests,
// fixed selections and Local proof, and call remember only after acceptance.
export function useRunnerPreference(kind: RunnerWorkflow, active: boolean, eligible: (row: Resource) => boolean = eligibleRunner, excludeLocal = false, eligibilityKey = "") {
 const authority = useContext(context);
 const transport = useTransport(), client = useMemo(() => createClient(ResourceService, transport), [transport]);
 const requestKey = `${kind}:${eligibilityKey}`;
 const [resolvedKey, setResolvedKey] = useState<string>();
 const [candidates, setCandidates] = useState<Resource[]>([]);
 const [suggestion, setSuggestion] = useState<Resource>();
 const [problem, setProblem] = useState<CreationPreferenceProblem>();
 const [lookupError, setLookupError] = useState<unknown>();
 const [reading, setReading] = useState(false), [canRetry, setCanRetry] = useState(false);
 const epoch = useRef(0), alive = useRef(true), touched = useRef(false), eligibility = useRef(eligible);
 eligibility.current = eligible;
 useEffect(() => { alive.current = true; return () => { alive.current = false; epoch.current++; }; }, []);
 const inspect = async () => {
  if (!authority || !active) return;
  const owner = ++epoch.current; setReading(true); setLookupError(undefined);
  const live = () => alive.current && epoch.current === owner;
  const memory = authority.memory.get(kind) ?? { scope: authority.scope }; authority.memory.set(kind, memory);
  try {
   const raw = await authority.bridge.read(kind); if (!live()) return;
   if (raw !== undefined) {
    const snapshot = parseRunnerPreference(raw);
    if (memory.scope && (memory.scope.server_id !== snapshot.scope.server_id || memory.scope.device_id !== snapshot.scope.device_id) || memory.revision !== undefined && snapshot.revision < memory.revision) throw new Error("Replaced Runner preference authority");
    memory.scope = snapshot.scope; memory.revision = snapshot.revision; memory.needsInspection = Boolean(snapshot.problem); setProblem(snapshot.problem ?? undefined);
    if (!snapshot.problem && !memory.pending) memory.machine = snapshot.machine_id ?? undefined;
    setCanRetry(!snapshot.problem && Boolean(memory.pending));
   }
   const excludedLocalID = excludeLocal ? (await authority.local?.())?.machineId : undefined;
   if (excludeLocal && !excludedLocalID) throw new Error("Local device identity unavailable");
   const resolve = async (machine: string) => {
    try { const result = await client.getResource({ kind: EntityKind.MACHINE, id: machine }); if (!live()) return undefined; const row = result.resource; if (!row || row.id !== machine || row.kind !== EntityKind.MACHINE || row.revision < 1n || !supportsResourceSchema(row)) throw new ConnectError("Invalid exact Runner resource", Code.DataLoss); return row.id !== excludedLocalID && eligibleRunner(row) && eligibility.current(row) ? row : undefined; }
    catch (error) { if (definite(error)) return undefined; throw error; }
   };
   let row = memory.machine ? await resolve(memory.machine) : undefined;
   const candidates = row ? [row] : [];
   // Network uncertainty above exits before native fallback. Only the native
   // adapter's machine identity is used; its proof never enters this store.
   if (authority.local && !excludeLocal) {
    try { const local = await authority.local(); if (!live()) return; const choice = await resolve(local.machineId); if (choice && !candidates.some(r => r.id === choice.id)) candidates.push(choice); row ??= choice; }
    catch (error) { if (!row) throw error; }
   }
   if (live() && !touched.current) { setSuggestion(row); setCandidates(candidates); setResolvedKey(requestKey); }
  } catch (error) { if (live()) setLookupError(error); }
  finally { if (live()) setReading(false); }
 };
 useEffect(() => {
  epoch.current++; setResolvedKey(undefined); setSuggestion(undefined); setCandidates([]);
  if (active) { touched.current = false; void inspect(); }
  else setReading(false);
 }, [active, authority, kind, client, excludeLocal, eligibilityKey]);
 const persist = (machine: string) => {
  if (!authority) return;
  const memory = authority.memory.get(kind) ?? { scope: authority.scope }; authority.memory.set(kind, memory);
  authority.queue = authority.queue.catch(() => {}).then(async () => {
   if (!authority.alive || memory.needsInspection) return;
   try {
    const raw = await authority.bridge.read(kind); if (!authority.alive || raw === undefined) return;
    const read = parseRunnerPreference(raw);
    if (memory.scope && (read.scope.server_id !== memory.scope.server_id || read.scope.device_id !== memory.scope.device_id)) throw new Error("Replaced Runner preference authority");
    memory.scope = read.scope; memory.revision = read.revision;
    if (read.problem) { memory.needsInspection = true; if (alive.current) setProblem(read.problem); return; }
    const result = parseRunnerPreference(await authority.bridge.update(kind, machine, read.revision)); if (!authority.alive) return;
    if (result.scope.server_id !== read.scope.server_id || result.scope.device_id !== read.scope.device_id || result.revision < read.revision) throw new Error("Replaced Runner preference authority");
    memory.revision = result.revision; memory.needsInspection = Boolean(result.problem); if (alive.current) setProblem(result.problem ?? undefined);
    if (!result.problem && memory.pending === machine) memory.pending = undefined;
    if (alive.current) setCanRetry(false);
   } catch { memory.needsInspection = true; if (alive.current) { setProblem(CreationPreferenceProblem.OutcomeUnknown); setCanRetry(false); } }
  });
 };
 const remember = (machine: string) => { if (!id.test(machine) || !authority) return; const memory = authority.memory.get(kind) ?? { scope: authority.scope }; memory.machine = machine; memory.pending = machine; authority.memory.set(kind, memory); persist(machine); };
 const touch = () => { touched.current = true; epoch.current++; setReading(false); };
 const guidance = authority ? <>{reading ? <p role="status">{copy("new-session.preferencesResolving")}</p> : null}{problem ? <p role="alert">{creationPreferenceProblemMessage(problem)}</p> : null}{lookupError ? <p role="alert">{copy("new-session.preferencesReadFailed")}</p> : null}{!reading && !suggestion && !lookupError ? <p>{copy("runner-preferences.choose")}</p> : null}{problem || lookupError ? <button type="button" disabled={reading} onClick={() => void inspect()}>{copy("new-session.preferencesInspect")}</button> : null}{canRetry ? <button type="button" onClick={() => { const memory = authority?.memory.get(kind); if (memory?.pending) { setCanRetry(false); persist(memory.pending); } }}>{copy("new-session.preferencesSave")}</button> : null}</> : null;
 return { suggestion: active && resolvedKey === requestKey ? suggestion : undefined, candidates: active && resolvedKey === requestKey ? candidates : [], resolved: active && resolvedKey === requestKey && !reading, reading, lookupFailed: Boolean(lookupError), touch, remember, guidance, inspect };
}
