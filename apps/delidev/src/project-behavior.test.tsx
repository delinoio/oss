// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceService, ResourceSchema, ConfigurationService, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { ProjectBehaviorFields, newConfiguration } from "./configuration-fields";
import { type Document } from "./documents";
it("retains explicit disabled overrides and restores continuous global inheritance", async () => {
 const defaults = { ...newConfiguration(EntityKind.SETTINGS), automatic_plan_approval: true };
 const resource = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, revision: 1n, schemaVersion: 2, documentJson: new TextEncoder().encode(JSON.stringify(defaults)) });
 const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [resource] }) }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 function Fixture() { const [data,setData]=useState<Document>({settings:{}});return <><ProjectBehaviorFields data={data} change={setData} active /><output aria-label="retained settings">{JSON.stringify(data.settings)}</output></>; }
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
 await waitFor(() => expect(screen.getAllByText("Effective value: Enabled").length).toBe(2));
 fireEvent.change(screen.getByLabelText("Automatically approve native plans"),{target:{value:"disabled"}});
 expect(screen.getByLabelText("retained settings").textContent).toContain('"automatic_plan_approval":"disabled"');
 fireEvent.change(screen.getByLabelText("Pull request remediation policy"),{target:{value:"explicit"}});
 expect(screen.getByLabelText("retained settings").textContent).toContain('"remediation"');
 fireEvent.click(screen.getByRole("button",{name:"Return all settings to global defaults"}));
 expect(screen.getByLabelText("retained settings").textContent).not.toContain('"remediation"');
 expect(screen.getByLabelText("retained settings").textContent).toContain('"automatic_plan_approval":"inherit"');
 });

it.each([false, true])("preserves legacy saves and negotiates schema 2 when supported (%s)", async supported => {
 const legacy = { ...newConfiguration(EntityKind.SETTINGS) }; delete legacy.automatic_plan_approval;
 const initial = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, revision: 1n, schemaVersion: 1, documentJson: new TextEncoder().encode(JSON.stringify(legacy)) });
 const save = vi.fn(async request => ({ resource: create(ResourceSchema, { ...initial, revision: 2n, schemaVersion: request.schemaVersion, documentJson: request.documentJson }) }));
 const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: supported ? [SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1] : [] }) });
 router.service(ResourceService, { getResource: () => ({ resource: initial }), listResources: () => ({ resources: [] }) });
 router.service(ConfigurationService, { saveConfiguration: save });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const { ConfigurationEditor } = await import("./settings");
 const { MutationIntents } = await import("./mutation");
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationEditor kind={EntityKind.SETTINGS} initial={initial} active saved={() => {}} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
 if (supported) fireEvent.click(await screen.findByRole("checkbox", { name: "Automatically approve native plans" }));
 fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
 await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
 const request = save.mock.calls[0][0];
 const document = JSON.parse(new TextDecoder().decode(request.documentJson));
 expect(request.schemaVersion).toBe(supported ? 2 : 1);
 expect(document.notifications).toBe(true);
 expect(document.remediation).toEqual(legacy.remediation);
 expect(document.automatic_plan_approval).toBe(supported ? true : undefined);
 });
