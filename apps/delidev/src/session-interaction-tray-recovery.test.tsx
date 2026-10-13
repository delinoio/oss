// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { expect, it } from "vitest";
import {
  EntityKind,
  ErrorDetailSchema,
  InboxService,
  InteractionService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
  type WatchEventsResponse,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

// Each interaction list read consults the current behavior, so a test can fail,
// stall or settle the next read while the live stream stays connected.
enum ReadBehavior {
  Empty = "empty",
  Fail = "fail",
  ContinuationAppeared = "continuation-appeared",
  Hold = "hold",
}

type Channel = {
  events: (WatchEventsResponse | "reconnect")[];
  notify: () => void;
};

async function openEmptyTray(label: string) {
  const sessionId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: label,
      workspace: "general-chat",
      outcome: "succeeded",
      archive: "active",
      dispatch: "paused",
      recovery: "clear",
    }),
  });
  const control = {
    behavior: ReadBehavior.Empty,
    // Behavior applied after a held read is released.
    settled: ReadBehavior.Empty,
    reads: 0,
    started: 0,
    release: () => {},
  };
  const channels = new Set<Channel>();
  let snapshots = 0;
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(InteractionService, {});
    router.service(ResourceService, {
      getSnapshot: () => {
        snapshots += 1;
        return { resources: [session], cursor: `recovery-${snapshots}` };
      },
      getResource: () => ({ resource: session }),
      listResources: async (request) => {
        if (request.filter?.kind !== EntityKind.INTERACTION)
          return { resources: [] };
        control.started += 1;
        let behavior = control.behavior;
        if (behavior === ReadBehavior.Hold) {
          await new Promise<void>((done) => {
            control.release = done;
          });
          behavior = control.settled;
        }
        control.reads += 1;
        if (behavior === ReadBehavior.Fail)
          throw new ConnectError("Interactions unavailable", Code.Unavailable);
        return {
          resources: [],
          nextPageToken:
            behavior === ReadBehavior.ContinuationAppeared ? "more" : "",
        };
      },
      async *watchEvents(_request, context) {
        const channel: Channel = { events: [], notify: () => {} };
        channels.add(channel);
        const abort = () => channel.notify();
        context.signal.addEventListener("abort", abort);
        try {
          while (!context.signal.aborted) {
            if (!channel.events.length)
              await new Promise<void>((done) => {
                channel.notify = done;
              });
            while (channel.events.length && !context.signal.aborted) {
              const next = channel.events.shift()!;
              if (next === "reconnect")
                throw new ConnectError(
                  "Cursor expired.",
                  Code.OutOfRange,
                  undefined,
                  [
                    {
                      desc: ErrorDetailSchema,
                      value: create(ErrorDetailSchema, {
                        code: "cursor_expired",
                      }),
                    },
                  ],
                );
              yield next;
            }
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
    ).findByRole("button", { name: new RegExp(`General Chat ${label}`) }),
  );
  const region = screen.getByRole("region", { name: "Current session" });
  await within(region).findByRole("textbox", { name: "Message" });
  await waitFor(() => expect(control.reads).toBeGreaterThan(0));
  await act(async () => {
    await new Promise((done) => setTimeout(done, 50));
  });
  // A healthy empty list renders no disclosure.
  expect(region.querySelector(".requests")).toBeNull();
  // Force the live reconnect that triggers the automatic Refresh of the list.
  const refresh = async (
    behavior: ReadBehavior,
    settled = ReadBehavior.Empty,
  ) => {
    control.behavior = behavior;
    control.settled = settled;
    const before = control.started;
    await act(async () => {
      for (const channel of channels) {
        channel.events.push("reconnect");
        channel.notify();
      }
    });
    await waitFor(() => expect(control.started).toBeGreaterThan(before), {
      timeout: 8000,
    });
  };
  return { region, control, refresh };
}

const flush = () =>
  act(async () => {
    await new Promise((done) => setTimeout(done, 100));
  });

async function openFailedTray(label: string, failure: ReadBehavior) {
  const tray = await openEmptyTray(label);
  await tray.refresh(failure);
  await waitFor(
    () => expect(tray.region.querySelector(".requests")).not.toBeNull(),
    { timeout: 8000 },
  );
  const disclosure = tray.region.querySelector<HTMLElement>(".requests")!;
  fireEvent.click(disclosure.querySelector("summary")!);
  return { ...tray, disclosure };
}

it("keeps the requests disclosure and loading status mounted while an explicit Retry over a failed empty list is pending", async () => {
  const { region, control, disclosure } = await openFailedTray(
    "Retry empty tray",
    ReadBehavior.Fail,
  );
  const retry = await within(disclosure).findByRole("button", {
    name: "Retry",
  });
  control.behavior = ReadBehavior.Hold;
  control.settled = ReadBehavior.Empty;
  const before = control.started;
  fireEvent.click(retry);
  await waitFor(() => expect(control.started).toBeGreaterThan(before));
  // The read is held: the surface and its status stay mounted for as long as it
  // is pending, not only for the first render after the click.
  for (let step = 0; step < 3; step += 1) {
    await flush();
    expect(region.querySelector(".requests")).toBe(disclosure);
    expect(within(disclosure).getByText(/^Loading .*…$/)).not.toBeNull();
  }
  await act(async () => control.release());
  // Empty success after recovery hides the disclosure again.
  await waitFor(() => expect(region.querySelector(".requests")).toBeNull());
}, 30_000);

it("keeps recovery controls reachable when an explicit Retry over a failed empty list fails again", async () => {
  const { region, control, disclosure } = await openFailedTray(
    "Retry failure tray",
    ReadBehavior.Fail,
  );
  control.behavior = ReadBehavior.Hold;
  control.settled = ReadBehavior.Fail;
  const before = control.started;
  fireEvent.click(
    await within(disclosure).findByRole("button", { name: "Retry" }),
  );
  await waitFor(() => expect(control.started).toBeGreaterThan(before));
  await flush();
  expect(region.querySelector(".requests")).toBe(disclosure);
  await act(async () => control.release());
  await waitFor(() =>
    expect(
      within(region.querySelector<HTMLElement>(".requests")!).getByText(
        /Could not read .*Previous data is shown\./,
      ),
    ).not.toBeNull(),
  );
  const retry = within(
    region.querySelector<HTMLElement>(".requests")!,
  ).getByRole("button", { name: "Retry" });
  expect(retry).toHaveProperty("disabled", false);
}, 30_000);

it("keeps the surface through a boundary Reload from a failed empty list until it settles", async () => {
  const { region, control, disclosure } = await openFailedTray(
    "Reload empty tray",
    ReadBehavior.ContinuationAppeared,
  );
  // A changed list boundary requires an explicit Reload instead of Retry.
  expect(
    within(disclosure).queryByRole("button", { name: "Retry" }),
  ).toBeNull();
  const reload = await within(disclosure).findByRole("button", {
    name: "Reload list",
  });
  control.behavior = ReadBehavior.Hold;
  control.settled = ReadBehavior.Empty;
  const before = control.started;
  fireEvent.click(reload);
  await waitFor(() => expect(control.started).toBeGreaterThan(before));
  for (let step = 0; step < 3; step += 1) {
    await flush();
    expect(region.querySelector(".requests")).toBe(disclosure);
    expect(within(disclosure).getByText(/^Loading .*…$/)).not.toBeNull();
  }
  await act(async () => control.release());
  await waitFor(() => expect(region.querySelector(".requests")).toBeNull());
}, 30_000);

it("keeps a Reload failure reachable when the reread settles with another failure", async () => {
  const { region, control, disclosure } = await openFailedTray(
    "Reload failure tray",
    ReadBehavior.ContinuationAppeared,
  );
  control.behavior = ReadBehavior.Hold;
  control.settled = ReadBehavior.Fail;
  const before = control.started;
  fireEvent.click(
    await within(disclosure).findByRole("button", { name: "Reload list" }),
  );
  await waitFor(() => expect(control.started).toBeGreaterThan(before));
  await flush();
  expect(region.querySelector(".requests")).toBe(disclosure);
  await act(async () => control.release());
  await waitFor(() =>
    expect(
      within(region.querySelector<HTMLElement>(".requests")!).getByRole(
        "button",
        { name: "Retry" },
      ),
    ).not.toBeNull(),
  );
}, 30_000);

it("keeps a healthy background Refresh over a confirmed empty list quiet", async () => {
  const { region, control, refresh } = await openEmptyTray("Quiet empty tray");
  await refresh(ReadBehavior.Hold);
  for (let step = 0; step < 3; step += 1) {
    await flush();
    expect(region.querySelector(".requests")).toBeNull();
  }
  await act(async () => control.release());
  await flush();
  expect(region.querySelector(".requests")).toBeNull();
}, 30_000);
