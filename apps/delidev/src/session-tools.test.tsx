import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTools } from "./session-tools";
import { CreateSession } from "./views";

function fixture() {
  const execution = newRequestId();
  const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Session", archive: "active", outcome: "not-started", dispatch: "paused", recovery: "required", preparation: { state: "uncertain" }, execution: { execution_id: execution } }) });
  const workspace = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const recover = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const prepare = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const rename = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const control = vi.fn(async () => ({ change: { session } }));
  const createSession = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Project", repositories: [newRequestId()], agents: { configured: false, ids: [] } }) });
  const agent = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Later-page agent" }) });
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Worker" }) });
  const transport = createRouterTransport((router) => {
    router.service(SessionService, { createSession, recoverSessionWorkspace: workspace, recoverSessionExecution: recover, prepareSessionWorkspace: prepare, renameSession: rename, controlSession: control });
    router.service(ResourceService, { getResource: (request) => ({ resource: request.id === project.id ? project : undefined }), listResources: (request) => request.filter?.kind === EntityKind.AGENT && !request.filter.pageToken ? { resources: [], nextPageToken: "later" } : { resources: [project, agent, machine].filter((row) => row.kind === request.filter?.kind) } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, session, execution, workspace, recover, prepare, rename, control, createSession, project, agent, machine };
}

it("requires explicit incomplete preparation cleanup and retains its original retry after a peer change", async () => {
  const value = fixture();
  value.workspace.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  const rendered = render(value.view(<SessionTools resource={value.session} changed={() => {}} />));
  fireEvent.click(screen.getByText("Session details and recovery"));
  fireEvent.click(screen.getByRole("button", { name: "Clean incomplete preparation" }));
  expect(value.workspace).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  await screen.findByRole("button", { name: "Retry the same workspace recovery" });
  rendered.rerender(value.view(<SessionTools resource={create(ResourceSchema, { ...value.session, revision: 9n })} changed={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same workspace recovery" }));
  await waitFor(() => expect(value.workspace).toHaveBeenCalledTimes(2));
  expect(value.workspace.mock.calls[0][0]).toEqual(value.workspace.mock.calls[1][0]);
  expect(value.workspace.mock.calls[0][0]).toMatchObject({ cleanup: true, mutation: { id: value.session.id, expectedRevision: 8n } });
  expect(value.control).not.toHaveBeenCalled(); expect(value.prepare).not.toHaveBeenCalled();
});

it("binds execution recovery to the original execution without resending input or resuming", async () => {
  const value = fixture();
  render(value.view(<SessionTools resource={value.session} changed={() => {}} />));
  fireEvent.click(screen.getByText("Session details and recovery"));
  fireEvent.click(screen.getByRole("button", { name: "Reconcile original execution" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  await waitFor(() => expect(value.recover).toHaveBeenCalledTimes(1));
  expect(value.recover.mock.calls[0][0]).toMatchObject({ expectedExecutionId: value.execution, mutation: { expectedRevision: 8n } });
  expect(value.control).not.toHaveBeenCalled(); expect(value.createSession).not.toHaveBeenCalled();
});

it("keeps a stale name draft and blocks a recovery confirmation selected before a peer revision", async () => {
  const value = fixture(), rendered = render(value.view(<SessionTools resource={value.session} changed={() => {}} />));
  fireEvent.click(screen.getByText("Session details and recovery"));
  fireEvent.click(screen.getByRole("button", { name: "Rename session" }));
  fireEvent.change(screen.getByLabelText("Session name"), { target: { value: "Staged name" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect original workspace recovery" }));
  rendered.rerender(value.view(<SessionTools resource={create(ResourceSchema, { ...value.session, revision: 9n })} changed={() => {}} />));
  expect((screen.getByRole("button", { name: "Save session name" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Confirm selected recovery action" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("Session name") as HTMLInputElement).value).toBe("Staged name");
  expect(value.rename).not.toHaveBeenCalled(); expect(value.workspace).not.toHaveBeenCalled();
});

it("selects an Agent from later pages and preserves an explicit per-repository starting override", async () => {
  const value = fixture();
  render(value.view(<CreateSession visible close={() => {}} open={() => {}} />));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Explicit session" } });
  await screen.findByRole("option", { name: "Project" });
  fireEvent.change(screen.getByLabelText("Project"), { target: { value: value.project.id } });
  const choices = within(screen.getByLabelText("Agent Worker").closest(".resource-choice")!);
  fireEvent.click(await choices.findByRole("button", { name: "More choices" }));
  fireEvent.change(screen.getByLabelText("Agent Worker"), { target: { value: (await screen.findByRole("option", { name: "Later-page agent" }) as HTMLOptionElement).value } });
  fireEvent.change(screen.getByLabelText("Execution Worker"), { target: { value: value.machine.id } });
  const repository = (document(value.project).repositories as string[])[0];
  fireEvent.change(screen.getByLabelText("Add repository override"), { target: { value: repository } });
  fireEvent.click(screen.getByRole("button", { name: "Add starting override" }));
  fireEvent.change(screen.getByLabelText(`Starting ${repository} type`), { target: { value: "remote-branch" } });
  fireEvent.change(screen.getByLabelText(`Starting ${repository} name`), { target: { value: "feature/source" } });
  fireEvent.change(screen.getByLabelText(`Starting ${repository} remote`), { target: { value: "upstream" } });
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Original selected work" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(() => expect(value.createSession).toHaveBeenCalledTimes(1));
  const request = value.createSession.mock.calls[0][0] as { documentJson: Uint8Array; localWorkerToken: string };
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toMatchObject({ source: "MANUAL", workspace: "worktree", project_id: value.project.id, agent_id: value.agent.id, starting: [{ repository_id: repository, reference: { type: "remote-branch", name: "feature/source", remote: "upstream" } }] });
  expect(request.localWorkerToken).toBe("");
});

it("reads fresh matching Local Worker proof for creation and retains that exact secret-bearing request on uncertainty", async () => {
  const value = fixture();
  const proof = vi.fn(async () => ({ machineId: value.machine.id, token: "A".repeat(43) }));
  value.createSession.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<CreateSession visible close={() => {}} open={() => {}} readLocalWorker={proof} />));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Local session" } });
  await screen.findByRole("option", { name: "Project" });
  fireEvent.change(screen.getByLabelText("Project"), { target: { value: value.project.id } });
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => expect((screen.getByLabelText("Execution Worker") as HTMLSelectElement).value).toBe(value.machine.id));
  expect((screen.getByLabelText("Execution Worker") as HTMLSelectElement).disabled).toBe(true);
  const choices = within(screen.getByLabelText("Agent Worker").closest(".resource-choice")!);
  fireEvent.click(await choices.findByRole("button", { name: "More choices" }));
  await screen.findByRole("option", { name: "Later-page agent" });
  fireEvent.change(screen.getByLabelText("Agent Worker"), { target: { value: value.agent.id } });
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Local prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await screen.findByRole("button", { name: "Retry the same session creation" });
  expect(proof).toHaveBeenCalledTimes(2);
  proof.mockResolvedValue({ machineId: newRequestId(), token: "B".repeat(42) + "A" });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same session creation" }));
  await waitFor(() => expect(value.createSession).toHaveBeenCalledTimes(2));
  expect(value.createSession.mock.calls[0][0]).toEqual(value.createSession.mock.calls[1][0]);
  expect(proof).toHaveBeenCalledTimes(2);
  const request = value.createSession.mock.calls[0][0] as { documentJson: Uint8Array; localWorkerToken: string };
  expect(request.localWorkerToken).toBe("A".repeat(43));
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toMatchObject({ workspace: "local", machine_id: value.machine.id });
  expect(new TextDecoder().decode(request.documentJson)).not.toContain(request.localWorkerToken);
  expect(screen.queryByLabelText("Add repository override")).toBeNull();
});
