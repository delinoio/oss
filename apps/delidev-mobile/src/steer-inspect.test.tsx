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
  type SteerQueuedInputRequest,
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
const profileId = "00000000-0000-7000-8000-000000000001";
const receiptRead = { kind: EntityKind.STEER, id: "original-request-id" };
const queueRead = { kind: EntityKind.QUEUE, id: "queue-input" };
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
type Receipt =
  | "missing"
  | "failure"
  | {
      kind?: EntityKind;
      id?: string;
      sessionId?: string;
      schemaVersion?: number;
      document?: Record<string, unknown>;
    };

function fixture(
  queue: Queue,
  pending: Pending = legacyPending,
  receipt: Receipt = "missing",
  realRetry = false,
) {
  const state = new ProtectedState({
    read: async () => null,
    write: async () => {},
  });
  state.state = {
    version: 1,
    profiles: [
      {
        id: profileId,
        name: "Fixture",
        origin: "https://fixture.invalid",
        serverId: "00000000-0000-7000-8000-000000000002",
        deviceId: "00000000-0000-7000-8000-000000000003",
        token: "a".repeat(43),
        pending,
      },
    ],
    selectedProfile: profileId,
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
    document: Record<string, unknown> = { name: "Original conversation" },
  ) =>
    create(ResourceSchema, {
      kind,
      id,
      sessionId,
      revision,
      schemaVersion,
      documentJson: documentBytes(document),
    });
  const reads: { kind: EntityKind; id: string }[] = [];
  const dispatch = vi.fn(async (request: SteerQueuedInputRequest) => ({
    requestId: request.mutation?.requestId,
    replayed: true,
  }));
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      listSessions: () => ({ sessions: [] }),
      listQueue: () => ({ inputs: [] }),
      steerQueuedInput: dispatch,
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: (request) => {
        reads.push({ kind: request.kind, id: request.id });
        if (request.kind === EntityKind.STEER) {
          if (receipt === "missing")
            throw new ConnectError("missing", Code.NotFound);
          if (receipt === "failure")
            throw new ConnectError("unavailable", Code.Unavailable);
          return {
            resource: resource(
              receipt.kind ?? EntityKind.STEER,
              receipt.id ?? "original-request-id",
              receipt.sessionId ?? "session",
              1n,
              receipt.schemaVersion,
              receipt.document ?? {
                version: 1,
                input_id: "queue-input",
                execution_id: "original-execution",
                native_turn_id: "original-turn",
              },
            ),
          };
        }
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
  const retry = vi.spyOn(state, "retry");
  if (!realRetry) retry.mockResolvedValue(undefined);
  render(<App state={state} />);
  return { state, perform, retry, reads, dispatch };
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

it("uses the unchanged original QUEUE input when no receipt exists", async () => {
  const f = fixture({});
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([receiptRead, queueRead]);
  expect(retryButton().disabled).toBe(false);
  fireEvent.click(retryButton());
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.retry).toHaveBeenCalledWith(profileId));
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
});
it("inspects the original queue input for a pending record with a new-style target", async () => {
  const f = fixture({}, { ...legacyPending, target: "queue-input" });
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([receiptRead, queueRead]);
});
it("confirms an exact receipt replay after the original Steer revised the queue", async () => {
  const f = fixture({ revision: 5n }, legacyPending, {}, true);
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([receiptRead]);
  expect(f.dispatch).not.toHaveBeenCalled();
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
  fireEvent.click(retryButton());
  expect(f.dispatch).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.dispatch).toHaveBeenCalledTimes(1));
  expect(
    toJsonString(SteerQueuedInputRequestSchema, f.dispatch.mock.calls[0][0]),
  ).toBe(requestJson);
  expect(f.dispatch.mock.calls[0][0].mutation?.requestId).toBe(
    "original-request-id",
  );
  expect(await f.retry.mock.results[0].value).toMatchObject({
    requestId: "original-request-id",
    replayed: true,
  });
  await waitFor(() =>
    expect(f.state.profile(profileId).pending).toBeUndefined(),
  );
});
it("permits a matching receipt when the original queue input is missing", async () => {
  const f = fixture("missing", legacyPending, {});
  await inspect();
  await screen.findByText(en.inspected);
  expect(f.reads).toEqual([receiptRead]);
  expect(retryButton().disabled).toBe(false);
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
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
    expect(f.reads).toEqual([receiptRead, queueRead]);
    expect(f.perform).not.toHaveBeenCalled();
    expect(f.retry).not.toHaveBeenCalled();
    expect(f.state.profile(profileId).pending).toEqual(legacyPending);
  },
);
it.each([
  ["wrong resource kind", { kind: EntityKind.QUEUE }],
  ["wrong request ID", { id: "another-request" }],
  ["foreign session", { sessionId: "another-session" }],
  ["unsupported schema", { schemaVersion: 99 }],
  [
    "wrong input",
    {
      document: {
        version: 1,
        input_id: "another-input",
        execution_id: "original-execution",
        native_turn_id: "original-turn",
      },
    },
  ],
  [
    "wrong execution",
    {
      document: {
        version: 1,
        input_id: "queue-input",
        execution_id: "another-execution",
        native_turn_id: "original-turn",
      },
    },
  ],
  [
    "wrong native turn",
    {
      document: {
        version: 1,
        input_id: "queue-input",
        execution_id: "original-execution",
        native_turn_id: "another-turn",
      },
    },
  ],
  [
    "wrong document version",
    {
      document: {
        version: 2,
        input_id: "queue-input",
        execution_id: "original-execution",
        native_turn_id: "original-turn",
      },
    },
  ],
] as [string, Exclude<Receipt, string>][])(
  "rejects a %s receipt without consulting a revised queue",
  async (_name, receipt) => {
    const f = fixture({ revision: 5n }, legacyPending, receipt);
    await inspect();
    expect((await screen.findByRole("alert")).textContent).toBe(
      en.steerTargetChanged,
    );
    expect(f.reads).toEqual([receiptRead]);
    expect(retryButton().disabled).toBe(true);
    expect(f.dispatch).not.toHaveBeenCalled();
    expect(f.state.profile(profileId).pending).toEqual(legacyPending);
  },
);
it("keeps the original pending request after a receipt read failure", async () => {
  const f = fixture({ revision: 5n }, legacyPending, "failure");
  await inspect();
  expect((await screen.findByRole("alert")).textContent).toBe(en.error);
  expect(f.reads).toEqual([receiptRead]);
  expect(retryButton().disabled).toBe(true);
  expect(f.dispatch).not.toHaveBeenCalled();
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
});
it("retains the original request after a lost replay response, then recovers", async () => {
  const f = fixture({ revision: 5n }, legacyPending, {}, true);
  f.dispatch.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable));
  await inspect();
  await screen.findByText(en.inspected);
  fireEvent.click(retryButton());
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.dispatch).toHaveBeenCalledTimes(1));
  await screen.findByRole("alert");
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
  await inspect();
  await screen.findByText(en.inspected);
  fireEvent.click(retryButton());
  fireEvent.click(await screen.findByRole("button", { name: en.confirm }));
  await waitFor(() => expect(f.dispatch).toHaveBeenCalledTimes(2));
  expect(
    f.dispatch.mock.calls.map(([request]) =>
      toJsonString(SteerQueuedInputRequestSchema, request),
    ),
  ).toEqual([requestJson, requestJson]);
  await waitFor(() =>
    expect(f.state.profile(profileId).pending).toBeUndefined(),
  );
});
it("keeps retry disabled with the generic message when the queue read fails", async () => {
  const f = fixture("failure");
  await inspect();
  expect((await screen.findByRole("alert")).textContent).toBe(en.error);
  expect(retryButton().disabled).toBe(true);
  expect(f.state.profile(profileId).pending).toEqual(legacyPending);
});
it("clears a prior mismatch message and disables retry again after a repeated inspect", async () => {
  const f = fixture("missing");
  await inspect();
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: en.inspect }));
  await waitFor(() => expect(f.reads.length).toBe(4));
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
