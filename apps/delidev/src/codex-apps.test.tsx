// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CodexAppsService, EntityKind, ResourceSchema, newRequestId, type Resource, type SelectCodexAppsRequest, type InspectCodexAppsRequest, type RevokeCodexAppsRequest } from "@delinoio/delidev-api-client";
import { CodexAppsPanel, codexAppsView } from "./codex-apps";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
function fixture({ configured = true, live = false }: { configured?: boolean; live?: boolean } = {}) {
  const sessionId = newRequestId(), accountId = newRequestId(), execution = newRequestId(), job = newRequestId(), machine = newRequestId(), instance = newRequestId(), thread = newRequestId();
  const session = create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 2n, documentJson: encode({ archive: "active", recovery: "none", pending_inputs: 0, ...(live ? { active_execution_id: execution } : {}), initial_execution: { initial_account_id: accountId, configuration: { harness: "codex" } }, execution: { execution_id: execution, native_thread_id: thread, cleanup_verified: !live } }) });
  const account = create(ResourceSchema, { id: accountId, kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 3n });
  const metadata = (body: object, id = newRequestId()) => create(ResourceSchema, { id, kind: EntityKind.UNSPECIFIED, sessionId, schemaVersion: 1, revision: 1n, documentJson: encode(body) });
  let config = { version: 1, session_id: sessionId, account_id: accountId, generation: newRequestId(), app_ids: ["calendar"] };
  const app = { id: "calendar", name: "Calendar", discovered: true, accessible: true, installed: true, enabled: true, callable: true, selected: true };
  let inventory = { operation_id: newRequestId(), claim_id: newRequestId(), native_catalog_refresh_verified: true, version: 1, session_id: sessionId, account_id: accountId, configuration_generation: config.generation, execution_id: execution, execution_job_id: job, machine_id: machine, instance_id: instance, native_thread_id: thread, observed_at: "2026-10-10T01:00:00Z", apps: [app, { ...app, id: "notes", name: "Notes", installed: false, enabled: false, callable: false, selected: false }] };
  let reply: { configuration?: Resource; inventory?: Resource; operation?: Resource } = configured ? { configuration: metadata(config, config.generation), inventory: metadata(inventory) } : {};
  const read = vi.fn(() => reply);
  const select = vi.fn(async (request: SelectCodexAppsRequest) => {
    config = { ...config, generation: request.mutation!.requestId, app_ids: [...request.appIds] };
    reply = { configuration: metadata(config, config.generation) };
    return reply;
  });
  const inspect = vi.fn(async (request: InspectCodexAppsRequest) => {
    const operation = { version: 1, id: newRequestId(), revision: "1", request_id: request.mutation!.requestId, actor_id: newRequestId(), action: "inspect", state: "queued", original: config, execution_id: execution, execution_job_id: job, machine_id: machine, instance_id: instance, native_thread_id: thread };
    reply = { ...reply, operation: metadata(operation, operation.id) }; return reply;
  });
  const revoke = vi.fn(async (request: RevokeCodexAppsRequest) => {
    const operation = { version: 1, id: newRequestId(), revision: "1", request_id: request.mutation!.requestId, actor_id: newRequestId(), action: "revoke", state: "queued", original: config, next: { ...config, generation: request.mutation!.requestId, app_ids: config.app_ids.filter(v => !request.appIds.includes(v)) }, execution_id: execution, execution_job_id: job, machine_id: machine, instance_id: instance, native_thread_id: thread };
    reply = { ...reply, operation: metadata(operation, operation.id) }; return reply;
  });
  const transport = createRouterTransport(router => router.service(CodexAppsService, { getCodexApps: read, selectCodexApps: select, inspectCodexApps: inspect, revokeCodexApps: revoke }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const onClose = vi.fn();
  return { session, account, client, select, inspect, revoke, onClose, metadata, get config() { return config; }, get inventory() { return inventory; }, set reply(value: typeof reply) { reply = value; }, get reply() { return reply; }, render: (source = session, owner = account) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><CodexAppsPanel session={source} account={owner} onClose={onClose} /></MutationIntents></QueryClientProvider></TransportProvider> };
}
it("keeps discovery and callability separate and creates only an explicit empty first configuration", async () => {
  const f = fixture({ configured: false }); render(f.render());
  await screen.findByText("No Apps selection is configured. Creating an empty configuration grants no app access.");
  expect(f.select).not.toHaveBeenCalled(); expect(f.inspect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Create empty Apps configuration" }));
  fireEvent.click(screen.getByRole("button", { name: "Save next-execution selection" }));
  await waitFor(() => expect(f.select).toHaveBeenCalledTimes(1));
  expect(f.select.mock.calls[0][0]).toMatchObject({ mutation: { id: f.session.id, expectedRevision: 2n }, accountId: f.account.id, accountRevision: 3n, configurationGeneration: "", appIds: [] });
});
it("retains independent native availability flags without granting installation from selection", async () => {
  const f = fixture(); render(f.render());
  const notes = await screen.findByRole("region", { name: "Notes" });
  expect(notes.textContent).toContain("InstalledNo"); expect(notes.textContent).toContain("Callable on original controllerNo");
  expect(f.select).not.toHaveBeenCalled(); expect(f.inspect).not.toHaveBeenCalled();
  expect(screen.queryByText(f.account.id)).toBeNull();
});
it("uses only removal IDs for the current live generation and locks native pending operations", async () => {
  const f = fixture({ live: true }); render(f.render());
  await screen.findByRole("region", { name: "Calendar" });
  expect((screen.getByRole("button", { name: "Edit next-execution selection" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("checkbox", { name: "Calendar" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove chosen Apps" }));
  await waitFor(() => expect(f.revoke).toHaveBeenCalledTimes(1));
  expect(f.revoke.mock.calls[0][0]).toMatchObject({ accountId: f.account.id, configurationGeneration: f.config.generation, appIds: ["calendar"] });
  await screen.findByText("The original Apps operation is queued.");
  expect((screen.getByRole("button", { name: "Inspect original Apps" }) as HTMLButtonElement).disabled).toBe(true);
});
it("retains exact uncertain Inspect bytes across saved reads and never automatically repeats a native request", async () => {
  const f = fixture({ live: true }); f.inspect.mockRejectedValueOnce(new ConnectError("Original receipt lost", Code.Unavailable)); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Inspect original Apps" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Inspect original Apps" }));
  await screen.findByRole("button", { name: "Retry same Apps request" });
  const original = f.inspect.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Reload saved Apps state" }));
  await waitFor(() => expect(f.inspect).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Retry same Apps request" }));
  await waitFor(() => expect(f.inspect).toHaveBeenCalledTimes(2));
  expect(f.inspect.mock.calls[1][0]).toEqual(original);
});
it("rejects foreign account projections and callable-without-installation inventory", () => {
  const f = fixture();
  expect(codexAppsView({ configuration: f.metadata({ ...f.config, account_id: newRequestId() }, f.config.generation) }, f.session.id, f.account.id).invalid).toBe(true);
  expect(codexAppsView({ inventory: f.metadata({ ...f.inventory, apps: [{ ...f.inventory.apps[0], installed: false }] }) }, f.session.id, f.account.id).invalid).toBe(true);
});
it("keeps a changed original session revision from silently rebasing positive selection", async () => {
  const f = fixture(); const rendered = render(f.render());
  await screen.findByRole("region", { name: "Calendar" });
  fireEvent.click(screen.getByRole("button", { name: "Edit next-execution selection" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Notes" }));
  rendered.rerender(f.render(create(ResourceSchema, { ...f.session, revision: 4n })));
  expect((screen.getByRole("button", { name: "Save next-execution selection" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.select).not.toHaveBeenCalled();
});
it("rejects a selection response that does not bind the original request generation", async () => {
  const f = fixture(); f.select.mockImplementationOnce(async () => f.reply); render(f.render());
  await screen.findByRole("region", { name: "Calendar" });
  fireEvent.click(screen.getByRole("button", { name: "Edit next-execution selection" }));
  fireEvent.click(screen.getByRole("button", { name: "Save next-execution selection" }));
  await screen.findByRole("button", { name: "Retry same Apps request" });
  expect(f.select).toHaveBeenCalledTimes(1);
  expect((screen.getByRole("button", { name: "Save next-execution selection" }) as HTMLButtonElement).disabled).toBe(true);
});
it("freezes a removal draft to its original revision instead of retargeting after refresh", async () => {
  const f = fixture({ live: true }); const rendered = render(f.render());
  await screen.findByRole("region", { name: "Calendar" });
  fireEvent.click(screen.getByRole("checkbox", { name: "Calendar" }));
  rendered.rerender(f.render(create(ResourceSchema, { ...f.session, revision: 5n })));
  expect((screen.getByRole("button", { name: "Remove chosen Apps" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.revoke).not.toHaveBeenCalled();
});
it("does not transfer availability or request authority to another account", async () => {
  const f = fixture({ live: true }); render(f.render(f.session, create(ResourceSchema, { ...f.account, id: newRequestId() })));
  await screen.findByText("The original Apps scope or returned metadata is unavailable. No new action is authorized.");
  expect((screen.getByRole("button", { name: "Inspect original Apps" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.inspect).not.toHaveBeenCalled(); expect(f.select).not.toHaveBeenCalled(); expect(f.revoke).not.toHaveBeenCalled();
});
it("requires native catalog refresh and original claim proof in inventory metadata", () => {
  const f = fixture();
  for (const patch of [{ native_catalog_refresh_verified: false }, { claim_id: undefined }, { operation_id: newRequestId(), claim_id: "foreign" }]) expect(codexAppsView({ inventory: f.metadata({ ...f.inventory, ...patch }) }, f.session.id, f.account.id).invalid).toBe(true);
});
