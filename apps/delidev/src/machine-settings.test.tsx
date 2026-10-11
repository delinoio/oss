// SPDX-License-Identifier: Apache-2.0
import type { PropsWithChildren } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  WorkerService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

const earlier = "2026-10-09T00:00:00Z";
const later = "2026-10-09T00:00:05Z";

function machine(
  id = newRequestId(),
  revision = 7n,
  lastSeen = earlier,
  path = "/original/codex",
) {
  return create(ResourceSchema, {
    id,
    kind: EntityKind.MACHINE,
    schemaVersion: 1,
    revision,
    documentJson: encode({
      name: "Runner",
      last_seen: lastSeen,
      installations: [{ harness: "codex", explicit_path: path }],
    }),
  });
}

function fixture(
  initial = machine(),
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  }),
) {
  const read = vi.fn(async () => ({ resource: initial }));
  const discover = vi.fn(async (_request: unknown) => ({ machine: initial }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { getResource: read });
    router.service(WorkerService, { discoverHarnesses: discover });
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const hook = renderHook(
    ({ resource, active }) => useMachineSettingsController(resource, active),
    { initialProps: { resource: initial, active: true }, wrapper },
  );
  const refresh = async (resource: Resource) => {
    read.mockResolvedValueOnce({ resource });
    await act(async () => {
      await hook.result.current.refetch();
    });
  };
  const ready = async () => {
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
  };
  return { ...hook, initial, read, discover, client, refresh, ready };
}

it("accepts a fresh equal-revision heartbeat and keeps the business request revision", async () => {
  const value = fixture();
  await value.ready();
  await value.refresh(machine(value.initial.id, 7n, later));
  expect(value.result.current.data.last_seen).toBe(later);
  expect(value.result.current.current?.revision).toBe(7n);
  await act(async () => {
    await value.result.current.discovery.send({
      mutation: {
        id: value.initial.id,
        expectedRevision: value.result.current.current!.revision,
        requestId: newRequestId(),
      },
    });
  });
  expect(value.discover.mock.calls[0][0]).toMatchObject({
    mutation: { id: value.initial.id, expectedRevision: 7n },
  });
});

it("accepts a new successful read when the wall clock moves backward", async () => {
  const value = fixture();
  await value.ready();
  const earlierNow = Date.now() - 60_000;
  const clock = vi.spyOn(Date, "now").mockReturnValue(earlierNow);
  try {
    await value.refresh(machine(value.initial.id, 7n, later));
  } finally {
    clock.mockRestore();
  }
  expect(value.result.current.data.last_seen).toBe(later);
  expect(value.result.current.current?.revision).toBe(7n);
});

it("retains higher configuration and its observation after lower reads and read failures", async () => {
  const value = fixture();
  await value.ready();
  await value.refresh(machine(value.initial.id, 8n, later, "/new/codex"));
  await value.refresh(machine(value.initial.id, 7n, earlier));
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.data.last_seen).toBe(later);
  expect(value.result.current.data.installations).toMatchObject([
    { explicit_path: "/new/codex" },
  ]);
  value.read.mockRejectedValueOnce(
    new ConnectError("Offline", Code.Unavailable),
  );
  await act(async () => {
    await value.result.current.refetch();
  });
  expect(value.result.current.current?.revision).toBe(8n);
  expect(value.result.current.data.last_seen).toBe(later);
  expect(value.result.current.readError).toBeTruthy();
});

it.each([7n, 8n])(
  "keeps an acknowledgement over cached revision %s until a fresh compatible read",
  async (cachedRevision) => {
    const value = fixture();
    await value.ready();
    await value.refresh(
      machine(value.initial.id, cachedRevision, earlier, "/cached/codex"),
    );
    value.discover.mockResolvedValueOnce({
      machine: machine(value.initial.id, 8n, earlier, "/acknowledged/codex"),
    });
    await act(async () => {
      await value.result.current.discovery.send({
        mutation: {
          id: value.initial.id,
          expectedRevision: cachedRevision,
          requestId: newRequestId(),
        },
      });
    });
    value.rerender({ resource: value.initial, active: false });
    expect(value.result.current.data.installations).toMatchObject([
      { explicit_path: "/acknowledged/codex" },
    ]);
    await value.refresh(machine(value.initial.id, 7n, later));
    expect(value.result.current.current?.revision).toBe(8n);
    expect(value.result.current.data.installations).toMatchObject([
      { explicit_path: "/acknowledged/codex" },
    ]);
    await value.refresh(
      machine(value.initial.id, 8n, later, "/acknowledged/codex"),
    );
    expect(value.result.current.data.last_seen).toBe(later);
    expect(value.result.current.current?.revision).toBe(8n);
  },
);

it("accepts a newer read delivered with a lower mutation acknowledgement", async () => {
  const value = fixture();
  await value.ready();
  const newer = machine(value.initial.id, 9n, later, "/server/codex");
  const acknowledged = machine(
    value.initial.id,
    8n,
    earlier,
    "/acknowledged/codex",
  );
  value.read.mockResolvedValueOnce({ resource: newer });
  value.discover.mockResolvedValueOnce({ machine: acknowledged });
  await act(async () => {
    await Promise.all([
      value.result.current.refetch(),
      value.result.current.discovery.send({
        mutation: {
          id: value.initial.id,
          expectedRevision: 7n,
          requestId: newRequestId(),
        },
      }),
    ]);
  });
  expect(value.result.current.current?.revision).toBe(9n);
  expect(value.result.current.data.installations).toMatchObject([
    { explicit_path: "/server/codex" },
  ]);
});

it("preserves an editable path draft and its request revision during heartbeat refresh", async () => {
  const value = fixture();
  await value.ready();
  const paths = { codex: "/draft/codex" };
  act(() => value.result.current.setEdit({ revision: 7n, paths }));
  await value.refresh(machine(value.initial.id, 7n, later));
  expect(value.result.current.edit).toEqual({ revision: 7n, paths });
  expect(value.result.current.stale).toBe(false);
  expect(value.result.current.locked).toBe(true);
  expect(value.discover).not.toHaveBeenCalled();
  await value.refresh(machine(value.initial.id, 8n, later));
  expect(value.result.current.edit?.paths).toEqual(paths);
  expect(value.result.current.stale).toBe(true);
});

it.each(["foreign", "malformed", "unsupported"] as const)(
  "retains accepted observations and the read-error lock for a %s response",
  async (kind) => {
    const value = fixture();
    await value.ready();
    await value.refresh(machine(value.initial.id, 7n, later));
    const invalid = machine(value.initial.id, 8n);
    if (kind === "foreign") invalid.id = newRequestId();
    if (kind === "malformed")
      invalid.documentJson = encode({ installations: false });
    if (kind === "unsupported") invalid.schemaVersion = 99;
    await value.refresh(invalid);
    expect(value.result.current.current?.revision).toBe(7n);
    expect(value.result.current.data.last_seen).toBe(later);
    expect(value.result.current.readError).toMatchObject({
      code: Code.DataLoss,
    });
    expect(value.discover).not.toHaveBeenCalled();
  },
);

it("keeps a pending discovery and its exact retry while fresh heartbeats arrive", async () => {
  const value = fixture();
  await value.ready();
  value.discover.mockRejectedValueOnce(
    new ConnectError("Lost acknowledgement", Code.Unavailable),
  );
  const request = {
    mutation: {
      id: value.initial.id,
      expectedRevision: 7n,
      requestId: newRequestId(),
    },
    selectionsJson: encode({
      executables: [{ harness: "codex", path: "/draft/codex" }],
    }),
  };
  await act(async () => {
    await value.result.current.discovery.send(request);
  });
  expect(value.result.current.discovery.uncertain).toBe(true);
  await value.refresh(machine(value.initial.id, 7n, later));
  expect(value.result.current.pending).toBe(true);
  expect(value.result.current.discovery.uncertain).toBe(true);
  await act(async () => {
    await value.result.current.discovery.retry();
  });
  expect(value.discover.mock.calls[1][0]).toEqual(
    value.discover.mock.calls[0][0],
  );
});

it("starts a new same-ID opening from its own connection observation", async () => {
  const original = fixture();
  await original.ready();
  await original.refresh(machine(original.initial.id, 9n, later));
  original.unmount();
  const reopened = fixture(
    machine(original.initial.id, 7n, earlier),
    original.client,
  );
  await waitFor(() => expect(reopened.result.current.loading).toBe(false));
  expect(reopened.result.current.current?.revision).toBe(7n);
  expect(reopened.result.current.data.last_seen).toBe(earlier);
  expect(reopened.result.current.edit).toBeUndefined();
  expect(reopened.result.current.pending).toBe(false);
  await reopened.refresh(machine(original.initial.id, 7n, later));
  expect(reopened.result.current.data.last_seen).toBe(later);
});
