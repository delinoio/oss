// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { ConnectError, Code, createRouterTransport } from "@connectrpc/connect";
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

it("keeps global fetch denial effective even with an enabled project override", async () => {
 const defaults = { ...newConfiguration(EntityKind.SETTINGS), automatic_fetch: false };
 const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, revision: 1n, schemaVersion: 2, documentJson: new TextEncoder().encode(JSON.stringify(defaults)) });
 const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => ({ resources: [row] }) }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ProjectBehaviorFields data={{ settings: { automatic_fetch: "enabled" } }} change={() => {}} active /></QueryClientProvider></TransportProvider>);
 const input = screen.getByLabelText("Allow automatic fetch before Worktree preparation");
 await waitFor(() => expect(input.parentElement?.parentElement?.textContent).toContain("Effective value: Disabled"));
 });

it("opens the exact sidebar project and retains its draft through a resource refresh", async () => {
 const projectId = newRequestId();
 let row = create(ResourceSchema, { id: projectId, kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 2, documentJson: new TextEncoder().encode(JSON.stringify({ ...newConfiguration(EntityKind.PROJECT), name: "Original project" })) });
 const get = vi.fn((_request: { kind: EntityKind; id: string }) => ({ resource: row }));
 const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1] }) });
 router.service(ResourceService, { getResource: get, listResources: () => ({ resources: [] }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const { ProjectSettingsMenu } = await import("./project-settings-menu");
 const { MutationIntents } = await import("./mutation");
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ProjectSettingsMenu projectId={projectId} label="Original project" active /></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByRole("button", { name: "Project settings · Original project" }));
 const name = await screen.findByLabelText("Name") as HTMLInputElement;
 expect(get.mock.calls[0][0]).toMatchObject({ kind: EntityKind.PROJECT, id: projectId });
 fireEvent.change(name, { target: { value: "Retained draft" } });
 row = { ...row, revision: 2n, documentJson: new TextEncoder().encode(JSON.stringify({ ...newConfiguration(EntityKind.PROJECT), name: "External replacement" })) };
 await client.invalidateQueries();
 expect(name.value).toBe("Retained draft");
 expect(screen.getAllByRole("dialog")).toHaveLength(1);
 });


it.each(["failed refresh", "incomplete empty page"])("does not project defaults from a cached empty Settings inventory after %s", async state => {
 let invalid = false;
 const transport = createRouterTransport(router => router.service(ResourceService, { listResources: () => {
  if (invalid && state === "failed refresh") throw new ConnectError("Settings observation unavailable", Code.Unavailable);
  return { resources: [], nextPageToken: invalid ? "original-continuation" : "" };
 } }));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 function Fixture() {
  const [data, setData] = useState<Document>({ name: "retained draft", settings: { automatic_plan_approval: "disabled" } });
  return <><ProjectBehaviorFields data={data} change={setData} active /><output aria-label="retained project">{JSON.stringify(data)}</output></>;
 }
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
 await screen.findByText("Effective value: sequential-exhaustion");
 const fetch = screen.getByLabelText("Allow automatic fetch before Worktree preparation");
 const policy = screen.getByLabelText("Pull request remediation policy");
 expect((screen.getByRole("option", { name: "Project override" }) as HTMLOptionElement).disabled).toBe(false);
 invalid = true;
 await client.invalidateQueries();
 await waitFor(() => expect((screen.getByRole("option", { name: "Project override" }) as HTMLOptionElement).disabled).toBe(true));
 expect(screen.getAllByText("Effective value: Unavailable")).toHaveLength(2);
 expect(screen.getByText("Effective value: Disabled")).toBeTruthy();
 expect(screen.getByLabelText("Allow automatic fetch before Worktree preparation")).toBe(fetch);
 expect(screen.getByLabelText("Pull request remediation policy")).toBe(policy);
 expect(screen.getByLabelText("retained project").textContent).toBe('{"name":"retained draft","settings":{"automatic_plan_approval":"disabled"}}');
 fireEvent.change(policy, { target: { value: "explicit" } });
 expect(screen.getByLabelText("retained project").textContent).not.toContain('"remediation"');
});
