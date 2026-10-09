// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document as readDocument, object } from "./documents";
import { copy } from "./localization";

export interface TurnTiming { accepted: number; terminal?: number }
export interface TurnProjection { owner: string; inputId: string; timing?: TurnTiming; captured: boolean; inherited?: { sessionId: string; executionId: string; inputId: string } }
export interface CurrentTurn extends TurnProjection { running: boolean }
const uuid = (value: unknown): value is string => typeof value === "string" && /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(value);
const identity = (value: unknown): value is string => typeof value === "string" && value.trim().length > 0 && new TextEncoder().encode(value).length <= 1024 && !/[\u0000-\u001f\u007f\uD800-\uDFFF]/u.test(value);
function utc(value: unknown): number | undefined {
  if (typeof value !== "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,3})?Z$/.test(value)) return;
  const parsed = Date.parse(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0 || new Date(parsed).toISOString().slice(0, 19) !== value.slice(0, 19)) return;
  return parsed;
}
export function retainedTurnTiming(value: unknown): TurnTiming | undefined {
  const timing = object(value), accepted = utc(timing.accepted_at);
  if (accepted === undefined || Object.keys(timing).some(key => !["accepted_at", "terminal_at"].includes(key))) return;
  if (!Object.hasOwn(timing, "terminal_at")) return { accepted };
  const terminal = utc(timing.terminal_at);
  if (terminal === undefined || terminal < accepted || !Number.isSafeInteger(terminal - accepted)) return;
  return { accepted, terminal };
}
function owner(session: string, execution: string, thread: string, turn: string) { return JSON.stringify([session, execution, thread, turn]); }
/** Original native owner only, retained before a late primary-user record. */
export function messageTurnOwner(row: Resource, sessionId: string): string | undefined {
  const d = readDocument(row);
  if (row.kind !== EntityKind.MESSAGE || row.sessionId !== sessionId || row.schemaVersion !== 1 || row.revision <= 0n || row.documentJson.byteLength > 1 << 20 || !uuid(row.id) || !uuid(sessionId) || !uuid(d.execution_id) || !identity(d.native_thread_id) || !identity(d.native_turn_id) || !["user", "assistant", "tool", "artifact", "progress"].includes(String(d.role)) || !["streaming", "complete"].includes(String(d.state)) || d.inherited != null || !Number.isSafeInteger(d.first_sequence) || Number(d.first_sequence) <= 0 || !Number.isSafeInteger(d.last_sequence) || Number(d.last_sequence) < Number(d.first_sequence)) return;
  return owner(sessionId, d.execution_id, d.native_thread_id, d.native_turn_id);
}
/** Bounded identity/timestamp metadata only; no prompt/native content retention. */
export function messageTurn(row: Resource, sessionId: string): TurnProjection | undefined {
  const d = readDocument(row), inherited = object(d.inherited);
  if (row.kind !== EntityKind.MESSAGE || row.sessionId !== sessionId || row.schemaVersion !== 1 || row.revision <= 0n || row.documentJson.byteLength > 1 << 20 || !uuid(row.id) || !uuid(sessionId) || d.role !== "user" || !uuid(d.execution_id) || !identity(d.native_thread_id) || !identity(d.native_turn_id)) return;
  if (!["streaming", "complete"].includes(String(d.state)) || typeof d.text !== "string" || new TextEncoder().encode(d.text).length > 256 * 1024 || ["tool", "artifact", "progress", "claude", "claude_tool", "claude_progress", "claude_interruption", "grok_tool", "grok_text"].some(key => d[key] != null)) return;
  let origin: TurnProjection["inherited"];
  if (d.inherited != null) {
    if (!uuid(inherited.session_id) || !uuid(inherited.execution_id) || !uuid(inherited.input_id) || !uuid(inherited.message_id) || d.input_id !== undefined && d.input_id !== "" || d.first_sequence !== 0 || d.last_sequence !== 0 || !Number.isSafeInteger(inherited.first_sequence) || Number(inherited.first_sequence) <= 0 || !Number.isSafeInteger(inherited.last_sequence) || Number(inherited.last_sequence) < Number(inherited.first_sequence)) return;
    origin = { sessionId: inherited.session_id, executionId: inherited.execution_id, inputId: inherited.input_id };
  } else if (!uuid(d.input_id) || !Number.isSafeInteger(d.first_sequence) || Number(d.first_sequence) <= 0 || !Number.isSafeInteger(d.last_sequence) || Number(d.last_sequence) < Number(d.first_sequence)) return;
  return { owner: owner(sessionId, d.execution_id, d.native_thread_id, d.native_turn_id), inputId: origin?.inputId ?? String(d.input_id), captured: Object.hasOwn(d, "turn_timing"), timing: retainedTurnTiming(d.turn_timing), inherited: origin };
}
export function currentTurn(row: Resource | undefined, sessionId: string): CurrentTurn | undefined {
  if (!row || row.kind !== EntityKind.SESSION || row.id !== sessionId || row.schemaVersion !== 1 || row.revision <= 0n || row.documentJson.byteLength > 1 << 20 || !uuid(sessionId)) return;
  const d = readDocument(row), e = object(d.execution), selected = object(d.current_execution ?? d.initial_execution);
  if (!uuid(e.execution_id) || !uuid(e.input_id) || !uuid(e.job_id) || selected.id !== e.execution_id || selected.input_id !== e.input_id || d.active_execution_id && d.active_execution_id !== e.execution_id || !identity(e.native_thread_id) || !identity(e.native_turn_id) || !["running", "succeeded", "failed", "stopped"].includes(String(e.outcome))) return;
  const bindings = Array.isArray(e.accepted_inputs) ? e.accepted_inputs.map(object) : [];
  const bound = bindings.length > 0 && bindings.length <= 4095 && bindings[0].input_id === e.input_id && bindings.every(binding => uuid(binding.input_id) && typeof binding.prompt_digest === "string" && /^[a-f0-9]{64}$/.test(binding.prompt_digest)) && new Set(bindings.map(binding => binding.input_id)).size === bindings.length;
  if (e.accepted_inputs !== undefined && !bound || !Number.isSafeInteger(e.last_sequence) || Number(e.last_sequence) < 2 || Number(e.last_sequence) > 100000) return;
  const timing = retainedTurnTiming(e.turn_timing), running = e.outcome === "running";
  // A terminal timestamp never fabricates a terminal outcome and a terminal
  // outcome without its retained end cannot fabricate a finished duration.
  const consistent = timing && bound && (running ? timing.terminal === undefined : timing.terminal !== undefined);
  return { owner: owner(sessionId, e.execution_id, e.native_thread_id, e.native_turn_id), inputId: e.input_id, captured: Object.hasOwn(e, "turn_timing"), timing: consistent ? timing : undefined, running };
}

/** Exactly one mounted timer; inactive/unconfirmed observations do no clock work. */
export function useTurnClock(turn: CurrentTurn | undefined, active: boolean, confirmed: boolean) {
  const frame = useRef<{ owner: string; seconds?: number }>({ owner: "" });
  const [, redraw] = useState(0);
  const accepted = turn?.running ? turn.timing?.accepted : undefined;
  useLayoutEffect(() => {
    if (!active || !confirmed || accepted === undefined || !turn) return;
    const tick = () => {
      const delta = Date.now() - accepted;
      frame.current = { owner: turn.owner, seconds: Number.isSafeInteger(delta) && delta >= 0 ? Math.floor(delta / 1000) : undefined };
      redraw(value => value + 1);
    };
    tick();
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, [active, confirmed, turn?.owner, accepted]);
  return turn && frame.current.owner === turn.owner ? frame.current.seconds : undefined;
}
export function turnDuration(seconds: number): string {
  const days = Math.floor(seconds / 86400), hours = Math.floor(seconds / 3600) % 24, minutes = Math.floor(seconds / 60) % 60, rest = seconds % 60;
  return [days ? copy("turnTiming.days", { v0: days }) : "", hours ? copy("turnTiming.hours", { v0: hours }) : "", minutes ? copy("turnTiming.minutes", { v0: minutes }) : "", copy("turnTiming.seconds", { v0: rest })].filter(Boolean).join(" ");
}
const TurnClockContext = createContext<number | undefined>(undefined);
export function TurnTimingProvider({ current, active, confirmed, children }: { current?: CurrentTurn; active: boolean; confirmed: boolean; children: ReactNode }) {
  const seconds = useTurnClock(current, active, confirmed);
  return <TurnClockContext.Provider value={seconds}>{children}</TurnClockContext.Provider>;
}
function LiveTurnTime(props: { turn: TurnProjection; current?: CurrentTurn; confirmed: boolean }) {
  const seconds = useContext(TurnClockContext);
  return <TurnTimeLine {...props} seconds={seconds} />;
}
export function TurnTime(props: { turn: TurnProjection; current?: CurrentTurn; confirmed: boolean }) {
  return props.current?.owner === props.turn.owner && props.current.inputId === props.turn.inputId && props.current.timing?.terminal === undefined ? <LiveTurnTime {...props} /> : <TurnTimeLine {...props} />;
}
function TurnTimeLine({ turn, current, seconds, confirmed }: { turn: TurnProjection; current?: CurrentTurn; seconds?: number; confirmed: boolean }) {
  const matches = current?.owner === turn.owner && current.inputId === turn.inputId;
  const timing = matches ? current.timing : turn.timing;
  const terminal = timing?.terminal !== undefined;
  const value = terminal ? Math.floor((timing!.terminal! - timing!.accepted) / 1000) : matches ? seconds : undefined;
  const label = value === undefined ? copy("turnTiming.unavailable") : terminal ? copy("turnTiming.elapsed", { v0: turnDuration(value) }) : copy(matches && confirmed ? "turnTiming.inProgress" : "turnTiming.unconfirmed", { v0: turnDuration(value) });
  const unconfirmed = !terminal && (!matches || !confirmed);
  const inherited = turn.inherited;
  const displayed = `${inherited ? `${copy("turnTiming.inherited")} · ` : ""}${label}${unconfirmed && value === undefined ? ` · ${copy("turnTiming.unconfirmedLabel")}` : ""}`;
  return <p className="turn-time" data-turn-owner={turn.owner} aria-live="off" aria-label={inherited ? `${displayed}. ${copy("turnTiming.inheritedOrigin", { v0: inherited.sessionId, v1: inherited.executionId, v2: inherited.inputId })}` : undefined}>{displayed}</p>;
}
