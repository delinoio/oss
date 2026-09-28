import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
  const resources = [machine];
  const transport = createRouterTransport((router) => {
    router.service(ScheduleService, { listSchedules: () => ({ schedules: [schedule] }), getSchedule: () => ({ schedule }), saveSchedule: save, runScheduleNow: run, controlSchedule: control, deleteSchedule: remove, listScheduleOccurrences: () => ({ occurrences: [occurrence] }), getScheduleOccurrence: () => ({ occurrence }) });
    router.service(ResourceService, { listResources: () => ({ resources }), getResource: (request) => ({ resource: request.kind === EntityKind.JOB ? job : resources.find((row) => row.id === request.id) }) });
    router.service(WorkerService, { discoverHarnesses: discovery });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { schedule, machine, resources, definition, occurrence, save, run, control, remove, discovery, view };
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
  expect((screen.getByLabelText("Execution Worker") as HTMLSelectElement).disabled).toBe(true);
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
  fireEvent.click(screen.getByRole("button", { name: "Check installed harnesses" }));
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
  expect((screen.getByRole("button", { name: "Check installed harnesses" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("codex executable path") as HTMLInputElement).value).toBe("/staged/codex");
  expect(value.discovery).not.toHaveBeenCalled();
});
