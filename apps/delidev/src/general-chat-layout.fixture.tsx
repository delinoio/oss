// SPDX-License-Identifier: Apache-2.0
// Synthetic creation geometry and draft checks, without native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState, useRef } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SkillService, SkillProvenance, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { QueuedInput } from "./queue";
import { useSkillCompletion } from "./skill-completion";
import { NewSession, NewSessionKind } from "./new-session";
import "./themes.css";
import "./styles.css";
import "./session.css";
import "./general-chat-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
const make = (kind: EntityKind, name: string) => create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name }) });
const agent = make(EntityKind.AGENT, "Agent One"), machine = make(EntityKind.MACHINE, "Worker One"), project = make(EntityKind.PROJECT, "Project One");
const skillCompletion = args.get("skills") === "true";
const inventoryId = newRequestId(), workerDeviceId = newRequestId();
const skills = ["add-issue", "long-name-".repeat(30), ...Array.from({ length: 12 }, (_, index) => `other-${index}`)].map((name, index) => ({ name, description: index === 1 ? "" : "Evidence-driven GitHub issue creation ".repeat(20), provenance: index % 2 ? SkillProvenance.PROJECT : SkillProvenance.USER, selection: { inventoryId, workerDeviceId, skillId: newRequestId(), contentRevision: "a".repeat(64) } }));
const rows = [agent, machine, project], creates: Record<string, unknown>[] = [];
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let report: (() => void) | undefined;
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1, ...(skillCompletion ? [SystemCapability.NATIVE_SKILLS_V1] : [])] }) });
  router.service(SkillService, { listSkills: () => ({ skills }) });
  router.service(ResourceService, { getResource: request => ({ resource: rows.find(row => row.id === request.id && row.kind === request.kind) }), listResources: request => ({ resources: rows.filter(row => row.kind === request.filter?.kind) }) });
  router.service(SessionService, { createSession: request => {
    const data = JSON.parse(new TextDecoder().decode(request.documentJson)); creates.push(data); report?.();
    const session = make(EntityKind.SESSION, "Synthetic accepted conversation"); session.sessionId = session.id;
    return { change: { session } };
  } });
});
const session = make(EntityKind.SESSION, "Synthetic session"); session.sessionId = session.id; session.documentJson = encode({ machine_id: machine.id, agent_id: agent.id, archive: "active", outcome: "stopped" });
const queued = make(EntityKind.QUEUE, "Synthetic queue"); queued.sessionId = session.id; queued.documentJson = encode({ prompt: "Original queue draft", delivery: "queued", mode: "execute", sequence: 1 });
function Followup() {
  const [value, change] = useState(""), textarea = useRef<HTMLTextAreaElement>(null);
  const completion = useSkillCompletion({ value, change, textarea, machineId: machine.id, agentId: agent.id, sessionId: session.id });
  return <form className="composer" data-shared="follow-up" onSubmit={event => { event.preventDefault(); creates.push({ forbidden: true }); report?.(); }}><textarea ref={textarea} value={value} onChange={event => completion.onChange(event.target.value, event.target.selectionStart)} onSelect={completion.onSelect} onKeyDown={completion.onKeyDown} onCompositionStart={completion.onCompositionStart} onCompositionEnd={completion.onCompositionEnd} {...completion.attributes}/>{completion.list}</form>;
}
function Fixture() {
  const [active, setActive] = useState<NewSessionKind | undefined>(NewSessionKind.GeneralChat), [identity, setIdentity] = useState(0), [count, setCount] = useState(0);
  report = () => setCount(creates.length);
  return <div className="app general-chat-fixture" data-fixture="__generalChatFixture"><aside><h1>DeliDev</h1>
    <button onClick={() => setActive(NewSessionKind.GeneralChat)}>Fixture General Chat</button><button onClick={() => setActive(NewSessionKind.Session)}>Fixture New session</button>
    <button onClick={() => setActive(undefined)}>Fixture Settings</button><button onClick={() => { setActive(undefined); setTimeout(() => setActive(NewSessionKind.GeneralChat), 10); }}>Fixture reconnect</button>
    <button onClick={() => setIdentity(value => value + 1)}>Fixture identity</button>
    <output data-fixture-creates={count}>{JSON.stringify(creates.at(-1) ?? {})}</output>
  </aside><main><MutationIntents key={identity}>
    {args.get("shared") === "true" ? <section className="new-session-content" style={{ margin: "24px auto" }}><Followup/><div data-shared="queue"><QueuedInput resource={queued} session={session} refresh={() => {}} /></div></section> : null}
    <NewSession kind={NewSessionKind.GeneralChat} active={active === NewSessionKind.GeneralChat} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    <NewSession active={active === NewSessionKind.Session} ownsActivation activation={1} back={() => {}} openSettings={() => setActive(undefined)} open={() => {}} created={() => {}} />
    {active === undefined ? <h2>Synthetic Settings</h2> : null}
  </MutationIntents></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
