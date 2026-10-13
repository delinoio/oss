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
        id: newRequestId(),
        state: "accepted",
        input: { answers: { original: ["A"] } },
        accepted_at: "2026-01-02T03:04:05Z",
        claim: {
          id: newRequestId(),
          job_id: newRequestId(),
          machine_id: newRequestId(),
          instance_id: newRequestId(),
          device_id: newRequestId(),
          claimed_at: "2026-01-02T03:04:06Z",
        },
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

it("keeps the compact question state and count after a successful answer when the payload page no longer lists the request", async () => {
  const sessionId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Evicted question fixture",
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
  let listed = [original];
  const respond = vi.fn(async () => ({
    interaction: create(ResourceSchema, {
      ...original,
      revision: 10n,
      documentJson: encode({ ...data, response: { state: "queued" } }),
    }),
  }));
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
        cursor: "evicted-question-snapshot",
      }),
      getResource: () => ({ resource: original }),
      // The authoritative list no longer includes the request after the answer.
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.INTERACTION ? listed : [],
      }),
      async *watchEvents(_request, context) {
        await new Promise<void>((done) =>
          context.signal.addEventListener("abort", () => done()),
        );
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
    ).findByRole("button", { name: /General Chat Evicted question fixture/ }),
  );
  const region = screen.getByRole("region", { name: "Current session" });
  const answer = await within(region).findByRole("textbox", {
    name: "Your answer",
  });
  fireEvent.change(answer, { target: { value: "Original response bytes" } });
  listed = [];
  fireEvent.click(within(region).getByRole("button", { name: "Send answers" }));
  await within(region).findByText("Response: queued");
  await waitFor(() => expect(respond).toHaveBeenCalledTimes(1));
  // Allow any post-success refresh to settle against the empty list.
  await act(async () => {
    await new Promise((done) => setTimeout(done, 50));
  });
  expect(region.querySelector(".requests")).not.toBeNull();
  expect(within(region).getByText("Response: queued")).not.toBeNull();
  expect(
    within(region).queryByRole("textbox", { name: "Your answer" }),
  ).toBeNull();
  expect(region.querySelector(".requests summary")?.textContent).toContain("1");
  expect(respond).toHaveBeenCalledTimes(1);
});

it("keeps an evicted older question receipt before newer page requests", async () => {
  const sessionId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Receipt order fixture",
      workspace: "general-chat",
      outcome: "succeeded",
      archive: "active",
      dispatch: "paused",
      recovery: "clear",
    }),
  });
  const question = (header: string) => ({
    type: "user-question",
    closure: "open",
    questions: {
      questions: [
        {
          id: "original",
          text: header,
          header,
          secret: false,
          other: true,
          options: [],
        },
      ],
    },
  });
  // One request per page keeps the fixture small while reaching four pages.
  const pages = ["First", "Second", "Third", "Fourth"].map((header) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.INTERACTION,
      schemaVersion: 1,
      revision: 9n,
      documentJson: encode(question(`${header} question`)),
    }),
  );
  const respond = vi.fn(async () => ({
    interaction: create(ResourceSchema, {
      ...pages[0],
      revision: 10n,
      documentJson: encode({
        ...question("First question"),
        response: { state: "queued" },
      }),
    }),
  }));
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
        cursor: "receipt-order-snapshot",
      }),
      getResource: () => ({ resource: pages[0] }),
      listResources: (request) => {
        if (request.filter?.kind !== EntityKind.INTERACTION)
          return { resources: [] };
        const index = request.filter.pageToken
          ? Number(request.filter.pageToken)
          : 0;
        return {
          resources: [pages[index]],
          nextPageToken: index < pages.length - 1 ? String(index + 1) : "",
        };
      },
      async *watchEvents(_request, context) {
        await new Promise<void>((done) =>
          context.signal.addEventListener("abort", () => done()),
        );
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
    ).findByRole("button", { name: /General Chat Receipt order fixture/ }),
  );
  const region = screen.getByRole("region", { name: "Current session" });
  const answer = await within(region).findByRole("textbox", {
    name: "Your answer",
  });
  fireEvent.change(answer, { target: { value: "Original response bytes" } });
  fireEvent.click(within(region).getByRole("button", { name: "Send answers" }));
  await within(region).findByText("Response: queued");
  const requests = region.querySelector<HTMLElement>(".requests")!;
  // Reach pages two through four; page one then leaves the three-page window.
  for (let page = 1; page < pages.length; page++) {
    fireEvent.click(
      await within(requests).findByRole("button", { name: /^Load more/ }),
    );
    await waitFor(() =>
      expect(requests.querySelectorAll("[data-payload-page]")).toHaveLength(
        page + 1,
      ),
    );
  }
  await waitFor(() =>
    expect(
      within(requests).getByRole("button", {
        name: "Restore previously loaded items",
      }),
    ).not.toBeNull(),
  );
  const order = () =>
    [...requests.querySelectorAll("[data-payload-page]")].map((node) =>
      node.textContent?.includes("Response: queued")
        ? "receipt"
        : (/(Second|Third|Fourth) question/.exec(node.textContent ?? "")?.[1] ??
          "restore"),
    );
  expect(order()).toEqual(["receipt", "Second", "Third", "Fourth"]);
  expect(requests.textContent?.match(/Response: queued/g)).toHaveLength(1);
  expect(respond).toHaveBeenCalledTimes(1);
});

enum RecoveredResult {
  Advancing = "advancing",
  Equal = "equal",
  OtherSession = "other-session",
  OtherInteraction = "other-interaction",
}

enum RetryTiming {
  /** The retry resolves while the compact receipt is still mounted. */
  Settled = "settled",
  /** Payload restoration replaces the receipt while the retry is in flight and
   * the projection has already advanced to the result's revision. */
  RestoredBeforeResult = "restored-before-result",
}

/** Sends the first of four paged questions, loses its acknowledgement, evicts
 * its payload page, retries through the compact receipt, then reopens the
 * request so any surviving edit draft becomes visible again. */
async function recoverThroughEvictedReceipt(
  returned: RecoveredResult,
  timing: RetryTiming = RetryTiming.Settled,
) {
  const sessionId = newRequestId();
  const session = create(ResourceSchema, {
    id: sessionId,
    sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Recovered draft fixture",
      workspace: "general-chat",
      outcome: "succeeded",
      archive: "active",
      dispatch: "paused",
      recovery: "clear",
    }),
  });
  const question = (header: string) => ({
    type: "user-question",
    closure: "open",
    questions: {
      questions: [
        {
          id: "original",
          text: header,
          header,
          secret: false,
          other: true,
          options: [],
        },
      ],
    },
  });
  const pages = ["First", "Second", "Third", "Fourth"].map((header) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.INTERACTION,
      schemaVersion: 1,
      revision: 9n,
      documentJson: encode(question(`${header} question`)),
    }),
  );
  let first = pages[0];
  const returnedResource = () => {
    // The replaced-receipt timing keeps the interaction open at the result
    // revision so the restored form, and any surviving draft, stays visible.
    const base = {
      ...pages[0],
      documentJson: encode(
        timing === RetryTiming.RestoredBeforeResult
          ? question("First question")
          : { ...question("First question"), response: { state: "queued" } },
      ),
    };
    switch (returned) {
      case RecoveredResult.Advancing:
        return create(ResourceSchema, { ...base, revision: 10n });
      case RecoveredResult.Equal:
        return create(ResourceSchema, { ...base, revision: 9n });
      case RecoveredResult.OtherSession:
        return create(ResourceSchema, {
          ...base,
          revision: 10n,
          sessionId: newRequestId(),
        });
      case RecoveredResult.OtherInteraction:
        return create(ResourceSchema, {
          ...base,
          revision: 10n,
          id: newRequestId(),
        });
    }
  };
  let releaseRetry: () => void = () => {};
  const retryGate = new Promise<void>((done) => {
    releaseRetry = done;
  });
  const respond = vi
    .fn()
    .mockRejectedValueOnce(
      new ConnectError("Lost acknowledgement", Code.Unavailable),
    )
    .mockImplementation(async () => {
      if (timing === RetryTiming.RestoredBeforeResult) await retryGate;
      return { interaction: returnedResource() };
    });
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
        cursor: "recovered-draft-snapshot",
      }),
      getResource: () => ({ resource: first }),
      listResources: (request) => {
        if (request.filter?.kind !== EntityKind.INTERACTION)
          return { resources: [] };
        const index = request.filter.pageToken
          ? Number(request.filter.pageToken)
          : 0;
        return {
          resources: [index === 0 ? first : pages[index]],
          nextPageToken: index < pages.length - 1 ? String(index + 1) : "",
        };
      },
      async *watchEvents(_request, context) {
        await new Promise<void>((done) =>
          context.signal.addEventListener("abort", () => done()),
        );
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
    ).findByRole("button", { name: /General Chat Recovered draft fixture/ }),
  );
  const region = screen.getByRole("region", { name: "Current session" });
  const answer = await within(region).findByRole("textbox", {
    name: "Your answer",
  });
  fireEvent.change(answer, { target: { value: "Original response bytes" } });
  fireEvent.click(within(region).getByRole("button", { name: "Send answers" }));
  await within(region).findByRole("button", { name: "Retry the same answers" });
  // Compacting the uncertain answer moves focus to its retry control. The
  // reader then scrolls away, so focus protection must not pin the first page.
  act(() => {
    (window.document.activeElement as HTMLElement | null)?.blur();
  });
  const requests = region.querySelector<HTMLElement>(".requests")!;
  for (let page = 1; page < pages.length; page++) {
    fireEvent.click(
      await within(requests).findByRole("button", { name: /^Load more/ }),
    );
    await waitFor(() =>
      expect(requests.querySelectorAll("[data-payload-page]")).toHaveLength(
        page + 1,
      ),
    );
    if (page === 1) {
      // An unrelated draft on the second request must survive the recovery.
      const rows = [...requests.querySelectorAll("[data-payload-page]")];
      const second = rows.find((node) =>
        node.textContent?.includes("Second question"),
      ) as HTMLElement;
      fireEvent.change(within(second).getByRole("textbox"), {
        target: { value: "Second draft" },
      });
    }
  }
  await waitFor(() =>
    expect(
      within(requests).getByRole("button", {
        name: "Restore previously loaded items",
      }),
    ).not.toBeNull(),
  );
  const retry = within(requests).getByRole("button", {
    name: "Retry the same answers",
  });
  fireEvent.click(retry);
  await waitFor(() => expect(respond).toHaveBeenCalledTimes(2));
  if (timing === RetryTiming.RestoredBeforeResult) {
    // The authoritative projection reaches the result revision, and restoring
    // the evicted payload pages replaces the compact receipt, before the
    // retried RPC completes.
    first = create(ResourceSchema, {
      ...pages[0],
      revision: 10n,
      documentJson: encode(question("First question")),
    });
    fireEvent.click(
      within(requests).getByRole("button", {
        name: "Restore previously loaded items",
      }),
    );
    await act(async () => {
      await new Promise((done) => setTimeout(done, 300));
    });
    releaseRetry();
  }
  await act(async () => {
    await new Promise((done) => setTimeout(done, 50));
  });
  expect(respond.mock.calls[1][0]).toEqual(respond.mock.calls[0][0]);
  expect(respond).toHaveBeenCalledTimes(2);
  // Reopen the request so a surviving draft is visible after payload restore.
  first = create(ResourceSchema, {
    ...pages[0],
    revision: 11n,
    documentJson: encode(question("First question")),
  });
  for (let attempt = 0; attempt < 5; attempt++) {
    const [restore] = within(requests).queryAllByRole("button", {
      name: "Restore previously loaded items",
    });
    if (!restore || requests.textContent?.includes("First question")) break;
    fireEvent.click(restore);
    await act(async () => {
      await new Promise((done) => setTimeout(done, 50));
    });
  }
  await waitFor(() => expect(requests.textContent).toContain("First question"));
  const values = () =>
    within(requests)
      .queryAllByRole("textbox", { name: "Your answer" })
      .map((node) => (node as HTMLTextAreaElement | HTMLInputElement).value);
  return { values, respond };
}

it("clears only the recovered request draft after a retry through an evicted receipt", async () => {
  const { values, respond } = await recoverThroughEvictedReceipt(
    RecoveredResult.Advancing,
  );
  expect(respond).toHaveBeenCalledTimes(2);
  expect(values()).not.toContain("Original response bytes");
  expect(values()).toContain("");
  expect(values()).toContain("Second draft");
}, 30_000);

it("clears the recovered draft when restoration replaced the receipt before the retry completed", async () => {
  const { values, respond } = await recoverThroughEvictedReceipt(
    RecoveredResult.Advancing,
    RetryTiming.RestoredBeforeResult,
  );
  expect(respond).toHaveBeenCalledTimes(2);
  expect(values()).not.toContain("Original response bytes");
  expect(values()).toContain("");
  expect(values()).toContain("Second draft");
}, 30_000);

it.each([
  RecoveredResult.Equal,
  RecoveredResult.OtherSession,
  RecoveredResult.OtherInteraction,
])(
  "keeps the draft when a replaced receipt's retry returns a %s result",
  async (returned) => {
    const { values } = await recoverThroughEvictedReceipt(
      returned,
      RetryTiming.RestoredBeforeResult,
    );
    expect(values()).toContain("Original response bytes");
  },
  30_000,
);

it.each([
  RecoveredResult.Equal,
  RecoveredResult.OtherSession,
  RecoveredResult.OtherInteraction,
])(
  "keeps the recovered draft when the retry returns a %s result",
  async (returned) => {
    const { values } = await recoverThroughEvictedReceipt(returned);
    expect(values()).toContain("Original response bytes");
  },
  30_000,
);
