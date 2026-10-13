// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { create, toJsonString } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  InboxService,
  SteerQueuedInputRequestSchema,
} from "@delinoio/delidev-api-client";
import { App } from "./app";
import {
  documentBytes,
  Operation,
  PendingPhase,
  ProtectedState,
  type Pending,
} from "./state";
import { en, ko } from "./localization";

vi.mock("./platform", () => ({
  storage: { read: async () => null, write: async () => {} },
  observeForeground: async () => () => {},
  permission: async () => false,
  notify: async () => false,
  tls: async () => "ok",
}));
// Keep the fixture read-only and independent of network synchronization.
vi.mock("./connection", async () => {
  const { QueryClient } = await import("@tanstack/react-query");
  const { Status } =
    await vi.importActual<typeof import("./connection")>("./connection");
  return {
    Status,
    Connection: class {
      active = false;
      status = Status.Connecting;
      cache = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      });
      constructor(
        _state: unknown,
        _id: string,
        readonly changed: (status: typeof Status.Live) => void,
      ) {}
      async resume() {
        this.active = true;
        this.status = Status.Live;
        this.changed(Status.Live);
      }
      suspend() {
        this.active = false;
        void this.cache.cancelQueries();
        this.cache.clear();
      }
      canMutate() {
        return this.active;
      }
    },
  };
});
afterEach(cleanup);
// jsdom does not implement modal dialogs.
HTMLDialogElement.prototype.showModal = function showModal() {
  this.setAttribute("open", "");
};
HTMLDialogElement.prototype.close = function close() {
  this.removeAttribute("open");
};

const requestJson = toJsonString(
  SteerQueuedInputRequestSchema,
  create(SteerQueuedInputRequestSchema, {
    mutation: {
      id: "queue-input",
      expectedRevision: 4n,
      requestId: "original-request-id",
    },
    sessionId: "session",
    expectedExecutionId: "original-execution",
    expectedTurnId: "original-turn",
  }),
);
// Legacy records retain the session ID as the target; no migration is applied.
const legacyPending: Pending = {
  operation: Operation.Steer,
  phase: PendingPhase.Uncertain,
  target: "session",
  request: requestJson,
};

type Queue =
  | {
      sessionId?: string;
      revision?: bigint;
      id?: string;
      schemaVersion?: number;
    }
  | "missing"
  | "failure";

function fixture(queue: Queue, pending: Pending = legacyPending) {
  const state = new ProtectedState({
    read: async () => null,
    write: async () => {},
  });
  state.state = {
    version: 1,
    profiles: [
      {
        id: "profile",
        name: "Fixture",
        origin: "https://fixture.invalid",
        serverId: "server",
        deviceId: "device",
        token: "fixture",
        pending,
      },
    ],
    selectedProfile: "profile",
    language: "en",
    theme: "system",
    notifications: false,
  };
  vi.spyOn(state, "load").mockResolvedValue(undefined);
  const resource = (
    kind: EntityKind,
    id: string,
    sessionId: string,
    revision: bigint,
    schemaVersion = 1,
  ) =>
    create(ResourceSchema, {
      kind,
      id,
      sessionId,
      revision,
      schemaVersion,
      documentJson: documentBytes({ name: "Original conversation" }),
    });
  const reads: { kind: EntityKind; id: string }[] = [];
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      listSessions: () => ({ sessions: [] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: (request) => {
        reads.push({ kind: request.kind, id: request.id });
        if (request.kind !== EntityKind.QUEUE)
          return { resource: resource(EntityKind.SESSION, "session", "", 9n) };
        if (queue === "missing")
          throw new ConnectError("missing", Code.NotFound);
        if (queue === "failure")
          throw new ConnectError("unavailable", Code.Unavailable);
        return {
          resource: resource(
            EntityKind.QUEUE,
            queue.id ?? "queue-input",
            queue.sessionId ?? "session",
            queue.revision ?? 4n,
            queue.schemaVersion,
          ),
        };
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({}),
    });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const retry = vi.spyOn(state, "retry").mockResolvedValue(undefined);
  render(<App state={state} />);
  return { state, perform, retry, reads };
}
async function inspect() {
  const button = await screen.findByRole("button", { name: en.inspect });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(button);
}
const retryButton = () =>
  screen.getByRole("button", { name: en.retry }) as HTMLButtonElement;

it("inspects the original QUEUE input, never the session, and enables the exact retry", async () => {
  const f = fixture({});
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([{ kind: EntityKind.QUEUE, id: "queue-input" }]);
  expect(retryButton().disabled).toBe(false);
  fireEvent.click(retryButton());
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.retry).toHaveBeenCalledWith("profile"));
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.state.profile("profile").pending).toEqual(legacyPending);
});
it("inspects the original queue input for a pending record with a new-style target", async () => {
  const f = fixture({}, { ...legacyPending, target: "queue-input" });
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([{ kind: EntityKind.QUEUE, id: "queue-input" }]);
});
it.each([
  ["missing", "missing", en.steerTargetMissing],
  ["foreign session", { sessionId: "other-session" }, en.steerTargetChanged],
  ["revised", { revision: 5n }, en.steerTargetChanged],
  ["different id", { id: "replacement-input" }, en.steerTargetChanged],
  ["unsupported schema", { schemaVersion: 99 }, en.steerTargetChanged],
] as [string, Queue, string][])(
  "keeps retry disabled and shows guidance when the original input is %s",
  async (_name, queue, message) => {
    const f = fixture(queue);
    await inspect();
    expect((await screen.findByRole("alert")).textContent).toBe(message);
    expect(screen.queryByText(en.inspected)).toBeNull();
    expect(retryButton().disabled).toBe(true);
    expect(f.reads).toEqual([{ kind: EntityKind.QUEUE, id: "queue-input" }]);
    expect(f.perform).not.toHaveBeenCalled();
    expect(f.retry).not.toHaveBeenCalled();
    expect(f.state.profile("profile").pending).toEqual(legacyPending);
  },
);
it("keeps retry disabled with the generic message when the queue read fails", async () => {
  const f = fixture("failure");
  await inspect();
  expect((await screen.findByRole("alert")).textContent).toBe(en.error);
  expect(retryButton().disabled).toBe(true);
  expect(f.state.profile("profile").pending).toEqual(legacyPending);
});
it("clears a prior mismatch message and disables retry again after a repeated inspect", async () => {
  const f = fixture("missing");
  await inspect();
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: en.inspect }));
  await waitFor(() => expect(f.reads.length).toBe(2));
  expect(retryButton().disabled).toBe(true);
});
it("keeps the SESSION inspect behavior for other operations", async () => {
  const f = fixture(
    {},
    {
      operation: Operation.Send,
      phase: PendingPhase.Uncertain,
      target: "session",
      request: '{"requestId":"original-request","sessionId":"session"}',
    },
  );
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([{ kind: EntityKind.SESSION, id: "session" }]);
  expect(retryButton().disabled).toBe(false);
});
it("provides the Steer guidance in English and Korean", () => {
  for (const key of ["steerTargetMissing", "steerTargetChanged"] as const) {
    expect(en[key].length).toBeGreaterThan(0);
    expect(ko[key].length).toBeGreaterThan(0);
  }
  expect(Object.keys(ko).sort()).toEqual(Object.keys(en).sort());
});
