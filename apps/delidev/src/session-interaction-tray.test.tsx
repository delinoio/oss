// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  EventAction,
  InboxService,
  InteractionService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  WatchEventsResponseSchema,
  newRequestId,
  type WatchEventsResponse,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { document, encode } from "./documents";
import { i18n } from "./localization";

it("retires the latest question through RPC and live closure while keeping composer identity, draft and focus", async () => {
  const sessionId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Question tray fixture",
      workspace: "general-chat",
      outcome: "succeeded",
      archive: "active",
      dispatch: "paused",
      recovery: "clear",
    }),
  });
  const data = {
    type: "user-question",
    closure: "open",
    questions: {
      questions: [
        {
          id: "original",
          text: "Keep exact answer",
          header: "Original question",
          secret: false,
          other: true,
          options: [],
        },
      ],
    },
  };
  const original = create(ResourceSchema, {
    id: newRequestId(),
    sessionId,
    kind: EntityKind.INTERACTION,
    schemaVersion: 1,
    revision: 9n,
    documentJson: encode(data),
  });
  let current = original;
  const respond = vi.fn(async () => ({
    interaction: create(ResourceSchema, {
      ...original,
      revision: 10n,
      documentJson: encode({ ...data, response: { state: "queued" } }),
    }),
  }));
  const channels = new Set<{
    events: WatchEventsResponse[];
    notify: () => void;
  }>();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(InteractionService, { respondQuestion: respond });
    router.service(ResourceService, {
      getSnapshot: () => ({
        resources: [session],
        cursor: "question-tray-snapshot",
      }),
      getResource: () => ({ resource: current }),
      // Keep a stale list response to prove that RPC/live observations outrank it.
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.INTERACTION ? [original] : [],
      }),
      async *watchEvents(_request, context) {
        const channel = {
          events: [] as WatchEventsResponse[],
          notify: () => {},
        };
        channels.add(channel);
        const abort = () => channel.notify();
        context.signal.addEventListener("abort", abort);
        try {
          while (!context.signal.aborted) {
            if (!channel.events.length)
              await new Promise<void>((done) => {
                channel.notify = done;
              });
            while (channel.events.length && !context.signal.aborted)
              yield channel.events.shift()!;
          }
        } finally {
          channels.delete(channel);
          context.signal.removeEventListener("abort", abort);
        }
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, { revision: 1n }),
      }),
    });
  });
  render(<App transport={transport} />);
  fireEvent.click(
    await within(
      screen.getByRole("complementary", { name: "Application sidebar" }),
    ).findByRole("button", { name: /General Chat Question tray fixture/ }),
  );
  const region = screen.getByRole("region", { name: "Current session" });
  const composer = await within(region).findByRole("textbox", {
    name: "Message",
  });
  fireEvent.change(composer, { target: { value: "Retained follow-up draft" } });
  const answer = await within(region).findByRole("textbox", {
    name: "Your answer",
  });
  fireEvent.change(answer, { target: { value: "Original response bytes" } });
  const send = within(region).getByRole("button", { name: "Send answers" });
  send.focus();
  fireEvent.click(send);
  await within(region).findByText("Response: queued");
  expect(
    within(region).queryByRole("textbox", { name: "Your answer" }),
  ).toBeNull();
  expect(within(region).getByRole("textbox", { name: "Message" })).toBe(
    composer,
  );
  expect(composer).toHaveProperty("value", "Retained follow-up draft");
  expect(window.document.activeElement).toBe(composer);
  expect(respond).toHaveBeenCalledTimes(1);
  current = create(ResourceSchema, {
    ...original,
    revision: 11n,
    documentJson: encode({
      ...document(original),
      closure: "native-closed",
      response: {
        state: "accepted",
        claim: { id: newRequestId() },
        delivery: { state: "transmitted", sequence: 10 },
        acceptance: { evidence: "native-question-output", sequence: 11 },
      },
    }),
  });
  await waitFor(() => expect(channels.size).toBeGreaterThan(0));
  await act(async () => {
    for (const channel of channels) {
      channel.events.push(
        create(WatchEventsResponseSchema, {
          id: newRequestId(),
          cursor: newRequestId(),
          entityId: original.id,
          kind: EntityKind.INTERACTION,
          sessionId,
          revision: 11n,
          action: EventAction.UPDATED,
        }),
      );
      channel.notify();
    }
  });
  await waitFor(() => expect(region.querySelector(".requests")).toBeNull());
  expect(composer).toHaveProperty("value", "Retained follow-up draft");
  expect(within(region).getByRole("textbox", { name: "Message" })).toBe(
    composer,
  );
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(region.querySelector(".requests")).toBeNull();
  expect(composer).toHaveProperty("value", "Retained follow-up draft");
  await act(async () => {
    await i18n.changeLanguage("en");
  });
  expect(respond).toHaveBeenCalledTimes(1);
});
