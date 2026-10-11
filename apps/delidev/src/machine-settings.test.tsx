// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  WorkerService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

function fixture() {
  const machine = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    revision: 7n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Original Runner",
      disabled: false,
      installations: [],
    }),
  });
  const initial = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.JOB,
    revision: 2n,
    schemaVersion: 1,
    documentJson: encode({
      type: "harness-discovery",
      machine_id: machine.id,
      state: "queued",
    }),
  });
  let observed: Resource | undefined = initial;
  let readError: Error | undefined;
  const read = vi.fn(async (request: { kind: EntityKind; id: string }) => {
    if (request.kind === EntityKind.MACHINE) return { resource: machine };
    if (readError) throw readError;
    return { resource: observed };
  });
  const discover = vi.fn(async () => ({ machine, job: initial }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { getResource: read });
    router.service(WorkerService, { discoverHarnesses: discover });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const hook = renderHook(
    ({ active }) => useMachineSettingsController(machine, active),
    { wrapper, initialProps: { active: true } },
  );
  const send = () =>
    hook.result.current.discovery.send({
      mutation: {
        id: machine.id,
        expectedRevision: machine.revision,
        requestId: newRequestId(),
      },
      verifyProtocol: hook.result.current.verify,
    });
  const refresh = async (resource?: Resource, error?: Error) => {
    observed = resource;
    readError = error;
    await act(async () => {
      await client.invalidateQueries();
    });
  };
  const terminal = (state: string, revision = initial.revision + 1n) =>
    create(ResourceSchema, {
      ...initial,
      revision,
      documentJson: encode({
        type: "harness-discovery",
        machine_id: machine.id,
        state,
        ...(state === "failed"
          ? {
              problem: {
                code: "unsupported",
                message: "Original inspection failed",
              },
            }
          : {}),
      }),
    });
  return {
    machine,
    initial,
    hook,
    read,
    discover,
    client,
    send,
    refresh,
    terminal,
  };
}

it.each(["succeeded", "failed", "canceled"])(
  "settles %s original inspection and submitted edits while retaining its result",
  async (state) => {
    const f = fixture();
    act(() => {
      f.hook.result.current.setEdit({
        revision: f.machine.revision,
        paths: { codex: "/submitted/codex" },
      });
      f.hook.result.current.setVerify(true);
    });
    await act(async () => {
      await f.send();
    });
    expect(f.hook.result.current.locked).toBe(true);
    await f.refresh(f.terminal(state));
    await waitFor(() => expect(f.hook.result.current.locked).toBe(false));
    const acceptedJob = f.hook.result.current.job;
    expect(acceptedJob).toMatchObject({
      id: f.initial.id,
      kind: EntityKind.JOB,
      revision: f.initial.revision,
    });
    expect(document(acceptedJob as Resource).state).toBe("queued");
    expect(f.hook.result.current.jobObservation.state).toBe(state);
    if (state === "failed")
      expect(f.hook.result.current.jobObservation.problem.message).toBe(
        "Original inspection failed",
      );
    expect(f.hook.result.current.edit).toBeUndefined();
    f.hook.rerender({ active: false });
    expect(f.hook.result.current.pending).toBe(false);
    expect(f.hook.result.current.job).toMatchObject({
      id: f.initial.id,
      kind: EntityKind.JOB,
      revision: f.initial.revision,
    });
    expect(document(f.hook.result.current.job as Resource).state).toBe(
      "queued",
    );
    expect(f.hook.result.current.jobObservation.state).toBe(state);
    expect(f.discover).toHaveBeenCalledOnce();
    expect(
      f.read.mock.calls
        .filter(([request]) => request.kind === EntityKind.JOB)
        .every(([request]) => request.id === f.initial.id),
    ).toBe(true);
    f.hook.unmount();
    f.client.clear();
  },
);

it("settles only submitted edits and keeps later unsent path and Verify changes locked", async () => {
  const f = fixture();
  act(() => {
    f.hook.result.current.setEdit({
      revision: f.machine.revision,
      paths: { codex: "/submitted/codex" },
    });
    f.hook.result.current.setVerify(true);
  });
  await act(async () => {
    await f.send();
  });
  const later = {
    revision: f.machine.revision,
    paths: { codex: "/later/codex" },
  };
  act(() => {
    f.hook.result.current.setEdit(later);
    f.hook.result.current.setVerify(false);
  });
  await f.refresh(f.terminal("succeeded"));
  await waitFor(() => expect(f.hook.result.current.pending).toBe(false));
  expect(f.hook.result.current.edit).toBe(later);
  expect(f.hook.result.current.locked).toBe(true);
  act(() => f.hook.result.current.setEdit(undefined));
  expect(f.hook.result.current.locked).toBe(true);
  expect(f.discover).toHaveBeenCalledOnce();
  f.hook.unmount();
  f.client.clear();
});

it.each([
  "queued",
  "claimed",
  "uncertain",
  "missing",
  "foreign",
  "kind",
  "schema",
  "regressive",
  "malformed",
  "unknown-state",
  "machine",
  "type",
  "read-error",
  "success-problem",
])("retains the original gate for %s observation", async (mode) => {
  const f = fixture();
  await act(async () => {
    await f.send();
  });
  let row: Resource | undefined = f.terminal("succeeded");
  let error: Error | undefined;
  if (["queued", "claimed", "uncertain"].includes(mode)) row = f.terminal(mode);
  else if (mode === "missing") row = undefined;
  else if (mode === "foreign") row.id = newRequestId();
  else if (mode === "kind") row.kind = EntityKind.MACHINE;
  else if (mode === "schema") row.schemaVersion = 99;
  else if (mode === "regressive") {
    await f.refresh(f.terminal("claimed", 8n));
    row.revision = 7n;
  } else if (mode === "malformed")
    row.documentJson = new TextEncoder().encode("{unreadable");
  else if (mode === "unknown-state")
    row.documentJson = encode({ state: "unknown", type: "harness-discovery" });
  else if (mode === "machine")
    row.documentJson = encode({
      state: "succeeded",
      type: "harness-discovery",
      machine_id: newRequestId(),
    });
  else if (mode === "type")
    row.documentJson = encode({ state: "succeeded", type: "save-repository" });
  else if (mode === "read-error")
    error = new ConnectError("Original status failed", Code.Unavailable);
  else if (mode === "success-problem")
    row.documentJson = encode({
      state: "succeeded",
      type: "harness-discovery",
      problem: { code: "unavailable" },
    });
  await f.refresh(row, error);
  expect(f.hook.result.current.pending).toBe(true);
  expect(f.hook.result.current.locked).toBe(true);
  expect(f.hook.result.current.jobObservation.terminal).toBe(false);
  expect(f.discover).toHaveBeenCalledOnce();
  f.hook.unmount();
  f.client.clear();
});

it("does not settle a superseding job from the preceding job's terminal response", async () => {
  const f = fixture();
  await act(async () => {
    await f.send();
  });
  const next = create(ResourceSchema, { ...f.initial, id: newRequestId() });
  act(() => f.hook.result.current.setJob(next));
  await f.refresh(f.terminal("succeeded"));
  expect(f.hook.result.current.job).toEqual(next);
  expect(f.hook.result.current.pending).toBe(true);
  expect(f.discover).toHaveBeenCalledOnce();
  f.hook.unmount();
  f.client.clear();
});
