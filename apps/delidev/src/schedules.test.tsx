import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, ScheduleService, WorkerService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { ScheduleDetails, ScheduleEditor, Schedules } from "./schedules";
import { MachineSettings } from "./machine-settings";

function fixture() {
  const definition = { name: "Weekday review", enabled: false, prompt: "Retained prompt", project_id: newRequestId(), agent_id: newRequestId(), machine_id: newRequestId(), workspace: "worktree", mode: "plan", cron: "0 9 * * 1-5", timezone: "Asia/Seoul", overlap: "wait" };
  const schedule = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SCHEDULE, revision: 3n, schemaVersion: 1, documentJson: encode({ definition, configuration_revision: 2, created_by: newRequestId(), last_occurrence: 9, next_run_at: "2026-09-28T00:00:00Z" }) });
  const machine = create(ResourceSchema, { id: definition.machine_id, kind: EntityKind.MACHINE, revision: 5n, schemaVersion: 1, documentJson: encode({ name: "Selected Worker", os: "darwin", architecture: "arm64", installations: [{ harness: "codex", state: "detected", explicit_path: "/original/codex", version: "0.151.0" }] }) });
  const occurrence = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.OCCURRENCE, revision: 1n, schemaVersion: 1, documentJson: encode({ schedule_id: schedule.id, state: "waiting", trigger: "manual", overlap: "wait", selection: { ...definition, prompt: "Original occurrence prompt" } }) });
  const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, revision: 1n, schemaVersion: 1, documentJson: encode({ state: "queued", type: "harness-discovery" }) });
  const save = vi.fn(async (_request: unknown) => ({ schedule }));
  const run = vi.fn(async (_request: unknown) => ({ occurrence }));
  const control = vi.fn(async (_request: unknown) => ({ schedule }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const discovery = vi.fn(async (_request: unknown) => ({ machine, job }));
  const repositoryId = newRequestId();
  const project = create(ResourceSchema, { id: definition.project_id, kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Selected project", repositories: [repositoryId], base: { type: "local-branch", name: "main" } }) });
  const agent = create(ResourceSchema, { id: definition.agent_id, kind: EntityKind.AGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Selected agent" }) });
  const resources = [machine, project, agent];
  const list = vi.fn(async (request: { filter?: { kind: EntityKind; pageToken: string } }) => ({ resources: resources.filter((row) => row.kind === request.filter?.kind), nextPageToken: "" }));
  const transport = createRouterTransport((router) => {
    router.service(ScheduleService, { listSchedules: () => ({ schedules: [schedule] }), getSchedule: () => ({ schedule }), saveSchedule: save, runScheduleNow: run, controlSchedule: control, deleteSchedule: remove, listScheduleOccurrences: () => ({ occurrences: [occurrence] }), getScheduleOccurrence: () => ({ occurrence }) });
    router.service(ResourceService, { listResources: list, getResource: (request) => ({ resource: request.kind === EntityKind.JOB ? job : resources.find((row) => row.id === request.id) }) });
    router.service(WorkerService, { discoverHarnesses: discovery });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { schedule, machine, resources, definition, occurrence, save, run, control, remove, discovery, view, client, list, project, agent, repositoryId };
}

it("sends only the editable schedule definition and retries its exact original revision", async () => {
  const value = fixture();
  value.save.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<ScheduleEditor initial={value.schedule} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Scheduled prompt"), { target: { value: "Updated prompt" } });
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = value.save.mock.calls[0][0] as { definitionJson: Uint8Array; mutation: { expectedRevision: bigint }; localWorkerToken: string };
  expect(JSON.parse(new TextDecoder().decode(request.definitionJson))).toEqual({ ...value.definition, prompt: "Updated prompt" });
  expect(request.mutation.expectedRevision).toBe(3n); expect(request.localWorkerToken).toBe("");
});

it("retains a new schedule draft when navigating away and back", () => {
  const value = fixture(), rendered = render(value.view(<Schedules active open={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  fireEvent.change(screen.getByLabelText("Scheduled prompt"), { target: { value: "Keep this schedule draft" } });
  rendered.rerender(value.view(<Schedules active={false} open={() => {}} />));
  rendered.rerender(value.view(<Schedules active open={() => {}} />));
  expect((screen.getByLabelText("Scheduled prompt") as HTMLTextAreaElement).value).toBe("Keep this schedule draft");
  expect(value.save).not.toHaveBeenCalled();
});

it("binds Run now retries to one occurrence without resuming the paused schedule", async () => {
  const value = fixture();
  value.run.mockRejectedValueOnce(new ConnectError("lost acknowledgment", Code.Unavailable));
  render(value.view(<ScheduleDetails initial={value.schedule} active open={() => {}} close={() => {}} edit={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Run now" }));
  expect(value.run).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm Run now" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same Run now" }));
  await screen.findByText("Run now accepted");
  expect(value.run.mock.calls[0][0]).toEqual(value.run.mock.calls[1][0]);
  expect(value.control).not.toHaveBeenCalled();
  expect(screen.getAllByText("No session has been created for this occurrence.").length).toBeGreaterThan(0);
});

it("keeps original occurrence history after configuration deletion without deleting sessions", async () => {
  const value = fixture();
  render(value.view(<ScheduleDetails initial={value.schedule} active open={() => {}} close={() => {}} edit={() => {}} />));
  await screen.findByText("Original occurrence prompt");
  fireEvent.click(screen.getByRole("button", { name: "Delete schedule" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm schedule deletion" }));
  await screen.findByText("Schedule configuration deleted. Retained occurrences and sessions remain.");
  expect(screen.getByText("Original occurrence prompt")).toBeTruthy();
  expect(value.run).not.toHaveBeenCalled(); expect(value.control).not.toHaveBeenCalled();
});

it("retains the original authenticated Local machine without inventing another origin", async () => {
  const value = fixture();
  value.schedule.documentJson = encode({ ...document(value.schedule), definition: { ...value.definition, workspace: "local" }, local_origin: { machine_id: value.machine.id, device_id: newRequestId() } });
  render(value.view(<ScheduleEditor initial={value.schedule} active saved={() => {}} cancel={() => {}} />));
  expect((screen.getByLabelText("Runner Device") as HTMLSelectElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0] as { definitionJson: Uint8Array; localWorkerToken: string };
  expect(JSON.parse(new TextDecoder().decode(request.definitionJson))).toEqual({ ...value.definition, workspace: "local" });
  expect(request.localWorkerToken).toBe("");
});

it("retains exact executable selections and protocol intent after uncertain Worker acceptance", async () => {
  const value = fixture();
  value.discovery.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<MachineSettings initial={value.machine} active close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  fireEvent.change(screen.getByLabelText("codex executable path"), { target: { value: "/selected/codex" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Verify the installed native protocol without login or inference" }));
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same harness check" }));
  await screen.findByText("Worker operation: queued");
  expect(value.discovery.mock.calls[0][0]).toEqual(value.discovery.mock.calls[1][0]);
  const request = value.discovery.mock.calls[0][0] as { selectionsJson: Uint8Array; verifyProtocol: boolean; mutation: { expectedRevision: bigint } };
  expect(request.verifyProtocol).toBe(true); expect(request.mutation.expectedRevision).toBe(5n);
  expect(JSON.parse(new TextDecoder().decode(request.selectionsJson))).toEqual({ executables: [{ harness: "codex", path: "/selected/codex" }, { harness: "claude-code", path: "" }, { harness: "opencode", path: "" }, { harness: "grok-build", path: "" }] });
  expect(screen.getByText("Native protocol: Not checked")).toBeTruthy();
});

it("blocks stale executable path edits while retaining the staged path", async () => {
  const value = fixture();
  render(value.view(<MachineSettings initial={value.machine} active close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  fireEvent.change(screen.getByLabelText("codex executable path"), { target: { value: "/staged/codex" } });
  value.resources[0] = create(ResourceSchema, { ...value.machine, revision: 6n });
  await screen.findByText(/Worker configuration changed elsewhere/, {}, { timeout: 7000 });
  expect((screen.getByRole("button", { name: "Run optional diagnostics" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("codex executable path") as HTMLInputElement).value).toBe("/staged/codex");
  expect(value.discovery).not.toHaveBeenCalled();
});


async function fillCreation(value: ReturnType<typeof fixture>) {
  fireEvent.change(screen.getByLabelText("Schedule name"), { target: { value: "Morning review" } });
  fireEvent.change(screen.getByLabelText("Scheduled prompt"), { target: { value: "Review project changes" } });
  for (const [label, resource] of [["Project", value.project], ["Agent Worker", value.agent], ["Runner Device", value.machine]] as const) {
    const select = screen.getByLabelText(label);
    await within(select).findByRole("option", { name: JSON.parse(new TextDecoder().decode(resource.documentJson)).name });
    fireEvent.change(select, { target: { value: resource.id } });
  }
}
const choose = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });
function createRequest(value: ReturnType<typeof fixture>) {
  return value.save.mock.calls[0][0] as { definitionJson: Uint8Array; mutation: { id: string; requestId: string; expectedRevision: bigint }; schemaVersion: number; localWorkerToken: string };
}

it("creates a paused strict definition with explicit resources and the original defaults", async () => {
  const value = fixture(), saved = vi.fn();
  render(value.view(<ScheduleEditor active saved={saved} cancel={() => {}} />));
  expect(screen.getByLabelText("Schedule name")).toBe(globalThis.document.activeElement);
  expect(screen.getByRole("button", { name: /Starting reference overrides/ }).getAttribute("aria-expanded")).toBe("false");
  await fillCreation(value);
  expect((screen.getByLabelText("Project") as HTMLSelectElement).required).toBe(true);
  expect((screen.getByRole("checkbox", { name: "Enable future scheduled runs" }) as HTMLInputElement).checked).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  const request = createRequest(value);
  expect(JSON.parse(new TextDecoder().decode(request.definitionJson))).toEqual({ name: "Morning review", prompt: "Review project changes", enabled: false, project_id: value.project.id, agent_id: value.agent.id, machine_id: value.machine.id, workspace: "worktree", mode: "execute", cron: "0 9 * * 1-5", timezone: "UTC", overlap: "overlap", starting: [] });
  expect(request.mutation).toMatchObject({ id: "", expectedRevision: 0n }); expect(request.schemaVersion).toBe(1);
  expect(request.mutation.requestId).toMatch(/^[0-9a-f-]{36}$/); expect(request.localWorkerToken).toBe("");
});

it.each([["daily", "07:05", undefined, "5 7 * * *"], ["weekdays", "18:30", undefined, "30 18 * * 1-5"], ["weekly", "23:59", "0", "59 23 * * 0"]])("serializes %s as only the generated cron", async (frequency, time, weekday, cron) => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  await fillCreation(value); choose("Frequency", frequency!); choose("Time", time!);
  if (weekday) choose("Weekday", weekday);
  choose("IANA timezone", "Asia/Seoul");
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  const definition = JSON.parse(new TextDecoder().decode(createRequest(value).definitionJson));
  expect(definition).toMatchObject({ cron, timezone: "Asia/Seoul", enabled: false, mode: "execute", overlap: "overlap" });
  expect(Object.keys(definition).sort()).toEqual(["name", "prompt", "enabled", "project_id", "agent_id", "machine_id", "workspace", "mode", "cron", "timezone", "overlap", "starting"].sort());
});

it("retains preset time and Weekly weekday while importing only canonical custom time", () => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  choose("Frequency", "weekly"); expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe("1");
  choose("Weekday", "3"); choose("Time", "07:05"); choose("Frequency", "daily"); choose("Frequency", "weekdays");
  expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe("07:05");
  choose("Frequency", "custom"); expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe("5 7 * * 1-5");
  choose("Cron expression", "5 7 * * 2"); choose("Frequency", "weekly");
  expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe("3"); expect(screen.getByText("5 7 * * 3")).toBeTruthy();
  choose("Time", "18:30"); choose("Frequency", "custom"); choose("Cron expression", "*/15 * * * *");
  expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe("*/15 * * * *");
  choose("Frequency", "daily"); expect(screen.getByText("30 18 * * *")).toBeTruthy();
  choose("Frequency", "custom"); choose("Cron expression", "05 07 * * 2"); choose("Frequency", "weekly");
  expect(screen.getByText("30 18 * * 3")).toBeTruthy();
});

it("uses 09:00 when no valid preset time has been retained, without parsing arbitrary cron", () => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  choose("Time", ""); choose("Frequency", "custom");
  expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe("0 9 * * 1-5");
  choose("Cron expression", " 5 7 * * 2 "); choose("Frequency", "daily"); expect(screen.getByText("0 9 * * *")).toBeTruthy();
});

it("keeps empty Time visible and blocks even direct form submission instead of sending stale cron", async () => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />)); await fillCreation(value);
  choose("Time", ""); expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe("");
  expect((screen.getByRole("button", { name: "Create schedule" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.submit(screen.getByRole("button", { name: "Create schedule" }).closest("form")!);
  expect(value.save).not.toHaveBeenCalled(); expect(screen.queryByText("0 9 * * 1-5")).toBeNull();
});

it.each([["0 0 31 2 *", "UTC"], ["0 9 * * 1-5", "unknown/zone"]])("retains server-rejected calendar %s and timezone %s for correction", async (cron, timezone) => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Calendar rejected", Code.InvalidArgument));
  render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />)); await fillCreation(value);
  choose("Frequency", "custom"); choose("Cron expression", cron); choose("IANA timezone", timezone);
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  await waitFor(() => expect((screen.getByRole("button", { name: "Create schedule" }) as HTMLButtonElement).disabled).toBe(false));
  expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe(cron); expect((screen.getByLabelText("IANA timezone") as HTMLInputElement).value).toBe(timezone);
  expect(screen.getByText("Next run is calculated by the server after saving.")).toBeTruthy();
  expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
});

it("keeps complete reference drafts mounted behind the disclosure and clears them on project change", async () => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />)); await fillCreation(value);
  const disclosure = screen.getByRole("button", { name: /Starting reference overrides/ }); fireEvent.click(disclosure);
  await within(screen.getByLabelText("Add repository override")).findByRole("option", { name: value.repositoryId });
  choose("Add repository override", value.repositoryId); fireEvent.click(screen.getByRole("button", { name: "Add starting override" }));
  const reference = screen.getByLabelText(`Starting ${value.repositoryId} name`); fireEvent.change(reference, { target: { value: "retained-branch" } });
  expect(disclosure.textContent).toContain("1 override"); fireEvent.click(disclosure); fireEvent.click(disclosure);
  expect(screen.getByLabelText(`Starting ${value.repositoryId} name`)).toBe(reference); expect((reference as HTMLInputElement).value).toBe("retained-branch");
  fireEvent.click(screen.getByRole("button", { name: "Remove starting override" })); expect(disclosure.textContent).toContain("Using saved project references");
  choose("Add repository override", value.repositoryId); fireEvent.click(screen.getByRole("button", { name: "Add starting override" }));
  choose("Project", ""); expect(disclosure.textContent).toContain("Using saved project references"); expect(screen.queryByLabelText(`Starting ${value.repositoryId} name`)).toBeNull();
  expect(document(value.project)).toMatchObject({ base: { name: "main" } });
});

it("retains identical Local schedule bytes on uncertain retry without ownership proof", async () => {
  const value = fixture();
  const read = vi.fn(() => Promise.reject(new Error("proof is unused")));
  value.save.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  const rendered = render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} readLocalWorker={read} />));
  await fillCreation(value);
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
  expect(screen.getByLabelText("Runner Device")).toHaveProperty("disabled", false);
  expect(read).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await screen.findByRole("button", { name: "Retry the same schedule" });
  rendered.rerender(value.view(<ScheduleEditor active={false} saved={() => {}} cancel={() => {}} readLocalWorker={read} />));
  rendered.rerender(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} readLocalWorker={read} />));
  fireEvent.submit(screen.getByRole("button", { name: "Create schedule" }).closest("form")!);
  expect(value.save).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Retry the same schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  expect(read).not.toHaveBeenCalled();
  expect(createRequest(value).localWorkerToken).toBe("");
});

it.each(["changed", "malformed", "rejected"])("ignores obsolete %s Local proof and preserves the selected Worker", async (failure) => {
  const value = fixture();
  const read = vi.fn(async () => { if (failure === "rejected") throw new Error("private failure"); return { machineId: newRequestId(), token: "bad" }; });
  render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} readLocalWorker={read} />));
  await fillCreation(value);
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(read).not.toHaveBeenCalled();
  expect(createRequest(value).localWorkerToken).toBe("");
});

it("retains creation UI state across inactivity without focus theft and Cancel returns to guidance", async () => {
  const value = fixture(), rendered = render(value.view(<Schedules active={false} open={() => {}} />));
  rendered.rerender(value.view(<Schedules active open={() => {}} />)); fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  expect(screen.getByLabelText("Schedule name")).toBe(globalThis.document.activeElement);
  choose("Frequency", "weekly"); choose("Weekday", "3"); choose("Time", "18:30"); choose("Frequency", "custom"); choose("Cron expression", "*/15 * * * *");
  fireEvent.click(screen.getByRole("button", { name: /Starting reference overrides/ })); screen.getByLabelText("Scheduled prompt").focus();
  rendered.rerender(value.view(<Schedules active={false} open={() => {}} />)); rendered.rerender(value.view(<Schedules active open={() => {}} />));
  await value.client.invalidateQueries();
  expect(globalThis.document.activeElement).toBe(screen.getByLabelText("Scheduled prompt")); expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe("*/15 * * * *");
  expect(screen.getByRole("button", { name: /Starting reference overrides/ }).getAttribute("aria-expanded")).toBe("true");
  choose("Frequency", "weekly"); expect((screen.getByLabelText("Weekday") as HTMLSelectElement).value).toBe("3"); expect((screen.getByLabelText("Time") as HTMLInputElement).value).toBe("18:30");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" })); expect(screen.getByText(/Select a schedule from the sidebar/)).toBeTruthy(); expect(value.save).not.toHaveBeenCalled();
});

it.each(["a", "한"])("retains the previous prompt at its UTF-8 limit for %s", (character) => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  const prompt = character.repeat(Math.floor(262144 / new TextEncoder().encode(character).byteLength));
  choose("Scheduled prompt", prompt); choose("Scheduled prompt", prompt + character);
  expect((screen.getByLabelText("Scheduled prompt") as HTMLTextAreaElement).value).toBe(prompt); expect(screen.getByText(/previous draft is retained/)).toBeTruthy();
});

it("shows initial catalog loading and successful empty pages without selecting a default", async () => {
  const value = fixture(); let ready!: () => void; const pending = new Promise<void>((resolve) => { ready = resolve; });
  value.list.mockImplementation(async () => { await pending; return { resources: [], nextPageToken: "" }; });
  render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  expect(screen.getByText("Loading Project choices…")).toBeTruthy(); expect((screen.getByLabelText("Project") as HTMLSelectElement).value).toBe("");
  ready(); await screen.findByText("No selectable Project choices are on this page."); await screen.findByText("No selectable Agent Worker choices are on this page.");
  expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("shows catalog failure %s without substituting a resource", async (code) => {
  const value = fixture(); value.list.mockRejectedValue(new ConnectError("fixture denied", code));
  render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />));
  await screen.findAllByText(code === Code.Unavailable ? /server connection failed while loading these choices/ : /server denied access to these choices/);
  expect((screen.getByLabelText("Project") as HTMLSelectElement).value).toBe(""); expect(value.save).not.toHaveBeenCalled();
});

it("reports cached catalog refresh failure with the previous exact choices and selections", async () => {
  const value = fixture(); render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />)); await fillCreation(value);
  value.list.mockRejectedValue(new ConnectError("fixture offline", Code.Unavailable)); await value.client.invalidateQueries();
  await screen.findByText(/Showing cached Project choices/);
  expect((screen.getByLabelText("Project") as HTMLSelectElement).value).toBe(value.project.id); expect(screen.getByRole("option", { name: "Selected project" })).toBeTruthy(); expect(value.save).not.toHaveBeenCalled();
});

it("preserves an exact off-page choice through bounded More/First pages and suspends inactive reads", async () => {
  const value = fixture(); value.list.mockImplementation(async (request) => ({ resources: request.filter?.pageToken ? [] : value.resources.filter((row) => row.kind === request.filter?.kind), nextPageToken: request.filter?.kind === EntityKind.PROJECT && !request.filter.pageToken ? "next-project-page" : "" }));
  const rendered = render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} />)); await fillCreation(value);
  const projectChoices = within(screen.getByLabelText("Project").closest(".resource-choice")!);
  fireEvent.click(projectChoices.getByRole("button", { name: "More choices" }));
  await projectChoices.findByText(/selected Project is outside this page/);
  expect((screen.getByLabelText("Project") as HTMLSelectElement).value).toBe(value.project.id);
  expect((screen.getByLabelText("Project") as HTMLSelectElement).selectedOptions[0].value).toBe(value.project.id);
  fireEvent.click(projectChoices.getByRole("button", { name: "First choices" })); await projectChoices.findByRole("option", { name: "Selected project" });
  rendered.rerender(value.view(<ScheduleEditor active={false} saved={() => {}} cancel={() => {}} />));
  const reads = value.list.mock.calls.length; await value.client.invalidateQueries(); expect(value.list).toHaveBeenCalledTimes(reads); expect(value.save).not.toHaveBeenCalled();
});

it("disposes Local creation without requesting ownership proof", async () => {
  const value = fixture(), read = vi.fn(async () => ({ machineId: value.machine.id, token: "unused" }));
  const rendered = render(value.view(<ScheduleEditor active saved={() => {}} cancel={() => {}} readLocalWorker={read} />));
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" })); rendered.unmount();
  expect(read).not.toHaveBeenCalled(); expect(value.save).not.toHaveBeenCalled();
});
