// SPDX-License-Identifier: Apache-2.0
// Synthetic original session failure; no native, account or external authority.
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
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
const execution = newRequestId(), job = newRequestId(), id = newRequestId();
const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ name: "Original session", workspace: "general-chat", archive: "active", outcome: "failed", dispatch: "paused", recovery: "required", initial_execution: { id: execution }, execution: { execution_id: execution }, startup: { job_id: job, execution_id: execution, failure: { state: 3, phase: 5, harness: "codex", problem_code: "recovery_required", correlation_id: job, input_delivery: 4, cleanup: 2 } } }) });
const writes = () => { document.documentElement.dataset.fixtureWrites = String(Number(document.documentElement.dataset.fixtureWrites ?? 0) + 1); return { change: { session } }; };
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
 router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: { session } }), controlSession: writes, recoverSessionExecution: writes, enqueueInput: writes });
 router.service(ResourceService, { getSnapshot: () => ({ resources: [session], cursor: "synthetic" }), listResources: () => ({ resources: [] }), async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(done => context.signal.addEventListener("abort", () => done(), { once: true })); } });
});
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
function Fixture() { const [draft, setDraft] = useState("Retained draft"); return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><div className="session-container fixture-session-owner"><SessionView id={id} draft={draft} setDraft={setDraft} /></div></MutationIntents></QueryClientProvider></TransportProvider>; }
void i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en").then(() => createRoot(document.getElementById("root")!).render(<Fixture />));
