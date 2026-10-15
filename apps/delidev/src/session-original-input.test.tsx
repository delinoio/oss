// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it } from "vitest";
import {
  EntityKind,
  EventAction,
  InboxService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  WatchEventsResponseSchema,
  newRequestId,
  type Resource,
  type WatchEventsResponse,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

const FIRST_PROMPT = "Original first prompt text";
const LATER_PROMPT = "Later queued follow-up text";

function queueRow(
  sessionId: string,
  id: string,
  sequence: number,
  prompt: string,
  revision = 1n,
  delivery = "queued",
) {
  return create(ResourceSchema, {
    id,
    sessionId,
    kind: EntityKind.QUEUE,
    schemaVersion: 1,
    revision,
    documentJson: encode({
      prompt,
      mode: "execute",
      delivery,
      sequence,
    }),
  });
}

function fixture(firstDelivery = "queued") {
  const sessionId = newRequestId(),
    firstId = newRequestId(),
    laterId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Original input session",
      workspace: "general-chat",
      archive: "active",
      outcome: "stopped",
      dispatch: "blocked",
      recovery: "required",
      initial_execution: { input_id: firstId },
    }),
  });
  const resources = new Map<string, Resource>([[sessionId, session]]);
  let queue = [
    queueRow(sessionId, firstId, 1, FIRST_PROMPT, 1n, firstDelivery),
    queueRow(sessionId, laterId, 2, LATER_PROMPT),
  ];
  const channels = new Set<{
    events: WatchEventsResponse[];
    notify: () => void;
  }>();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: queue }),
    });
    router.service(ResourceService, {
      getResource: (request) => ({
        resource:
          resources.get(request.id) ??
          queue.find((row) => row.id === request.id),
      }),
      getSnapshot: () => ({ resources: [session], cursor: "original-input" }),
      listResources: () => ({ resources: [] as Resource[] }),
      async *watchEvents(_request, context) {
        const channel = {
          events: [] as WatchEventsResponse[],
          notify: () => {},
        };
        channels.add(channel);
        const aborted = () => channel.notify();
        context.signal.addEventListener("abort", aborted);
        try {
          while (!context.signal.aborted) {
            if (!channel.events.length)
              await new Promise<void>((resolve) => {
                channel.notify = resolve;
                if (context.signal.aborted) resolve();
              });
            while (channel.events.length && !context.signal.aborted)
              yield channel.events.shift()!;
          }
        } finally {
          channels.delete(channel);
        }
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, {
          revision: 1n,
          interactions: true,
        }),
      }),
    });
  });
  const publish = (resource: Resource) => {
    resources.set(resource.id, resource);
    for (const channel of channels) {
      channel.events.push(
        create(WatchEventsResponseSchema, {
          id: newRequestId(),
          cursor: newRequestId(),
          entityId: resource.id,
          kind: resource.kind,
          sessionId: resource.sessionId,
          revision: resource.revision,
          action: EventAction.UPDATED,
        }),
      );
      channel.notify();
    }
  };
  return {
    sessionId,
    firstId,
    transport,
    publish,
    setQueue: (rows: Resource[]) => {
      queue = rows;
    },
  };
}

it("reconstructs the original input as one unlabelled sent bubble and keeps later queued inputs", async () => {
  const f = fixture();
  render(<App transport={f.transport} />);
  fireEvent.click(
    await screen.findByRole("button", {
      name: /General Chat Original input session/,
    }),
  );
  const bubble = await screen.findByRole("article", {
    name: "Submitted user message",
  });
  expect(bubble.textContent).toContain(FIRST_PROMPT);
  // No delivery label or badge on the original sent message.
  expect(bubble.querySelector("header, small, [role=status]")).toBeNull();
  // Exactly one presentation of the original input; the later input keeps its row.
  expect(screen.getAllByText(FIRST_PROMPT)).toHaveLength(1);
  await waitFor(() =>
    expect(screen.getAllByText(LATER_PROMPT).length).toBeGreaterThan(0),
  );

  await act(async () =>
    f.publish(
      create(ResourceSchema, {
        id: newRequestId(),
        sessionId: f.sessionId,
        kind: EntityKind.MESSAGE,
        schemaVersion: 1,
        revision: 1n,
        documentJson: encode({
          role: "user",
          state: "complete",
          input_id: f.firstId,
          text: FIRST_PROMPT,
        }),
      }),
    ),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("article", { name: "Submitted user message" }),
    ).toBeNull(),
  );
  expect(screen.getAllByText(FIRST_PROMPT)).toHaveLength(1);
});

it.each(["uncertain", "rejected-before-start"])(
  "reconstructs a %s original input from the verified sequence-one row",
  async (delivery) => {
    const f = fixture(delivery);
    render(<App transport={f.transport} />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: /General Chat Original input session/,
      }),
    );
    const bubble = await screen.findByRole("article", {
      name: "Submitted user message",
    });
    expect(bubble.textContent).toContain(FIRST_PROMPT);
    expect(screen.getAllByText(FIRST_PROMPT)).toHaveLength(1);
  },
);
