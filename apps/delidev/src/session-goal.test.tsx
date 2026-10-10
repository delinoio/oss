// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, NativeGoalAction, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId, type RequestSessionGoalActionRequest } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionGoal } from "./session-goal";
function fixture({ enabled = true, sidechat = false }: { enabled?: boolean; sidechat?: boolean } = {}) {
  const id = newRequestId(), execution = newRequestId(), thread = newRequestId(), machineId = newRequestId();
  let data: Record<string, unknown> = { archive: "active", recovery: "none", dispatch: "claimed", active_execution_id: execution, machine_id: machineId, initial_execution: { configuration: { harness: "codex" } }, execution: { execution_id: execution, native_thread_id: thread }, ...(sidechat ? { fork: { sidechat_parent_snapshot: {} } } : {}), native_goal: { enabled, source_execution_id: execution, source_native_thread_id: thread, observation: null } };
  let session = create(ResourceSchema, { id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode(data) });
  const original = session;
  const machine = create(ResourceSchema, { id: machineId, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({ worker_capabilities: ["native-codex-goals-v1"] }) });
  const read = vi.fn(() => ({ session }));
  const write = vi.fn(async (request: RequestSessionGoalActionRequest) => {
    const action = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, sessionId: id, schemaVersion: 1, revision: 1n, documentJson: encode({ type: "native-goal-action", state: "queued" }) });
    data = { ...data, native_goal: { ...data.native_goal as object, action_id: action.id, action_state: "accepted" } };
    session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(data) });
    return { action, session, requestId: request.mutation!.requestId, replayed: false };
  });
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_CODEX_GOALS_V1] }) });
    router.service(ResourceService, { getResource: () => ({ resource: machine }) });
    router.service(SessionService, { getSessionGoalState: read, requestSessionGoalAction: write });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const changed = vi.fn();
  return { write, changed, original, execution, thread, client, setState(value: object) { data = { ...data, ...value }; session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(data) }); }, setGoal(value: object) { data = { ...data, native_goal: value }; session = create(ResourceSchema, { ...session, revision: session.revision + 1n, documentJson: encode(data) }); }, render: () => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionGoal session={original} changed={changed} /></MutationIntents></QueryClientProvider></TransportProvider> };
}
it("preserves omitted status versus explicit managed-default budget reset", async () => {
  const f = fixture(); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Set or update goal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Set or update goal" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Change objective" }));
  fireEvent.change(screen.getByLabelText("Objective"), { target: { value: "Original goal 🙂" } });
  fireEvent.change(screen.getByLabelText("Token budget"), { target: { value: "default" } });
  fireEvent.click(screen.getByRole("button", { name: "Set native goal" }));
  await waitFor(() => expect(f.write).toHaveBeenCalledTimes(1));
  expect(f.write.mock.calls[0][0]).toMatchObject({ mutation: { id: f.original.id, expectedRevision: 1n }, expectedExecutionId: f.execution, expectedNativeThreadId: f.thread, action: NativeGoalAction.SET, set: { objective: "Original goal 🙂", budget: { case: "resetTokenBudget", value: true } } });
  expect(f.write.mock.calls[0][0].set?.status).toBeUndefined();
  expect((screen.getByRole("button", { name: "Refresh native goal" }) as HTMLButtonElement).disabled).toBe(true);
});
it("retains exact uncertain clear admission across readable absence without another native action", async () => {
  const f = fixture(); f.write.mockRejectedValueOnce(new ConnectError("Lost original receipt", Code.Unavailable)); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Clear native goal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Clear native goal" }));
  await screen.findByRole("button", { name: "Retry same goal request" });
  const original = f.write.mock.calls[0][0];
  f.setGoal({ enabled: true, source_execution_id: f.execution, source_native_thread_id: f.thread, observation: null, action_id: newRequestId(), action_state: "uncertain" });
  await f.client.invalidateQueries({ refetchType: "active" });
  expect((screen.getByRole("button", { name: "Clear native goal" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Reload saved goal state" }));
  expect(f.write).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry same goal request" }));
  await waitFor(() => expect(f.write).toHaveBeenCalledTimes(2));
  expect(f.write.mock.calls[1][0]).toEqual(original);
  expect(screen.queryByText(f.execution)).toBeNull(); expect(screen.queryByText(f.thread)).toBeNull();
});
it.each([{ enabled: false }, { sidechat: true }])("does not acquire Goals authority from capabilities alone: %j", async options => {
  const f = fixture(options); render(f.render());
  await screen.findByText("The last native observation reported no goal.");
  expect((screen.getByRole("button", { name: "Refresh native goal" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Clear native goal" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.write).not.toHaveBeenCalled();
});
it("sends native READ only for an explicit Refresh gesture; saved polling never sends it", async () => {
  const f = fixture(); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh native goal" }) as HTMLButtonElement).disabled).toBe(false));
  expect(f.write).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh native goal" }));
  await waitFor(() => expect(f.write).toHaveBeenCalledTimes(1));
  expect(f.write.mock.calls[0][0].action).toBe(NativeGoalAction.READ);
  expect(f.write.mock.calls[0][0].set).toBeUndefined();
});
it("retains a stale status draft and requires explicit original-scope revision review", async () => {
  const f = fixture(); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Set or update goal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Set or update goal" }));
  fireEvent.change(screen.getByLabelText("Goal status"), { target: { value: "paused" } });
  f.setGoal({ enabled: true, source_execution_id: f.execution, source_native_thread_id: f.thread, observation: null });
  await f.client.invalidateQueries({ refetchType: "active" });
  expect((screen.getByRole("button", { name: "Set native goal" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("Goal status") as HTMLSelectElement).value).toBe("paused");
  fireEvent.click(screen.getByRole("button", { name: "Use latest revision with this draft" }));
  fireEvent.click(screen.getByRole("button", { name: "Set native goal" }));
  await waitFor(() => expect(f.write).toHaveBeenCalledTimes(1));
  expect(f.write.mock.calls[0][0].mutation?.expectedRevision).toBe(2n);
  expect(f.write.mock.calls[0][0].set?.objective).toBeUndefined();
  expect(f.write.mock.calls[0][0].set?.budget.case).toBeUndefined();
});
it("does not transfer an original edit to a substituted thread", async () => {
  const f = fixture(); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Set or update goal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Set or update goal" }));
  fireEvent.change(screen.getByLabelText("Goal status"), { target: { value: "paused" } });
  f.setGoal({ enabled: true, source_execution_id: f.execution, source_native_thread_id: newRequestId(), observation: null });
  await f.client.invalidateQueries({ refetchType: "active" });
  expect(screen.queryByRole("button", { name: "Use latest revision with this draft" })).toBeNull();
  expect((screen.getByRole("button", { name: "Set native goal" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.write).not.toHaveBeenCalled();
});
it("keeps an unsent edit through an explicit native READ admission", async () => {
  const f = fixture(); render(f.render());
  await waitFor(() => expect((screen.getByRole("button", { name: "Set or update goal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Set or update goal" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Change objective" }));
  fireEvent.change(screen.getByLabelText("Objective"), { target: { value: "Unsent original draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Refresh native goal" }));
  await waitFor(() => expect(f.changed).toHaveBeenCalledTimes(1));
  expect((screen.getByLabelText("Objective") as HTMLTextAreaElement).value).toBe("Unsent original draft");
  expect(f.write.mock.calls[0][0].action).toBe(NativeGoalAction.READ);
});

it("keeps stopped or recovery state readable while native controls are disabled", async () => {
 const f = fixture(); render(f.render());
 await screen.findByText("The last native observation reported no goal.");
 f.setGoal({ enabled: true, source_execution_id: f.execution, source_native_thread_id: f.thread, observation: null, action_id: newRequestId(), action_state: "uncertain", problem_code: "recovery_required" });
 f.setState({ recovery: "needs-recovery", dispatch: "paused", active_execution_id: undefined });
 await f.client.invalidateQueries({ refetchType: "active" });
 await screen.findByText("Native controls require the original running execution. Saved state remains readable after it stops or requires recovery.");
 expect((screen.getByRole("button", { name: "Clear native goal" }) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button", { name: "Reload saved goal state" }));
 expect(f.write).not.toHaveBeenCalled();
});
