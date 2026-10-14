// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import {
  plainOpenCodeForkMessage,
  useOpenCodeForkProfile,
  useOpenCodeForkReasoningSupport,
} from "./opencode-fork-profile";
import { SessionForkAction, SessionForkProvider } from "./session-fork";
import "./localization";

const thread = "ses_01960dcbe1faABCDEFGHIJKLMN";
const turn = "msg_01960dcbe1fcABCDEFGHIJKLMN";
const assistant = "msg_01960dcbe1fdABCDEFGHIJKLMN";

function message(sessionId: string, document: Record<string, unknown>) {
  return create(ResourceSchema, {
    kind: EntityKind.MESSAGE,
    id: newRequestId(),
    sessionId,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      execution_id: newRequestId(),
      native_thread_id: thread,
      native_turn_id: turn,
      state: "complete",
      text: "",
      first_sequence: 5,
      last_sequence: 6,
      ...document,
    }),
  });
}

const snapshot = (extra: Record<string, unknown> = {}) => ({
  kind: "reasoning-text",
  text: "Private reasoning.",
  summary: [],
  content: [],
  ...extra,
});

function reasoning(
  sessionId: string,
  edit: (value: Record<string, unknown>) => void = () => {},
) {
  const document: Record<string, unknown> = {
    native_id: "prt_01960dcbe1f9ABCDEFGHIJKLMN",
    native_parent_id: assistant,
    role: "artifact",
    artifact: { started: snapshot(), completed: snapshot() },
  };
  edit(document);
  return message(sessionId, document);
}

function plainMessages(sessionId: string) {
  return [
    message(sessionId, {
      native_id: "prt_01960dcbe1faABCDEFGHIJKLMN",
      native_parent_id: turn,
      role: "user",
      input_id: newRequestId(),
      text: "Original plain text",
      first_sequence: 1,
      last_sequence: 1,
    }),
    message(sessionId, {
      native_id: "prt_01960dcbe1fbABCDEFGHIJKLMN",
      native_parent_id: assistant,
      role: "assistant",
      text: "Original plain text",
      first_sequence: 2,
      last_sequence: 2,
    }),
  ];
}

it("classifies a reasoning artifact only when the Worker reports the new capability", () => {
  const sessionId = newRequestId();
  const record = reasoning(sessionId);
  expect(plainOpenCodeForkMessage(record, sessionId, thread)).toBeUndefined();
  expect(plainOpenCodeForkMessage(record, sessionId, thread, false)).toBe(
    undefined,
  );
  expect(plainOpenCodeForkMessage(record, sessionId, thread, true)).toBe(
    "reasoning",
  );
});

it.each([
  [
    "revision",
    (value: Record<string, unknown>) =>
      (value.artifact = {
        started: snapshot({ kind: "opencode-revision" }),
        completed: snapshot({ kind: "opencode-revision" }),
      }),
  ],
  [
    "incomplete",
    (value: Record<string, unknown>) =>
      (value.artifact = { started: snapshot() }),
  ],
  [
    "summary",
    (value: Record<string, unknown>) =>
      (value.artifact = {
        started: snapshot(),
        completed: snapshot({ summary: ["x"] }),
      }),
  ],
  [
    "image",
    (value: Record<string, unknown>) =>
      (value.artifact = {
        started: snapshot(),
        completed: snapshot({ image_generation: {} }),
      }),
  ],
  [
    "turn parent",
    (value: Record<string, unknown>) => (value.native_parent_id = turn),
  ],
  [
    "input",
    (value: Record<string, unknown>) => (value.input_id = newRequestId()),
  ],
  ["tool", (value: Record<string, unknown>) => (value.tool = {})],
  [
    "streaming",
    (value: Record<string, unknown>) => (value.state = "streaming"),
  ],
  ["foreign field", (value: Record<string, unknown>) => (value.claude = {})],
])("keeps rejecting a non-reasoning artifact record: %s", (_name, edit) => {
  const sessionId = newRequestId();
  expect(
    plainOpenCodeForkMessage(
      reasoning(sessionId, edit),
      sessionId,
      thread,
      true,
    ),
  ).toBeUndefined();
});

it.each(["both", "server", "worker"])(
  "offers Fork for reasoning history only with both new capabilities: %s",
  async (present) => {
    const machineId = newRequestId();
    const source: Resource = create(ResourceSchema, {
      kind: EntityKind.SESSION,
      id: newRequestId(),
      revision: 8n,
      schemaVersion: 1,
      documentJson: encode({
        name: "OpenCode reasoning",
        machine_id: machineId,
        workspace: "general-chat",
        archive: "active",
        recovery: "none",
        outcome: "succeeded",
        initial_execution: { configuration: { harness: "opencode" } },
        execution: {
          native_thread_id: thread,
          native_turn_id: turn,
          cleanup_verified: true,
          observed: { opencode_agent: "build" },
        },
      }),
    });
    const machine = create(ResourceSchema, {
      kind: EntityKind.MACHINE,
      id: machineId,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        os: "linux",
        worker_capabilities: [
          "opencode-general-chat-fork-v1",
          ...(present === "server"
            ? []
            : ["opencode-general-chat-fork-reasoning-v1"]),
        ],
      }),
    });
    const messages = [...plainMessages(source.id), reasoning(source.id)];
    const list = vi.fn(() => ({ resources: messages }));
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [
            SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1,
            ...(present === "worker"
              ? []
              : [SystemCapability.OPENCODE_GENERAL_CHAT_FORK_REASONING_V1]),
          ],
        }),
      });
      router.service(SessionService, { forkSession: vi.fn() });
      router.service(ResourceService, {
        getResource: (request) => ({
          resource: request.kind === EntityKind.MACHINE ? machine : source,
        }),
        listResources: list,
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <SessionForkProvider openSession={vi.fn()}>
              <SessionForkAction source={source} />
            </SessionForkProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>,
    );
    if (present === "both") {
      await screen.findByRole("button", { name: "Fork session" });
    } else {
      await waitFor(() => expect(list).toHaveBeenCalled());
      await waitFor(() => expect(client.isFetching()).toBe(0));
      expect(screen.queryByRole("button", { name: "Fork session" })).toBeNull();
    }
  },
);

it("counts a completed reasoning record as eligible content like the server", async () => {
  const sessionId = newRequestId();
  const source: Resource = create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id: sessionId,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      execution: { native_thread_id: thread },
    }),
  });
  const user = plainMessages(sessionId)[0];
  const only = (messages: Resource[]) =>
    createRouterTransport((router) => {
      router.service(ResourceService, {
        getResource: () => ({ resource: source }),
        listResources: () => ({ resources: messages }),
      });
    });
  const run = async (messages: Resource[]) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () => useOpenCodeForkProfile(source, true, true),
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <TransportProvider transport={only(messages)}>
            <QueryClientProvider client={client}>
              {children}
            </QueryClientProvider>
          </TransportProvider>
        ),
      },
    );
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    return result.current.data;
  };
  // A user text record plus one reasoning record is two eligible records.
  expect(await run([user, reasoning(sessionId)])).toBe(true);
  // A lone user text record stays below the two-record floor.
  expect(await run([user])).toBe(false);
  // A duplicated reasoning native identity is still rejected.
  expect(await run([user, reasoning(sessionId), reasoning(sessionId)])).toBe(
    false,
  );
});

it.each([
  ["both pairs", true, true, true],
  ["reasoning pair without the base Worker capability", false, true, false],
  ["reasoning pair without the base system capability", true, false, false],
])(
  "requires the base capability together with reasoning: %s",
  async (_name, baseWorker, baseSystem, expected) => {
    const machineId = newRequestId();
    const source: Resource = create(ResourceSchema, {
      kind: EntityKind.SESSION,
      id: newRequestId(),
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({ machine_id: machineId }),
    });
    const machine = create(ResourceSchema, {
      kind: EntityKind.MACHINE,
      id: machineId,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        worker_capabilities: [
          ...(baseWorker ? ["opencode-general-chat-fork-v1"] : []),
          "opencode-general-chat-fork-reasoning-v1",
        ],
      }),
    });
    const transport = createRouterTransport((router) => {
      router.service(SystemService, {
        getStatus: () => ({
          capabilities: [
            ...(baseSystem ? [SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1] : []),
            SystemCapability.OPENCODE_GENERAL_CHAT_FORK_REASONING_V1,
          ],
        }),
      });
      router.service(ResourceService, {
        getResource: () => ({ resource: machine }),
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () => useOpenCodeForkReasoningSupport(source, true),
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <TransportProvider transport={transport}>
            <QueryClientProvider client={client}>{children}</QueryClientProvider>
          </TransportProvider>
        ),
      },
    );
    await waitFor(() => expect(client.isFetching()).toBe(0));
    expect(client.getQueryCache().getAll()).toHaveLength(2);
    expect(result.current).toBe(expected);
  },
);
