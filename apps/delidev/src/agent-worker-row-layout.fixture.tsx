// SPDX-License-Identifier: Apache-2.0
// Synthetic read-only row fixture; excluded from the production entry point.
import { useState } from "react";
import { ResourceChoice } from "./configuration-fields";
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { AgentWorkerRow } from "./agent-worker-row";
import { encode } from "./documents";
import { i18n, copy } from "./localization";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
if (args.get("theme") !== "system") document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
const models = ["first-native-".repeat(18), "second-native"].map(native => ({ native_id: native, name: "Display name " + native.slice(0, 220) }));
const rows = ["codex", "claude-code", "opencode", "grok-build", "unknown", "future"].map((harness, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: harness === "future" ? 99 : 4, revision: 1n, documentJson: encode({ name: harness === "codex" ? "Complete-Worker-name-".repeat(10) : `Worker ${harness}`, harness, ...(index === 2 ? { health: "Fixture status", reconfiguration_required: true } : {}), ...(index === 0 ? { routes: [models[0], models[1], models[0]].map(model => ({ model: {provider_id:newRequestId(),native_id:model.native_id,name:model.name,input_modalities:["text"],metadata_source:"unknown"}, accounts: [{ id: newRequestId(), weight: 1 }] })) } : { routes: [{model:{provider_id:newRequestId(),native_id:"original-native",input_modalities:["text"],metadata_source:"unknown"},accounts:[{id:newRequestId(),weight:1}]}] }) }) }));
const selectorRows = [...rows.filter(row => row.schemaVersion !== 99), create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Worker missing harness" }) })];
function AgentSelector() {
  const [value, setValue] = useState(rows[0].id);
  return <section aria-label="Agent picker"><ResourceChoice label={copy("configuration-fields.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={value} active change={setValue} /></section>;
}
const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: selectorRows }), getResource: request => { if (request.kind === EntityKind.AGENT) { document.documentElement.dataset.agentReads = String(Number(document.documentElement.dataset.agentReads ?? 0) + 1); return { resource: selectorRows.find(row => row.id === request.id) }; } document.documentElement.dataset.modelReads = String(Number(document.documentElement.dataset.modelReads ?? 0) + 1); return { resource: models.find(model => model.id === request.id) }; } }));
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const noop = () => {};
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}><TransportProvider transport={transport}><main className="settings-content" aria-label="Worker fixture"><div className="settings-content-column"><AgentSelector /><div className="settings-agent-list" style={args.has("availableWidth") ? { width: `${Number(args.get("availableWidth")) + 32}px`, maxWidth: "100%" } : undefined}>{rows.map(row => <AgentWorkerRow key={row.id} row={row} edit={noop} preview={noop} remove={noop} />)}</div></div></main></TransportProvider></QueryClientProvider>);
