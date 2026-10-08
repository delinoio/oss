// SPDX-License-Identifier: Apache-2.0
// Full retained Session workspace with synthetic services, never native authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionView } from "./session";
import { MutationIntents } from "./mutation";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./session-remediation-layout.fixture.css";
const args = new URLSearchParams(location.search);
if (args.get("theme") !== "system") document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
const id = newRequestId();
const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained historical session 세션 이름 with complete long wrapping identity", workspace: "general-chat", outcome: "stopped", dispatch: "paused", recovery: "none", archive: "active", name_mode: "automatic", title_state: "unsupported", title_reason: "worker-capability-absent", initial_execution: { configuration: { harness: args.get("harness") || "codex" } }, fork: { snapshot: { configuration: { harness: "claude-code" } } } }) });
const counts = { reads: 0, mutations: 0 };
Object.assign(window, { __sessionHeaderFixture: { counts, language: (language: string) => i18n.changeLanguage(language) } });
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => { counts.reads++; return { capabilities: [] }; } });
 router.service(SessionService, { listQueue: () => { counts.reads++; return { inputs: [] }; }, getSessionBudget: () => { counts.reads++; return { view: { session } }; }, controlSession: () => { counts.mutations++; return { change: { session } }; } });
 router.service(ResourceService, { getSnapshot: () => { counts.reads++; return { resources: [session], cursor: "synthetic" }; }, listResources: () => { counts.reads++; return { resources: [] }; }, async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(done => context.signal.addEventListener("abort", () => done(), { once: true })); } });
});
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
function Fixture() { const [draft, setDraft] = useState(""); return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><div className="session-container fixture-session-owner"><SessionView id={id} draft={draft} setDraft={setDraft} /></div></MutationIntents></QueryClientProvider></TransportProvider>; }
void i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en").then(() => createRoot(document.getElementById("root")!).render(<Fixture />));
