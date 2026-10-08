// SPDX-License-Identifier: Apache-2.0
// Synthetic creation geometry and draft checks, without native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { NewSession, NewSessionKind } from "./new-session";
import "./themes.css";
import "./styles.css";
import "./general-chat-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
const make = (kind: EntityKind, name: string) => create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name }) });
const agent = make(EntityKind.AGENT, "Agent One"), machine = make(EntityKind.MACHINE, "Worker One"), project = make(EntityKind.PROJECT, "Project One");
const rows = [agent, machine, project], creates: Record<string, unknown>[] = [];
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let report: (() => void) | undefined;
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1] }) });
  router.service(ResourceService, { getResource: request => ({ resource: rows.find(row => row.id === request.id && row.kind === request.kind) }), listResources: request => ({ resources: rows.filter(row => row.kind === request.filter?.kind) }) });
  router.service(SessionService, { createSession: request => {
    const data = JSON.parse(new TextDecoder().decode(request.documentJson)); creates.push(data); report?.();
    const session = make(EntityKind.SESSION, "Synthetic accepted conversation"); session.sessionId = session.id;
    return { change: { session } };
  } });
});
function Fixture() {
  const [active, setActive] = useState<NewSessionKind | undefined>(NewSessionKind.GeneralChat), [identity, setIdentity] = useState(0), [count, setCount] = useState(0);
  report = () => setCount(creates.length);
  return <div className="app general-chat-fixture" data-fixture="__generalChatFixture"><aside><h1>DeliDev</h1>
    <button onClick={() => setActive(NewSessionKind.GeneralChat)}>Fixture General Chat</button><button onClick={() => setActive(NewSessionKind.Session)}>Fixture New session</button>
    <button onClick={() => setActive(undefined)}>Fixture Settings</button><button onClick={() => { setActive(undefined); setTimeout(() => setActive(NewSessionKind.GeneralChat), 10); }}>Fixture reconnect</button>
    <button onClick={() => setIdentity(value => value + 1)}>Fixture identity</button>
    <output data-fixture-creates={count}>{JSON.stringify(creates.at(-1) ?? {})}</output>
  </aside><main><MutationIntents key={identity}>
    <NewSession kind={NewSessionKind.GeneralChat} active={active === NewSessionKind.GeneralChat} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    <NewSession active={active === NewSessionKind.Session} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    {active === undefined ? <h2>Synthetic Settings</h2> : null}
  </MutationIntents></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
