// SPDX-License-Identifier: Apache-2.0
// Synthetic presentation only. This entry has no native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BudgetState, EntityKind, EventAction, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type WatchEventsResponse } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";
import { transcriptResource, transcriptRoleFixtures, transcriptSessionId } from "./transcript-role-fixtures";
import "./themes.css";
import "./styles.css";
import "./transcript-role-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
if (args.get("theme") !== "system") document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
let session = create(ResourceSchema, { id: transcriptSessionId, sessionId: transcriptSessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Synthetic conversation", workspace: args.get("workspace") ?? "general-chat", archive: "active", outcome: "stopped", dispatch: "blocked", recovery: "none" }) });
const data = transcriptRoleFixtures();
const historical = [...Object.values(data), { role: "tool", state: "complete", text: "Original tool card" }, { ...data.grokUser, native_id: "foreign" }, { ...data.claude, claude: { invalid: true } }].map((value, index) => transcriptResource(value, index + 2));
const timingMode = args.get("timing");
const timingIds = { execution: newRequestId(), input: newRequestId(), job: newRequestId(), steer: newRequestId() };
const timingStart = "2026-10-09T10:00:00.123Z";
let timingFailed = false, timingReads = 0;
if (timingMode) {
  const timing = timingMode === "legacy" ? undefined : { accepted_at: timingMode === "invalid" ? "invalid" : timingStart, ...(timingMode === "zero" ? { terminal_at: timingStart } : {}) };
  session = create(ResourceSchema, { ...session, documentJson: encode({ name: "Synthetic accepted timing", workspace: args.get("workspace") ?? "general-chat", archive: "active", outcome: timingMode === "zero" ? "succeeded" : "running", dispatch: "claimed", recovery: "none", initial_execution: { id: timingIds.execution, input_id: timingIds.input }, active_execution_id: timingIds.execution, execution: { execution_id: timingIds.execution, input_id: timingIds.input, job_id: timingIds.job, native_thread_id: "timing-thread", native_turn_id: "timing-turn", last_sequence: 2, outcome: timingMode === "zero" ? "succeeded" : "running", accepted_inputs: [{ input_id: timingIds.input, prompt_digest: "a".repeat(64) }], turn_timing: timing } }) });
  historical.splice(0);
  if (timingMode === "inherited") historical.push(transcriptResource({ role: "user", state: "complete", text: "Inherited original immutable input", execution_id: newRequestId(), input_id: "", native_thread_id: "inherited-thread", native_turn_id: "inherited-turn", first_sequence: 0, last_sequence: 0, inherited: { session_id: newRequestId(), execution_id: newRequestId(), input_id: newRequestId(), message_id: newRequestId(), first_sequence: 3, last_sequence: 4 }, turn_timing: { accepted_at: "2026-10-08T08:59:59.123Z", terminal_at: timingStart } }, 40));
}
const resources = new Map(historical.map(row => [row.id, row]));
const events: WatchEventsResponse[] = [];
let wake: (() => void) | undefined, revision = 0n;
function publish() {
  revision++;
  const row = transcriptResource({ role: "assistant", state: revision === 1n ? "streaming" : "complete", text: revision === 1n ? "Synthetic streaming text" : "Synthetic complete text" }, 90, revision);
  resources.set(row.id, row);
  events.push(createEvent(row.id, revision)); wake?.();
}
function createEvent(entityId: string, revision: bigint): WatchEventsResponse {
  return { $typeName: "delidev.v1.WatchEventsResponse", cursor: `fixture-${revision}`, id: newRequestId(), entityId, kind: EntityKind.MESSAGE, sessionId: session.id, revision, action: revision === 1n ? EventAction.CREATED : EventAction.UPDATED, time: "2026-10-08T00:00:00Z" };
}
function timingSessionEvent() {
  events.push({ $typeName: "delidev.v1.WatchEventsResponse", cursor: `timing-${session.revision}`, id: newRequestId(), entityId: session.id, kind: EntityKind.SESSION, sessionId: session.id, revision: session.revision, action: EventAction.UPDATED, time: timingStart }); wake?.();
}
Object.assign(window, { __turnTimingFixture: {
  get reads() { return timingReads; },
  early() {
    const original = JSON.parse(new TextDecoder().decode(session.documentJson)); original.execution.last_sequence = 3;
    session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(original) }); timingSessionEvent();
    const row = transcriptResource({ role: "assistant", state: "complete", text: "Original native output before late user publication", execution_id: timingIds.execution, native_thread_id: "timing-thread", native_turn_id: "timing-turn", first_sequence: 3, last_sequence: 3 }, 49);
    resources.set(row.id,row); historical.push(row); events.push(createEvent(row.id,row.revision)); wake?.();
  },
  parts() {
    const original = JSON.parse(new TextDecoder().decode(session.documentJson));
    original.execution.accepted_inputs.push({ input_id: timingIds.steer, prompt_digest: "b".repeat(64) }); original.execution.last_sequence = 8;
    session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(original) }); timingSessionEvent();
    for (const [index, input] of [timingIds.input, timingIds.input, timingIds.steer].entries()) {
      const row = transcriptResource({ role: "user", state: "complete", text: index === 2 ? "Same-turn original accepted Steer" : "Original accepted primary part", execution_id: timingIds.execution, input_id: input, native_thread_id: "timing-thread", native_turn_id: "timing-turn", first_sequence: 4 + index, last_sequence: 4 + index, ...(index === 2 ? {} : { turn_timing: { accepted_at: timingStart } }) }, 50 + index);
      resources.set(row.id, row); historical.push(row); events.push(createEvent(row.id, row.revision));
    }
    wake?.();
  },
  fail() { timingFailed = true; wake?.(); },
  reconnect() { timingFailed = false; },
  terminal(outcome: string) {
    const original = JSON.parse(new TextDecoder().decode(session.documentJson)); original.execution.outcome = outcome; original.outcome = outcome; original.execution.last_sequence = 9; original.execution.turn_timing = { accepted_at: timingStart, terminal_at: "2026-10-09T10:00:12.123Z" };
    session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(original) }); timingSessionEvent();
    for (const original of [...resources.values()]) { const d = JSON.parse(new TextDecoder().decode(original.documentJson)); if (d.input_id !== timingIds.input) continue; d.turn_timing = { accepted_at: timingStart, terminal_at: "2026-10-09T10:00:12.123Z" }; const row = create(ResourceSchema, { ...original, revision: original.revision + 1n, documentJson: encode(d) }); resources.set(row.id, row); events.push(createEvent(row.id, row.revision)); }
    wake?.();
  },
} });
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
  router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: { session, state: BudgetState.ALLOW_INCOMPLETE } }) });
  router.service(ResourceService, {
    getSnapshot: () => { timingReads++; return ({ resources: [session], cursor: "fixture-original" }); },
    getResource: request => { timingReads++; return ({ resource: request.id === session.id ? session : resources.get(request.id) }); },
    listResources: request => { timingReads++; return ({ resources: request.filter?.kind === EntityKind.MESSAGE ? historical : [] }); },
    async *watchEvents(_request, context) {
      while (!context.signal.aborted) {
        if (timingFailed) throw new ConnectError("Synthetic timing stream unavailable", Code.Unavailable);
        if (events.length) { yield events.shift()!; continue; }
        await new Promise<void>(resolve => {
          const finish = () => { context.signal.removeEventListener("abort", finish); wake = undefined; resolve(); };
          wake = finish; context.signal.addEventListener("abort", finish, { once: true });
          if (context.signal.aborted) finish();
        });
      }
    },
  });
});
function Fixture() {
  const [draft, setDraft] = useState("Retained synthetic draft");
  return <div className="app transcript-role-fixture" data-fixture="__transcriptRoleFixture"><aside><h1>DeliDev</h1><button onClick={publish}>Fixture stream revision</button></aside><main><div className="session-container"><SessionView id={session.id} draft={draft} setDraft={setDraft} /></div></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MutationIntents><Fixture /></MutationIntents></QueryClientProvider></TransportProvider>);
