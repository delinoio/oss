// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

it("places an original choice only in an empty unsent live composer", async () => {
  const id = newRequestId();
  const session = create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Original suggestions",
      workspace: "general-chat",
      archive: "active",
      outcome: "stopped",
      dispatch: "paused",
      recovery: "none",
    }),
  });
  const message = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: id,
    kind: EntityKind.MESSAGE,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      role: "assistant",
      state: "complete",
      text: "Original text",
      native_id: "original-message",
      codex_assistant: {
        started: { delivery: null, questions: null },
        completed: {
          delivery: "async",
          questions: [
            { title: "Original question", options: ["Exact B", "Exact A"] },
          ],
        },
      },
    }),
  });
  const enqueue = vi.fn(),
    control = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.CODEX_ASYNC_MESSAGES_V1],
      }),
    });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
      enqueueInput: enqueue,
      controlSession: control,
    });
    router.service(ResourceService, {
      getSnapshot: () => ({
        resources: [session],
        cursor: "original-snapshot",
      }),
      listResources: (request) => ({
        resources: request.filter?.kind === EntityKind.MESSAGE ? [message] : [],
      }),
      async *watchEvents(_request, context) {
        if (!context.signal.aborted)
          await new Promise<void>((done) =>
            context.signal.addEventListener("abort", () => done(), {
              once: true,
            }),
          );
      },
    });
    router.service(InboxService, {
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, {
          revision: 1n,
          interactions: true,
        }),
      }),
      listInbox: () => ({ entries: [] }),
    });
  });
  render(<App transport={transport} />);
  const sidebar = within(
    screen.getByRole("complementary", { name: "Application sidebar" }),
  );
  fireEvent.click(
    await sidebar.findByRole("button", {
      name: /General Chat Original suggestions/,
    }),
  );
  const current = within(
    screen.getByRole("region", { name: "Current session" }),
  );
  const composer = await current.findByRole("textbox", { name: "Message" });
  const option = await current.findByRole("button", { name: "Exact B" });
  await waitFor(() =>
    expect((option as HTMLButtonElement).disabled).toBe(false),
  );
  expect(current.getByText("Asynchronous message")).toBeTruthy();
  fireEvent.click(option);
  expect(composer).toHaveProperty("value", "Exact B");
  expect(document.activeElement).toBe(composer);
  expect(enqueue).not.toHaveBeenCalled();
  expect(control).not.toHaveBeenCalled();
  fireEvent.change(composer, { target: { value: "Keep my draft" } });
  expect((option as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(option);
  expect(composer).toHaveProperty("value", "Keep my draft");
  expect(
    current.getByText("Clear your draft to use a suggestion."),
  ).toBeTruthy();
  expect(enqueue).not.toHaveBeenCalled();
  expect(control).not.toHaveBeenCalled();
});
