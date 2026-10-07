// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { copy } from "./localization";
import { invoke, isTauri } from "@tauri-apps/api/core";
import type { NewSessionKind } from "./new-session";
export interface CreationPreferenceScope { server_id: string; device_id: string }
export interface CreationPreferencePair { agent_id: string; machine_id: string }
export enum CreationPreferenceProblem { Unavailable = "unavailable", ReadFailed = "read-failed", InvalidDocument = "invalid-document", UnsupportedVersion = "unsupported-version", WriteFailed = "write-failed", OutcomeUnknown = "outcome-unknown", Changed = "changed", Capacity = "capacity" }
export interface CreationPreferenceSnapshot { revision: number; scope: CreationPreferenceScope; pair: CreationPreferencePair | null; problem: CreationPreferenceProblem | null }
export interface CreationPreferenceBridge { read: (kind: NewSessionKind) => Promise<unknown>; update: (kind: NewSessionKind, pair: CreationPreferencePair, revision: number) => Promise<unknown> }
const nativeBridge: CreationPreferenceBridge = {
 read: kind => isTauri() ? invoke("read_session_creation_preferences", { kind }) : Promise.resolve(undefined),
 update: (kind, pair, revision) => invoke("update_session_creation_preferences", { kind, agentId: pair.agent_id, machineId: pair.machine_id, expectedRevision: revision }),
};
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const sameScope = (a: CreationPreferenceScope, b: CreationPreferenceScope) => a.server_id === b.server_id && a.device_id === b.device_id;
function keys(value: unknown, expected: string): value is Record<string, unknown> { return Boolean(value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).sort().join(",") === expected); }
export function parseCreationPreferences(value: unknown): CreationPreferenceSnapshot {
 if (!keys(value, "pair,problem,revision,scope") || !Number.isInteger(value.revision) || Number(value.revision) < 1 || Number(value.revision) > 0xffffffff || !keys(value.scope,"device_id,server_id") || !id.test(String(value.scope.server_id)) || !id.test(String(value.scope.device_id)) || (value.problem !== null && !Object.values(CreationPreferenceProblem).includes(value.problem as CreationPreferenceProblem)) || (value.pair !== null && (!keys(value.pair,"agent_id,machine_id") || !id.test(String(value.pair.agent_id)) || !id.test(String(value.pair.machine_id))))) throw new Error("Invalid session creation preferences");
 return value as unknown as CreationPreferenceSnapshot;
}
export function useCreationPreferences(kind: NewSessionKind, active: boolean, bridge = nativeBridge, expectedScope?: CreationPreferenceScope) {
 const [pair,setPair] = useState<CreationPreferencePair>();
 const [problem,setProblem] = useState<CreationPreferenceProblem>();
 const [reading,setReading] = useState(false);
 const [canRetry,setCanRetry] = useState(false);
 const mounted = useRef(true), generation = useRef(0), nonce = useRef(0), initialized = useRef(false), scope = useRef(expectedScope), pending = useRef<CreationPreferencePair | undefined>(undefined), snapshot = useRef<CreationPreferenceSnapshot | undefined>(undefined), queue = useRef(Promise.resolve());
 useEffect(() => { mounted.current=true; return () => {mounted.current=false;generation.current++;initialized.current=false;nonce.current++;}; },[]);
 const accept = useCallback((raw: unknown) => {
  const next=parseCreationPreferences(raw);
  if (scope.current && !sameScope(scope.current,next.scope)) throw new Error("Replaced connection preference scope");
  scope.current=next.scope;
  if (snapshot.current && next.revision < snapshot.current.revision) throw new Error("Stale creation preference revision");
  snapshot.current=next;setProblem(next.problem ?? undefined);return next;
 },[]);
 const reinspect = useCallback(async () => {
  const owner=generation.current, request=++nonce.current;setReading(true);setCanRetry(false);
  try {const raw=await bridge.read(kind);if (!mounted.current || generation.current!==owner || nonce.current!==request) return;
   if (raw===undefined) return;
   const next=accept(raw);if (!pending.current && !next.problem) setPair(next.pair ?? undefined);setCanRetry(!next.problem && Boolean(pending.current));
  } catch {if (mounted.current && generation.current===owner && nonce.current===request) setProblem(CreationPreferenceProblem.ReadFailed);}
  finally {if(mounted.current && generation.current===owner && nonce.current===request)setReading(false);}
 },[bridge,kind,accept]);
 useEffect(() => {if(active && !initialized.current) {initialized.current=true;void reinspect();}},[active,reinspect]);
 const persist = useCallback((submitted: CreationPreferencePair) => {
  const owner=generation.current;
  queue.current=queue.current.catch(()=>{}).then(async()=>{
   if(!mounted.current || generation.current!==owner)return;
   try {
    const raw=await bridge.read(kind);if(!mounted.current || generation.current!==owner)return;
    if(raw===undefined)return;
    const read=accept(raw);if(read.problem)return;
    const result=await bridge.update(kind,submitted,read.revision);if(!mounted.current || generation.current!==owner)return;
    const updated=accept(result);
    if(!updated.problem && pending.current===submitted)pending.current=undefined;
    setCanRetry(false);
   } catch {if(mounted.current && generation.current===owner){setProblem(CreationPreferenceProblem.OutcomeUnknown);setCanRetry(false);}}
  });
 },[bridge,kind,accept]);
 const remember = useCallback((submitted: CreationPreferencePair) => {nonce.current++;setReading(false);pending.current=submitted;setPair(submitted);persist(submitted);},[persist]);
 const retrySave = useCallback(()=>{if(canRetry && pending.current){setCanRetry(false);persist(pending.current);}},[canRetry,persist]);
 return {pair,problem,reading,reinspect,remember,retrySave,canRetry};
}

export function creationPreferenceProblemMessage(problem: CreationPreferenceProblem): string {
 switch (problem) {
  case CreationPreferenceProblem.Unavailable: return copy("new-session.preferencesUnavailable");
  case CreationPreferenceProblem.ReadFailed: return copy("new-session.preferencesReadFailed");
  case CreationPreferenceProblem.InvalidDocument: return copy("new-session.preferencesInvalidDocument");
  case CreationPreferenceProblem.UnsupportedVersion: return copy("new-session.preferencesUnsupportedVersion");
  case CreationPreferenceProblem.WriteFailed: return copy("new-session.preferencesWriteFailed");
  case CreationPreferenceProblem.OutcomeUnknown: return copy("new-session.preferencesOutcomeUnknown");
  case CreationPreferenceProblem.Changed: return copy("new-session.preferencesChanged");
  case CreationPreferenceProblem.Capacity: return copy("new-session.preferencesCapacity");
 }
}
