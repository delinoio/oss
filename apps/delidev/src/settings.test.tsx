import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderService, ResourceSchema, ResourceService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Settings, ConfigurationEditor } from "./settings";
import { AccountConnection } from "./account-connection";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";

function resource(kind: EntityKind, value: Document, revision = 1n) { return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision, documentJson: encode(value) }); }
function fixture(resources: Resource[]) {
  const save = vi.fn(async (_request: unknown): Promise<{ resource?: Resource; job?: Resource }> => ({ resource: resources[0] }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const preview = vi.fn(async (_request: unknown) => ({ routeJson: encode({ policy: "remaining-quota", selected: "", candidates: [] }) }));
  const inspect = vi.fn(async (_request: unknown) => ({ job: resources.find((row) => row.kind === EntityKind.JOB) }));
  const connect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const disconnect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save, deleteConfiguration: remove, previewRouting: preview });
    router.service(WorkerService, { inspectRepository: inspect });
    router.service(ResourceService, { listResources: (request) => ({ resources: resources.filter((row) => row.kind === request.filter?.kind) }), getResource: (request) => ({ resource: resources.find((row) => row.id === request.id) }) });
    router.service(AccountService, { getAccountStatus: (request) => ({ account: resources.find((row) => row.id === request.id) }), connectAccount: connect, disconnectAccount: disconnect });
    router.service(ProviderService, { listProviderPresets: () => ({ presetsJson: encode([{ id: "ollama", provider: { name: "Local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true }, key_guidance: "Run your local model server first.", compatibility: "Requires a compatible model." }]) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { resources, save, remove, preview, inspect, connect, disconnect, client, view };
}
function input(value: unknown) { return value as { mutation: { requestId: string; expectedRevision: bigint }; documentJson: Uint8Array }; }

it("keeps a settings draft across closing the modal and retries the original provider document", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(new ConnectError("acknowledgement lost", Code.Unavailable));
  const view = render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New Provider" }));
  await screen.findByRole("option", { name: "Local provider" });
  fireEvent.change(screen.getByRole("combobox", { name: "Provider preset" }), { target: { value: "ollama" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "My local provider" } });
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings visible close={() => {}} />));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("My local provider");
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(0n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "My local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true });
});

it("preserves server-owned account observations during a preference edit", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const observed = { alias: "Original", provider_id: provider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "ready", connection: { id: newRequestId(), authentication: "bearer", connected_at: "2026-09-25T00:00:00Z" }, quota: [{ id: "window", remaining: 0 }], confirmed_exhausted: true, validation: { state: "observed" }, catalog: { state: "stale" } };
  const account = resource(EntityKind.ACCOUNT, observed, 7n);
  const value = fixture([account, provider]);
  render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Account alias" }), { target: { value: "Renamed" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(7n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ ...observed, alias: "Renamed" });
});

it("blocks stale settings writes without erasing the staged instructions", async () => {
  const initial = resource(EntityKind.TEMPLATE, { name: "Instructions", contents: "Original instructions" }, 3n);
  const value = fixture([create(ResourceSchema, { ...initial, revision: 4n })]);
  render(value.view(<ConfigurationEditor kind={EntityKind.TEMPLATE} initial={initial} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "My staged instructions" } });
  await screen.findByText(/This entry changed elsewhere/);
  fireEvent.submit(screen.getByRole("button", { name: "Save Instructions" }).closest("form")!);
  expect(value.save).not.toHaveBeenCalled();
  expect((screen.getByRole("textbox", { name: "Instructions" }) as HTMLTextAreaElement).value).toBe("My staged instructions");
});

it("retains a secret only for its exact uncertain connection and excludes it from read cache keys", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "API provider", authentication: "bearer" });
  const account = resource(EntityKind.ACCOUNT, { alias: "API account", provider_id: provider.id, type: "api", health: "disconnected" }, 5n);
  const value = fixture([account, provider]);
  value.connect.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  const key = screen.getByLabelText("API key");
  await waitFor(() => expect((key as HTMLInputElement).disabled).toBe(false));
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect account" }));
  const retry = await screen.findByRole("button", { name: "Retry the same connection" });
  expect((key as HTMLInputElement).value).toBe("");
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("fixture-only-secret");
  fireEvent.click(retry);
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(2));
  expect(value.connect.mock.calls[0][0]).toEqual(value.connect.mock.calls[1][0]);
  expect(value.connect.mock.calls[0][0]).toMatchObject({ mutation: { expectedRevision: 5n }, keyless: false });
  expect(new TextDecoder().decode((value.connect.mock.calls[0][0] as { apiKey: Uint8Array }).apiKey)).toBe("fixture-only-secret");
  await waitFor(() => expect(value.client.getMutationCache().getAll()).toHaveLength(0));
});

it("resumes pending credential cleanup using the original server-retained mutation", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider", authentication: "bearer" });
  const requestId = newRequestId();
  const account = resource(EntityKind.ACCOUNT, { alias: "Cleanup", provider_id: provider.id, type: "api", health: "disconnected", removal: { request_id: requestId, expected_revision: 8 } }, 12n);
  const value = fixture([account, provider]);
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Retry original credential cleanup" }));
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({ mutation: { id: account.id, requestId, expectedRevision: 8n } });
  expect(value.connect).not.toHaveBeenCalled();
});

it("preserves explicit empty restrictions and requires a primary repository after removal", async () => {
  const first = resource(EntityKind.REPOSITORY, { name: "First" }), second = resource(EntityKind.REPOSITORY, { name: "Second" });
  const project = resource(EntityKind.PROJECT, { name: "Project", repositories: [first.id, second.id], primary_repository: first.id, agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
  const value = fixture([project, first, second]);
  render(value.view(<ConfigurationEditor kind={EntityKind.PROJECT} initial={project} active saved={() => {}} cancel={() => {}} />));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" }));
  expect((screen.getByRole("combobox", { name: "Primary repository" }) as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: second.id } });
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson))).toMatchObject({ repositories: [second.id], primary_repository: second.id, accounts: { configured: true, ids: [] }, agents: { configured: false, ids: [] } });
});

it("keeps repository save acknowledgment separate from completed Worker validation", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository", checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }], base: {}, starting: {}, auto_fetch: true });
  const job = resource(EntityKind.JOB, { type: "save-repository", state: "queued" });
  const value = fixture([repository, job]), saved = vi.fn();
  value.save.mockResolvedValue({ job });
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} initial={repository} active saved={saved} cancel={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await screen.findByText("Worker operation: queued");
  expect(saved).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh operation" }) as HTMLButtonElement).disabled).toBe(false));
  value.resources[1] = create(ResourceSchema, { ...job, revision: 2n, documentJson: encode({ type: "save-repository", state: "uncertain", problem: { message: "Owned operation needs recovery" } }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh operation" }));
  await screen.findByText("Worker operation: uncertain");
  expect(value.save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Return to retained draft" })).toBeNull();
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh operation" }) as HTMLButtonElement).disabled).toBe(false));
  value.resources[1] = create(ResourceSchema, { ...job, revision: 3n, documentJson: encode({ type: "save-repository", state: "succeeded", output: { id: repository.id, revision: 2 } }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh operation" }));
  fireEvent.click(await screen.findByRole("button", { name: "Done" }));
  expect(saved).toHaveBeenCalledTimes(1);
});

it("retries an original checkout inspection and uses only its owning Worker's canonical root", async () => {
  const machine = resource(EntityKind.MACHINE, { name: "Owned Worker" });
  const job = resource(EntityKind.JOB, { type: "inspect-repository", state: "succeeded", machine_id: machine.id, output: { root: "/canonical/checkout", remotes: ["origin"], default_refs: { origin: "main" } } });
  const value = fixture([machine, job]);
  value.inspect.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Repository" } });
  await screen.findByRole("option", { name: "Owned Worker" });
  fireEvent.change(screen.getByLabelText("Execution Worker"), { target: { value: machine.id } });
  fireEvent.change(screen.getByLabelText("Absolute checkout path on this Worker"), { target: { value: "/alias/checkout" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  const retry = await screen.findByRole("button", { name: "Retry the same inspection" });
  expect((screen.getByRole("button", { name: "Cancel edit" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry);
  fireEvent.click(await screen.findByRole("button", { name: "Add inspected checkout" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.inspect.mock.calls[0][0]).toEqual(value.inspect.mock.calls[1][0]);
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson))).toMatchObject({ checkouts: [{ machine_id: machine.id, path: "/canonical/checkout" }], auto_fetch: true });
});

it("retains the original revision and identity when a configuration deletion acknowledgment is lost", async () => {
  const project = resource(EntityKind.PROJECT, { name: "Retained history" }, 7n), value = fixture([project]);
  value.remove.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<ConfigurationDeletion initial={project} deleted={() => {}} close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[0][0]).toEqual(value.remove.mock.calls[1][0]);
  expect(value.remove.mock.calls[0][0]).toMatchObject({ mutation: { id: project.id, expectedRevision: 7n } });
});

it("renders unknown quota and server candidate reasons without performing selection mutations", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Agent" }), project = resource(EntityKind.PROJECT, { name: "Restricted project" }), value = fixture([agent, project]);
  value.preview.mockResolvedValue({ routeJson: encode({ policy: "remaining-quota", fallback: true, candidates: [{ id: newRequestId(), weight: 1, eligibility: "project-restricted", quota_state: "unknown" }] }) });
  render(value.view(<RoutingPreview agent={agent} active close={() => {}} />));
  await screen.findByText(/Quota: unknown/);
  fireEvent.change(screen.getByLabelText("Project"), { target: { value: project.id } });
  await waitFor(() => expect(value.preview).toHaveBeenLastCalledWith(expect.objectContaining({ agentId: agent.id, projectId: project.id }), expect.anything()));
  expect(screen.getByText(/None eligible/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled();
});
