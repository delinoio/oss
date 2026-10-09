// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SessionQuery, SessionService, newRequestId, type RemoveQueuedInputRequest, type EditQueuedInputRequest, type SteerQueuedInputRequest } from "@delinoio/delidev-api-client";
import { QueuedInput, PendingQueueInputs } from "./queue";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { encode } from "./documents";

type Request = RemoveQueuedInputRequest | EditQueuedInputRequest | SteerQueuedInputRequest;
function fixture(kind: "removal" | "edit" | "Steer" = "removal") {
  const sessionId = newRequestId(), otherId = newRequestId(), inputId = newRequestId();
  const executionId = newRequestId(), turnId = newRequestId();
  const session = create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 4n, documentJson: encode({ outcome: "running", archive: "active", active_execution_id: executionId, execution: { execution_id: executionId, native_turn_id: turnId } }) });
  const resource = create(ResourceSchema, { id: inputId, sessionId, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 6n, documentJson: encode({ prompt: "Original queued input", sequence: 3, delivery: "queued", mode: "plan" }) });
  const requests: Request[] = [];
  let accounting = 0;
  const receipts = new Map<string, object>();
  const mutate = vi.fn(async (request: Request) => {
    requests.push(request);
    const id = request.mutation!.requestId;
    if (!receipts.has(id)) { receipts.set(id, { change: { requestId: id, session, input: create(ResourceSchema, { ...resource, revision: 7n, documentJson: encode({ delivery: "removed", mode: "plan", sequence: 3 }) }) } }); accounting++; throw new ConnectError("Lost acknowledgment", Code.Unavailable); }
    return receipts.get(id)!;
  });
  const transport = createRouterTransport(router => router.service(SessionService, { editQueuedInput: mutate, removeQueuedInput: mutate, steerQueuedInput: mutate }));
  const refresh = vi.fn();
  function View() {
    const [present, setPresent] = useState(true), [selected, setSelected] = useState(sessionId);
    return <><button onClick={() => setPresent(false)}>Publish tombstone</button><button onClick={() => setSelected(selected === sessionId ? otherId : sessionId)}>Switch session</button><button onClick={refresh}>Refetch original queue</button>
      {present && selected === sessionId ? <QueuedInput resource={resource} session={session} refresh={refresh} /> : null}
      <PendingQueueInputs sessionId={selected} presentInputIds={new Set(present && selected === sessionId ? [inputId] : [])} refresh={refresh} />
    </>;
  }
  const client = new QueryClient();
  const view = <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><View /></MutationIntents></QueryClientProvider></TransportProvider>;
  const start = async () => {
    if (kind === "edit") { fireEvent.click(screen.getByRole("button", { name: "Edit input" })); fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), { target: { value: "Exact original edit" } }); fireEvent.click(screen.getByRole("button", { name: "Save input" })); }
    else fireEvent.click(screen.getByRole("button", { name: kind === "removal" ? "Remove input" : "Steer with this input" }));
    await screen.findByRole("button", { name: `Retry the same ${kind}` });
  };
  return { view, start, mutate, requests, refresh, inputId, executionId, turnId, accounting: () => accounting };
}

it.each(["removal", "edit", "Steer"] as const)("keeps exact original %s retry after tombstone, refetch and session navigation", async kind => {
  const f = fixture(kind); render(f.view); await f.start();
  expect(screen.getAllByRole("button", { name: `Retry the same ${kind}` })).toHaveLength(1);
  expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Publish tombstone" }));
  expect(screen.queryByText("Original queued input")).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove input" })).toBeNull();
  expect(screen.getByRole("region", { name: "Pending input actions" })).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "Refetch original queue" }));
  fireEvent.click(screen.getByRole("button", { name: "Switch session" }));
  expect(screen.queryByRole("button", { name: `Retry the same ${kind}` })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Switch session" }));
  fireEvent.click(screen.getByRole("button", { name: `Retry the same ${kind}` }));
  await waitFor(() => expect(f.mutate).toHaveBeenCalledTimes(2));
  expect(f.requests[1]).toEqual(f.requests[0]);
  const method = kind === "removal" ? SessionQuery.removeQueuedInput : kind === "edit" ? SessionQuery.editQueuedInput : SessionQuery.steerQueuedInput;
  expect(toBinary(method.input, f.requests[1]!)).toEqual(toBinary(method.input, f.requests[0]!));
  expect(f.accounting()).toBe(1);
  if (kind === "Steer") expect(f.requests[1]).toMatchObject({ expectedExecutionId: f.executionId, expectedTurnId: f.turnId });
  if (kind === "edit") expect(f.requests[1]).toMatchObject({ prompt: "Exact original edit", skills: { selections: [] }, attachments: [] });
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull());
  expect(f.refresh).toHaveBeenCalled();
});

it("preserves a pending original action across row unmount and clears it on acknowledgment", async () => {
  const f = fixture(); let release!: () => void;
  f.mutate.mockImplementationOnce(async () => { await new Promise<void>(resolve => { release = resolve; }); return {}; });
  render(f.view); fireEvent.click(screen.getByRole("button", { name: "Remove input" }));
  await waitFor(() => expect(f.mutate).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "Publish tombstone" }));
  expect(screen.getByText("Sending the original input action…")).toBeDefined();
  expect(screen.queryByRole("button", { name: "Retry the same removal" })).toBeNull();
  await act(async () => release());
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull());
  expect(f.mutate).toHaveBeenCalledOnce();
});

it.each(["foreign session", "wrong input", "invalid revision", "invalid request"])("does not expose malformed retained identity: %s", async invalid => {
  const sessionId = newRequestId(), inputId = newRequestId();
  const mutate = vi.fn(async () => { throw new ConnectError("Uncertain", Code.Unavailable); });
  const transport = createRouterTransport(router => router.service(SessionService, { removeQueuedInput: mutate }));
  function View() {
    const remove = useRetainedMutation(`remove-input:${inputId}`, SessionQuery.removeQueuedInput);
    return <><button onClick={() => void remove.send({ sessionId: invalid === "foreign session" ? newRequestId() : sessionId, mutation: { id: invalid === "wrong input" ? newRequestId() : inputId, expectedRevision: invalid === "invalid revision" ? 0n : 1n, requestId: invalid === "invalid request" ? "bad" : newRequestId() } })}>Submit invalid fixture</button><PendingQueueInputs sessionId={sessionId} presentInputIds={new Set()} refresh={() => {}} /></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><MutationIntents><View /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Submit invalid fixture" }));
  await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
  expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull();
});

it("discards retry ownership when the connection registry is replaced", async () => {
  const f = fixture(); const mounted = render(f.view); await f.start();
  fireEvent.click(screen.getByRole("button", { name: "Publish tombstone" }));
  expect(screen.getByRole("button", { name: "Retry the same removal" })).toBeDefined();
  mounted.unmount(); render(f.view);
  expect(screen.queryByRole("button", { name: "Retry the same removal" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull();
  expect(f.mutate).toHaveBeenCalledOnce();
});
