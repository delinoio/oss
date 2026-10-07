import { chooseScrollOption, waitScrollChoices } from "./test-scroll-picker";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTools } from "./session-tools";
import { NewSession, NewSessionKind } from "./new-session";

function fixture(automaticTitles = true) {
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
  const listResources = vi.fn(async (request: { filter?: { kind: EntityKind; pageToken: string } }) => request.filter?.kind === EntityKind.AGENT && !request.filter.pageToken ? { resources: [], nextPageToken: "later" } : { resources: [project, agent, machine].filter((row) => row.kind === request.filter?.kind), nextPageToken: "" });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 1, capabilities: automaticTitles ? [SystemCapability.AUTOMATIC_TITLES_V1] : [] }) });
    router.service(SessionService, { createSession, recoverSessionWorkspace: workspace, recoverSessionExecution: recover, prepareSessionWorkspace: prepare, renameSession: rename, controlSession: control });
    router.service(ResourceService, { getResource: (request) => ({ resource: [project, agent, machine].find(row => row.id === request.id) }), listResources });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, session, execution, workspace, recover, prepare, rename, control, createSession, project, agent, machine, listResources, client };
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

it("preserves a General Chat UTF-8 draft when automatic titles are unsupported", async () => {
  const value = fixture(false);
  render(value.view(<NewSession kind={NewSessionKind.GeneralChat} active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  const message = screen.getByLabelText("First message") as HTMLTextAreaElement;
  const valid = "가".repeat(Math.floor((256 << 10) / 3));
  fireEvent.change(message, { target: { value: valid } });
  fireEvent.change(message, { target: { value: valid + "가" } });
  expect(message.value).toBe(valid);
  expect(screen.getByText(/exceeds 256 KiB/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Start general chat" }).getAttribute("disabled")).not.toBeNull();
  fireEvent.submit(message.form!);
  expect(value.createSession).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unavailable, undefined])("keeps General Chat selector failure or empty inventory distinct (%s)", async (code) => {
  const value = fixture();
  const original = value.listResources.getMockImplementation()!;
  value.listResources.mockImplementation(async (request) => {
    if (request.filter?.kind === EntityKind.AGENT) {
      if (code) throw new ConnectError("Choice read failed", code);
      return { resources: [], nextPageToken: "" };
    }
    return original(request);
  });
  render(value.view(<NewSession kind={NewSessionKind.GeneralChat} active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  const expected = code === Code.PermissionDenied ? /The server denied access to these choices/ : code === Code.Unavailable ? /The server connection failed while loading these choices/ : /No selectable Agent Worker choices are on this page/;
  expect(await screen.findByText(expected)).toBeTruthy();
  if (code) expect(screen.queryByText("No selectable Agent Worker choices are on this page.")).toBeNull();
  expect(value.listResources.mock.calls.some(([request]) => request.filter?.kind === EntityKind.PROJECT)).toBe(false);
  expect(value.createSession).not.toHaveBeenCalled();
});

it("rejects missing General Chat selections, whitespace and invalid budgets without losing the draft", async () => {
  const value = fixture();
  render(value.view(<NewSession kind={NewSessionKind.GeneralChat} active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  const message = screen.getByLabelText("First message") as HTMLTextAreaElement;
  fireEvent.change(message, { target: { value: "Draft with no execution selection" } });
  fireEvent.submit(message.form!);
  expect(value.createSession).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Agent Worker" }));
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Agent Worker" }), { key: "Escape" });
  await chooseScrollOption(screen.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(screen.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.change(message, { target: { value: "   " } });
  fireEvent.submit(message.form!);
  expect(value.createSession).not.toHaveBeenCalled();
  fireEvent.change(message, { target: { value: "Review this idea" } });
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByText("Optional estimated-cost budget"));
  fireEvent.click(screen.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.change(screen.getByLabelText("Budget currency"), { target: { value: "USD" } });
  fireEvent.change(screen.getByLabelText("Estimated-cost threshold"), { target: { value: "invalid" } });
  fireEvent.click(screen.getByRole("button", { name: "Start general chat" }));
  expect(screen.getByRole("alert")).toBeTruthy();
  expect(message.value).toBe("Review this idea");
  expect(value.createSession).not.toHaveBeenCalled();
});

it("retries only the original General Chat request after uncertainty and reentry", async () => {
  const value = fixture();
  const open = vi.fn();
  value.createSession.mockRejectedValueOnce(new ConnectError("Lost creation response", Code.Unavailable));
  const page = (active: boolean, activation: number) => value.view(<NewSession kind={NewSessionKind.GeneralChat} active={active} ownsActivation={active} activation={activation} back={() => {}} openSettings={() => {}} open={open} created={() => {}} />);
  const view = render(page(true, 1));
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Agent Worker" }));
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Agent Worker" }), { key: "Escape" });
  await chooseScrollOption(screen.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(screen.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "One general conversation" } });
  fireEvent.click(screen.getByRole("button", { name: "Start general chat" }));
  await screen.findByRole("button", { name: "Retry the same session creation" });
  expect((screen.getByLabelText("First message").closest("fieldset") as HTMLFieldSetElement).disabled).toBe(true);
  view.rerender(page(false, 1));
  view.rerender(page(true, 2));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same session creation" }));
  await waitFor(() => expect(value.createSession).toHaveBeenCalledTimes(2));
  expect(value.createSession.mock.calls[0][0]).toEqual(value.createSession.mock.calls[1][0]);
  expect(open).not.toHaveBeenCalled();
  expect(await screen.findByRole("button", { name: "Open conversation" })).toBeTruthy();
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

it("inspects a pre-native Worktree interruption with the original first identity and exact retry", async () => {
  const value = fixture();
  value.recover.mockRejectedValueOnce(new ConnectError("receipt lost", Code.Unavailable));
  const retained = document(value.session);
  delete retained.execution;
  retained.workspace = "worktree";
  retained.initial_execution = { id: value.execution };
  const session = create(ResourceSchema, { ...value.session, documentJson: encode(retained) });
  const view = render(value.view(<SessionTools resource={session} changed={() => {}} />));
  fireEvent.click(screen.getByText("Session details and recovery"));
  fireEvent.click(screen.getByRole("button", { name: "Reconcile original execution" }));
  expect(screen.getByText(/startup or native cleanup evidence/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  await screen.findByRole("button", { name: "Retry the same execution recovery" });
  view.rerender(value.view(<SessionTools resource={create(ResourceSchema, { ...session, revision: 10n })} changed={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same execution recovery" }));
  await waitFor(() => expect(value.recover).toHaveBeenCalledTimes(2));
  expect(value.recover.mock.calls[0][0]).toEqual(value.recover.mock.calls[1][0]);
  expect(value.recover.mock.calls[0][0]).toMatchObject({ expectedExecutionId: value.execution, mutation: { expectedRevision: 8n } });
  expect(value.control).not.toHaveBeenCalled(); expect(value.createSession).not.toHaveBeenCalled(); expect(value.prepare).not.toHaveBeenCalled();
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
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  await waitScrollChoices(screen.getByRole("combobox", { name: "Project" }));
  await chooseScrollOption(screen.getByRole("combobox", { name: "Project" }), value.project.id);
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  const choices = within(screen.getByRole("combobox", { name: "Agent Worker" }).closest(".resource-choice")!);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Agent Worker" }));
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Agent Worker" }), { key: "Escape" });
  await chooseScrollOption(screen.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(screen.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  const repository = (document(value.project).repositories as string[])[0];
  fireEvent.change(screen.getByLabelText("Add repository override"), { target: { value: repository } });
  fireEvent.click(screen.getByRole("button", { name: "Add starting override" }));
  fireEvent.change(screen.getByLabelText(`Starting ${repository} type`), { target: { value: "remote-branch" } });
  fireEvent.change(screen.getByLabelText(`Starting ${repository} name`), { target: { value: "feature/source" } });
  fireEvent.change(screen.getByLabelText(`Starting ${repository} remote`), { target: { value: "upstream" } });
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Original selected work" } });
  fireEvent.click(screen.getByText("Optional estimated-cost budget"));
  fireEvent.click(screen.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.change(screen.getByLabelText("Budget currency"), { target: { value: "USD" } });
  fireEvent.change(screen.getByLabelText("Estimated-cost threshold"), { target: { value: "0.000000000000001" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(() => expect(value.createSession).toHaveBeenCalledTimes(1));
  const request = value.createSession.mock.calls[0][0] as { documentJson: Uint8Array; localWorkerToken: string };
  const input = JSON.parse(new TextDecoder().decode(request.documentJson));
  expect(input).toMatchObject({ name_mode: "automatic", source: "MANUAL", estimated_cost_budget: { currency: "USD", threshold: "0.000000000000001" }, workspace: "worktree", project_id: value.project.id, agent_id: value.agent.id, starting: [{ repository_id: repository, reference: { type: "remote-branch", name: "feature/source", remote: "upstream" } }] });
  expect(input).not.toHaveProperty("name");
  expect(request.localWorkerToken).toBe("");
});

it("consumes a locked project entry without applying it after Local proof settles", async () => {
  const value = fixture(), otherProjectId = newRequestId();
  let release!: (proof: { machineId: string; token: string }) => void;
  const readLocalWorker = () => new Promise<{ machineId: string; token: string }>(resolve => { release = resolve; });
  const blockedChanged = vi.fn();
  const page = (activation: number, entryProjectId: string, active = true) => value.view(<NewSession active={active} ownsActivation={active} activation={activation} entryProjectId={entryProjectId} projectSelectionBlockedChanged={blockedChanged} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} readLocalWorker={readLocalWorker} />);
  const rendered = render(page(1, value.project.id));
  await waitScrollChoices(screen.getByRole("combobox", { name: "Project" }));
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Retained task" } });
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  expect(blockedChanged).toHaveBeenLastCalledWith(true);
  rendered.rerender(page(2, otherProjectId));
  await act(async () => release({ machineId: value.machine.id, token: "A".repeat(43) }));
  expect(blockedChanged).toHaveBeenLastCalledWith(false);
  expect(screen.getByRole("combobox", { name: "Project" })).toHaveProperty(["dataset", "value"], value.project.id);
  expect(screen.getByRole("button", { name: "Use this computer's Local checkouts" }).getAttribute("aria-pressed")).toBe("true");
  rendered.rerender(page(2, otherProjectId, false));
  rendered.rerender(page(2, otherProjectId));
  expect(screen.getByRole("combobox", { name: "Project" })).toHaveProperty(["dataset", "value"], value.project.id);
  rendered.rerender(page(3, otherProjectId));
  expect(screen.getByRole("combobox", { name: "Project" })).toHaveProperty(["dataset", "value"], otherProjectId);
  expect(screen.getByRole("button", { name: "Use separate Worktrees" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByRole("combobox", { name: "Runs on" })).toHaveProperty(["dataset", "value"], "");
  expect(screen.getByLabelText("First message")).toHaveProperty("value", "Retained task");
  expect(value.createSession).not.toHaveBeenCalled();
});

it("reads fresh matching Local Worker proof for creation and retains that exact secret-bearing request on uncertainty", async () => {
  const value = fixture();
  const proof = vi.fn(async () => ({ machineId: value.machine.id, token: "A".repeat(43) }));
  value.createSession.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} readLocalWorker={proof} />));
  await waitScrollChoices(screen.getByRole("combobox", { name: "Project" }));
  await chooseScrollOption(screen.getByRole("combobox", { name: "Project" }), value.project.id);
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => expect((screen.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement).dataset.value).toBe(value.machine.id));
  expect((screen.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement).disabled).toBe(true);
  const choices = within(screen.getByRole("combobox", { name: "Agent Worker" }).closest(".resource-choice")!);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Agent Worker" }));
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Agent Worker" }), { key: "Escape" });
  await chooseScrollOption(screen.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
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

it("locks Project selection while Local proof or session creation is pending or uncertain", async () => {
  const value = fixture();
  let finishProof!: (proof: { machineId: string; token: string }) => void;
  let finishCreate!: (result: { change: { session: Resource } }) => void;
  const proof = vi.fn(() => new Promise<{ machineId: string; token: string }>((resolve) => { finishProof = resolve; }));
  value.createSession
    .mockImplementationOnce(() => new Promise((resolve) => { finishCreate = resolve; }))
    .mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} readLocalWorker={proof} />));
  await waitScrollChoices(screen.getByRole("combobox", { name: "Project" }));
  const project = screen.getByRole("combobox", { name: "Project" }) as HTMLSelectElement;
  await chooseScrollOption(project, value.project.id);
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByRole("button", { name: "Use this computer's Local checkouts" }));
  await waitFor(() => {
    expect(proof).toHaveBeenCalledTimes(1);
    expect(project.disabled).toBe(true);
  });
  finishProof({ machineId: value.machine.id, token: "A".repeat(43) });
  await waitFor(() => expect(project.disabled).toBe(false));
  proof.mockResolvedValue({ machineId: value.machine.id, token: "A".repeat(43) });
  const agentChoices = within(screen.getByRole("combobox", { name: "Agent Worker" }).closest(".resource-choice")!);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Agent Worker" }));
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Agent Worker" }), { key: "Escape" });
  await chooseScrollOption(screen.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Retained local request" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(() => {
    expect(value.createSession).toHaveBeenCalledTimes(1);
    expect(project.disabled).toBe(true);
  });
  finishCreate({ change: { session: value.session } });
  await waitFor(() => expect(project.disabled).toBe(false));
  fireEvent.change(screen.getByLabelText("First message"), { target: { value: "Uncertain local request" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await screen.findByRole("button", { name: "Retry the same session creation" });
  expect(project.disabled).toBe(true);
  const retained = value.createSession.mock.calls[0][0] as { documentJson: Uint8Array };
  expect(JSON.parse(new TextDecoder().decode(retained.documentJson))).toMatchObject({ project_id: value.project.id, workspace: "local" });
});

it("names the New session selector Runs on while loading Runner Device choices", async () => {
  const value = fixture();
  const original = value.listResources.getMockImplementation()!;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  value.listResources.mockImplementation(async (request) => {
    if (request.filter?.kind === EntityKind.MACHINE) await gate;
    return original(request);
  });
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  const selector = screen.getByRole("combobox", { name: "Runs on" });
  expect(selector.textContent).toContain("Select runner device");
  expect(await screen.findByText("Loading Runner Device choices…")).toBeTruthy();
  release();
  await waitScrollChoices(selector);
  expect(value.createSession).not.toHaveBeenCalled();
});

it("explains an empty Runner Device inventory without replacing the Runs on selector", async () => {
  const value = fixture();
  const original = value.listResources.getMockImplementation()!;
  value.listResources.mockImplementation(async (request) => request.filter?.kind === EntityKind.MACHINE ? { resources: [], nextPageToken: "" } : original(request));
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  expect(await screen.findByText("No selectable Runner Device choices are on this page.")).toBeTruthy();
  const selector = screen.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement;
  expect(selector.dataset.value).toBe("");
  expect(selector.textContent).toContain("Select runner device");
  expect(value.createSession).not.toHaveBeenCalled();
});

it("retains an unavailable Runner Device's original identity through paginated inventory", async () => {
  const value = fixture();
  render(value.view(<NewSession active ownsActivation activation={1} back={() => {}} openSettings={() => {}} open={() => {}} created={() => {}} />));
  const selector = screen.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement;
  await chooseScrollOption(selector, value.machine.id);
  const original = value.listResources.getMockImplementation()!;
  const other = create(ResourceSchema, { ...value.machine, id: newRequestId(), documentJson: encode({ name: "Other device" }) });
  value.listResources.mockImplementation(async (request) => request.filter?.kind === EntityKind.MACHINE ? { resources: [other], nextPageToken: request.filter.pageToken ? "" : "next-devices" } : original(request));
  await act(() => value.client.invalidateQueries());
  await screen.findByText("Showing cached Runner Device choices. The latest request for these choices failed. Your current selection is retained.");
  fireEvent.click(selector);
  fireEvent.click(await screen.findByRole("button", { name: "Reload list" }));
  fireEvent.keyDown(selector, { key: "Escape" });
  expect(await screen.findByText("The selected Runner Device is outside this page. Its exact identity remains selected.")).toBeTruthy();
  expect(selector.dataset.value).toBe(value.machine.id);
  expect(selector.textContent).toBe("Worker");
  const choices = within(selector.closest(".resource-choice")!);
  fireEvent.click(selector);
  const more = screen.getByRole("button", { name: "Load more Runs on" }) as HTMLButtonElement;
  await waitFor(() => expect(more.disabled).toBe(false));
  fireEvent.click(more);
  await waitFor(() => expect(value.listResources.mock.calls.some(([request]) => request.filter?.kind === EntityKind.MACHINE && request.filter.pageToken === "next-devices")).toBe(true));
  expect(selector.dataset.value).toBe(value.machine.id);
  expect(value.createSession).not.toHaveBeenCalled();
});
