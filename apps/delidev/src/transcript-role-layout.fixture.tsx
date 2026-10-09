// SPDX-License-Identifier: Apache-2.0
// Synthetic presentation only. This entry has no native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
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
const session = create(ResourceSchema, { id: transcriptSessionId, sessionId: transcriptSessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Synthetic conversation", workspace: args.get("workspace") ?? "general-chat", archive: "active", outcome: "stopped", dispatch: "blocked", recovery: "none" }) });
const data = transcriptRoleFixtures();
const historical = [...Object.values(data), { role: "tool", state: "complete", text: "Original tool card" }, { ...data.grokUser, native_id: "foreign" }, { ...data.claude, claude: { invalid: true } }].map((value, index) => transcriptResource(value, index + 2));
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
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
  router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: { session, state: BudgetState.ALLOW_INCOMPLETE } }) });
  router.service(ResourceService, {
    getSnapshot: () => ({ resources: [session], cursor: "fixture-original" }),
    getResource: request => ({ resource: request.id === session.id ? session : resources.get(request.id) }),
    listResources: request => ({ resources: request.filter?.kind === EntityKind.MESSAGE ? historical : [] }),
    async *watchEvents(_request, context) {
      while (!context.signal.aborted) {
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
