// SPDX-License-Identifier: Apache-2.0
// Synthetic creation geometry and draft checks, without native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BudgetState, EntityKind, SessionBudgetViewSchema, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";
import { NewSession, NewSessionKind } from "./new-session";
import "./themes.css";
import "./styles.css";
import "./plan-mode-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
const make = (kind: EntityKind, name: string) => create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name }) });
const agent = make(EntityKind.AGENT, "Agent One"), machine = make(EntityKind.MACHINE, "Worker One"), project = make(EntityKind.PROJECT, "Project One");
const session = make(EntityKind.SESSION, "Synthetic session"); session.sessionId = session.id; session.documentJson = encode({ name: "Synthetic session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "blocked", recovery: "none" });
const rows = [agent, machine, project, session], requests: { operation: string; requestId: string; document: Record<string, unknown> }[] = [];
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
let report: (() => void) | undefined, outcome = "success", finish: (() => void) | undefined;
const capture = async (operation: string, request: { requestId: string; documentJson: Uint8Array }) => {
  requests.push({ operation, requestId: request.requestId, document: JSON.parse(new TextDecoder().decode(request.documentJson)) }); report?.();
  if (outcome === "uncertain") throw new ConnectError("Synthetic lost receipt", Code.Unavailable);
  if (outcome === "pending") await new Promise<void>(resolve => { finish = resolve; });
  return { change: { session } };
};
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1] }) });
  router.service(ResourceService, { getResource: request => ({ resource: rows.find(row => row.id === request.id && row.kind === request.kind) }), listResources: request => ({ resources: rows.filter(row => row.kind === request.filter?.kind) }), getSnapshot: () => ({ resources: [session], cursor: "synthetic-original" }), async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true })); } });
  router.service(SessionService, { createSession: request => capture("create", request), enqueueInput: request => capture("enqueue", request), listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: create(SessionBudgetViewSchema, { session, state: BudgetState.ALLOW_INCOMPLETE }) }) });
});
function Fixture() {
  const [active, setActive] = useState<NewSessionKind | "existing" | undefined>(NewSessionKind.GeneralChat), [identity, setIdentity] = useState(0), [count, setCount] = useState(0), [draft, setDraft] = useState("Retained synthetic draft");
  report = () => setCount(requests.length);
  return <div className="app plan-mode-fixture" data-fixture="__planModeFixture"><aside><h1>DeliDev</h1>
    <button onClick={() => setActive(NewSessionKind.GeneralChat)}>Fixture General Chat</button><button onClick={() => setActive(NewSessionKind.Session)}>Fixture New session</button>
    <button onClick={() => setActive("existing")}>Fixture existing session</button><button onClick={() => { outcome = "pending"; }}>Fixture hold request</button><button onClick={() => { outcome = "success"; finish?.(); finish = undefined; }}>Fixture accept request</button><button onClick={() => { outcome = "uncertain"; }}>Fixture fail request</button><button onClick={() => setActive(undefined)}>Fixture Settings</button><button onClick={() => { setActive(undefined); setTimeout(() => setActive(NewSessionKind.GeneralChat), 10); }}>Fixture reconnect</button>
    <button onClick={() => setIdentity(value => value + 1)}>Fixture identity</button>
    <output data-fixture-requests={count}>{JSON.stringify(requests)}</output>
  </aside><main><MutationIntents key={identity}>
    <NewSession kind={NewSessionKind.GeneralChat} active={active === NewSessionKind.GeneralChat} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    <NewSession active={active === NewSessionKind.Session} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    <div className="plan-mode-session" hidden={active !== "existing"}><SessionView id={session.id} draft={draft} setDraft={setDraft} /></div>
    {active === undefined ? <h2>Synthetic Settings</h2> : null}
  </MutationIntents></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
