// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  EventAction,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  WatchEventsResponseSchema,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode, document as readDocument } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";
import { SessionNameEditorProvider } from "./session-name-editor";
import { SessionTabsProvider } from "./session-tabs";
import { turnTimingConfirmed } from "./turn-timing";

// Session-level coverage for the active turn timer gate. Only the owning
// Session resource synchronization may freeze the estimate; independent
// operation failures must stay visible without relabelling the clock.
// Retained mutation state is connection-scoped by Session ID, so every test
// owns fresh identities.
let sessionId = newRequestId(),
  executionId = newRequestId(),
  inputId = newRequestId(),
  jobId = newRequestId(),
  messageId = newRequestId();
beforeEach(() => {
  sessionId = newRequestId();
  executionId = newRequestId();
  inputId = newRequestId();
  jobId = newRequestId();
  messageId = newRequestId();
});
const accepted = Date.parse("2026-10-09T10:00:00.000Z");
const at = (seconds: number) => new Date(accepted + seconds * 1000);
const iso = (seconds: number) => at(seconds).toISOString();

function sessionResource(
  revision: bigint,
  observedSeconds: number,
  extra: Record<string, unknown> = {},
  execution: Record<string, unknown> = {},
): Resource {
  return create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision,
    updatedAt: iso(observedSeconds),
    documentJson: encode({
      name: "Timed session",
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
      dispatch: "running",
      recovery: "none",
      initial_execution: { id: executionId, input_id: inputId },
      active_execution_id: executionId,
      execution: {
        execution_id: executionId,
        input_id: inputId,
        job_id: jobId,
        last_sequence: 2,
        accepted_inputs: [{ input_id: inputId, prompt_digest: "a".repeat(64) }],
        native_thread_id: "original-thread",
        native_turn_id: "original-turn",
        outcome: "running",
        turn_timing: { accepted_at: iso(0) },
        ...execution,
      },
      ...extra,
    }),
  });
}
function userMessage(): Resource {
  return create(ResourceSchema, {
    id: messageId,
    sessionId,
    kind: EntityKind.MESSAGE,
    schemaVersion: 1,
    revision: 1n,
    updatedAt: iso(0),
    documentJson: encode({
      role: "user",
      text: "Original timed input",
      state: "complete",
      execution_id: executionId,
      input_id: inputId,
      native_thread_id: "original-thread",
      native_turn_id: "original-turn",
      first_sequence: 3,
      last_sequence: 4,
      turn_timing: { accepted_at: iso(0) },
    }),
  });
}

enum Failure {
  None = "none",
  Control = "control",
  Queue = "queue",
  Interactions = "interactions",
}
interface Options {
  failure?: Failure;
  controlCode?: Code;
}
function fixture(options: Options = {}) {
  let current = sessionResource(1n, 0);
  const failures = {
    messages: false,
    queue: options.failure === Failure.Queue,
    interactions: options.failure === Failure.Interactions,
    snapshot: false,
    watch: false,
  };
  const events: ReturnType<typeof create<typeof WatchEventsResponseSchema>>[] =
    [];
  let wake = () => {};
  const publish = (resource: Resource) => {
    current = resource;
    events.push(
      create(WatchEventsResponseSchema, {
        cursor: `fixture-${newRequestId()}`,
        id: newRequestId(),
        entityId: resource.id,
        kind: resource.kind,
        sessionId,
        revision: resource.revision,
        action: EventAction.UPDATED,
        time: new Date().toISOString(),
      }),
    );
    wake();
  };
  const control = vi.fn(async () => {
    // Code.Aborted is the definitive Conflict rejection; Unavailable is uncertain.
    throw new ConnectError(
      "The session changed before the control was applied",
      options.controlCode ?? Code.Aborted,
    );
  });
  const enqueue = vi.fn();
  const listQueue = vi.fn(() => {
    if (failures.queue)
      throw new ConnectError("Fixture queue read failed", Code.Internal);
    return { inputs: [], nextPageToken: "" };
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, {
      listQueue,
      controlSession: control,
      enqueueInput: enqueue,
    });
    router.service(ResourceService, {
      getSnapshot: () => {
        if (failures.snapshot)
          throw new ConnectError("Fixture snapshot failed", Code.Internal);
        return { resources: [current], cursor: "fixture-snapshot" };
      },
      getResource: (request) => ({
        resource: request.id === sessionId ? current : undefined,
      }),
      listResources: (request) => {
        if (request.filter?.kind === EntityKind.MESSAGE) {
          if (failures.messages)
            throw new ConnectError(
              "Fixture message read failed",
              Code.Internal,
            );
          return { resources: [userMessage()], nextPageToken: "" };
        }
        if (request.filter?.kind === EntityKind.INTERACTION) {
          if (failures.interactions)
            throw new ConnectError(
              "Fixture interaction read failed",
              Code.Internal,
            );
          return { resources: [], nextPageToken: "" };
        }
        return { resources: [], nextPageToken: "" };
      },
      async *watchEvents(_request, context) {
        if (failures.watch)
          throw new ConnectError("Fixture stream failed", Code.Internal);
        while (!context.signal.aborted) {
          if (failures.watch)
            throw new ConnectError("Fixture stream failed", Code.Internal);
          while (events.length && !context.signal.aborted)
            yield events.shift()!;
          if (!context.signal.aborted)
            await new Promise<void>((resolve) => {
              const done = () => {
                context.signal.removeEventListener("abort", done);
                resolve();
              };
              wake = done;
              context.signal.addEventListener("abort", done, { once: true });
            });
        }
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionNameEditorProvider>
            <SessionTabsProvider>
              <SessionView
                id={sessionId}
                draft=""
                setDraft={vi.fn()}
                active
                embedded={false}
              />
            </SessionTabsProvider>
          </SessionNameEditorProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { ui, client, control, enqueue, failures, publish };
}
// Real timers drive the in-memory transport; only the presentation clock is fake.
function fakeClock() {
  vi.useFakeTimers({
    toFake: ["setInterval", "clearInterval", "performance"],
  });
}
const rejection = /The DeliDev request could not complete\./;
const timing = () =>
  document.querySelector(".turn-time")?.textContent ?? undefined;
async function expectTiming(text: string) {
  await waitFor(() => expect(timing()).toBe(text));
}
async function tick(seconds: number) {
  await act(async () => {
    vi.advanceTimersByTime(seconds * 1000);
  });
}
async function openTimedSession(options: Options = {}) {
  fakeClock();
  const f = fixture(options);
  render(f.ui);
  await screen.findByRole("heading", { name: "Timed session" });
  await expectTiming("In progress · 0s");
  return f;
}

afterEach(async () => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  await i18n.changeLanguage("en");
});

it("derives timing confirmation only from owning-resource synchronization", () => {
  const confirmed = {
    liveLive: true,
    liveFailed: false,
    messagesFailed: false,
    controlUncertain: false,
    recoveryRequired: false,
    unconfirmedResponses: 0,
  };
  expect(turnTimingConfirmed(confirmed)).toBe(true);
  for (const unconfirmed of [
    { liveLive: false },
    { liveFailed: true },
    { messagesFailed: true },
    { controlUncertain: true },
    { recoveryRequired: true },
    { unconfirmedResponses: 1 },
  ])
    expect(turnTimingConfirmed({ ...confirmed, ...unconfirmed })).toBe(false);
});

it("keeps advancing a Live turn timer after a definitive control rejection while the error stays visible", async () => {
  const f = await openTimedSession();
  await tick(3);
  await expectTiming("In progress · 3s");
  fireEvent.click(screen.getByRole("button", { name: "Stop" }));
  await waitFor(() => expect(f.control).toHaveBeenCalledTimes(1));
  await screen.findAllByText(rejection);
  // The rejection is definitive: the control stays retryable, not uncertain.
  expect(screen.getByRole("button", { name: "Stop" })).toHaveProperty(
    "disabled",
    false,
  );
  expect(timing()).toBe("In progress · 3s");
  await tick(2);
  expect(timing()).toBe("In progress · 5s");
  // A newer Session revision still advances the same confirmed clock.
  await act(async () => {
    f.publish(sessionResource(2n, 30));
  });
  await expectTiming("In progress · 30s");
  await tick(4);
  expect(timing()).toBe("In progress · 34s");
  expect(screen.getAllByText(rejection).length).toBeGreaterThan(0);
  expect(timing()).not.toContain("Unconfirmed");
  expect(f.enqueue).not.toHaveBeenCalled();
});

it.each([
  [Failure.Queue, "queue"],
  [Failure.Interactions, "interaction"],
])(
  "does not unconfirm timing for an isolated %s read error",
  async (failure) => {
    const f = await openTimedSession({ failure });
    await tick(7);
    await expectTiming("In progress · 7s");
    await act(async () => {
      f.publish(sessionResource(2n, 20));
    });
    await expectTiming("In progress · 20s");
    await tick(1);
    expect(timing()).toBe("In progress · 21s");
    expect(timing()).not.toContain("Unconfirmed");
    expect(f.enqueue).not.toHaveBeenCalled();
  },
);

it("freezes the estimate as Unconfirmed when the owning stream fails and resumes after a new Live revision", async () => {
  const f = await openTimedSession();
  await tick(6);
  await expectTiming("In progress · 6s");
  // Break the owning stream and wake the watcher so the failure is observed.
  f.failures.watch = true;
  f.failures.snapshot = true;
  await act(async () => {
    f.publish(sessionResource(2n, 6));
  });
  await expectTiming("Unconfirmed · 6s");
  await tick(30);
  expect(timing()).toBe("Unconfirmed · 6s");
  expect(f.enqueue).not.toHaveBeenCalled();
});

it("freezes the estimate when the message read fails", async () => {
  const f = await openTimedSession();
  await tick(4);
  await expectTiming("In progress · 4s");
  f.failures.messages = true;
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await expectTiming("Unconfirmed · 4s");
  await tick(30);
  expect(timing()).toBe("Unconfirmed · 4s");
  expect(f.enqueue).not.toHaveBeenCalled();
});

it("freezes the estimate when a control mutation outcome is uncertain", async () => {
  const f = await openTimedSession({ controlCode: Code.Unavailable });
  await tick(5);
  await expectTiming("In progress · 5s");
  fireEvent.click(screen.getByRole("button", { name: "Stop" }));
  await waitFor(() => expect(f.control).toHaveBeenCalledTimes(1));
  await expectTiming("Unconfirmed · 5s");
  await tick(30);
  expect(timing()).toBe("Unconfirmed · 5s");
  // An uncertain control cannot be sent again as a new execution request.
  expect(screen.getByRole("button", { name: "Stop" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(f.control).toHaveBeenCalledTimes(1);
  expect(f.enqueue).not.toHaveBeenCalled();
});

it.each([
  [
    "recovery is required",
    () => sessionResource(2n, 8, { recovery: "required" }),
  ],
  [
    "execution responses are unconfirmed",
    () => sessionResource(2n, 8, {}, { unconfirmed_responses: 1 }),
  ],
])(
  "freezes the estimate as Unconfirmed when %s, then resumes from a newer clear revision",
  async (_name, unconfirmed) => {
    const f = await openTimedSession();
    await tick(2);
    await expectTiming("In progress · 2s");
    await act(async () => {
      f.publish(unconfirmed());
    });
    // The last displayed value stays frozen; the newer observation is not applied.
    await expectTiming("Unconfirmed · 2s");
    await tick(30);
    expect(timing()).toBe("Unconfirmed · 2s");
    expect(f.enqueue).not.toHaveBeenCalled();
    await act(async () => {
      f.publish(sessionResource(3n, 40));
    });
    await expectTiming("In progress · 40s");
    await tick(2);
    expect(timing()).toBe("In progress · 42s");
    expect(readDocument(sessionResource(3n, 40)).recovery).toBe("none");
  },
);
