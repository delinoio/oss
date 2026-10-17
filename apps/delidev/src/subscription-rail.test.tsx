// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import {
  TransportProvider,
  createConnectQueryKey,
} from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceQuery,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  freshWindow,
  remainingBadge,
  railAccount,
  type RailWindow,
} from "./subscription-rail-data";
import { SubscriptionRail } from "./subscription-rail";
import { subscriptionInventoryRequest } from "./subscription-inventory";
const now = Date.now();
const window = (
  remaining: number,
  extra: Partial<RailWindow> = {},
): RailWindow => ({
  id: "weekly",
  remaining,
  state: "observed",
  observedAt: new Date(now).toISOString(),
  resetAt: "",
  comparisonGroup: "weekly",
  blocking: true,
  ...extra,
});
function resource(name: string, service = "chatgpt", extra = {}): Resource {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 1n,
    schemaVersion: 2,
    documentJson: encode({
      type: "subscription",
      name,
      subscription_service: service,
      connection: { id: newRequestId() },
      quota: [
        {
          id: "weekly",
          remaining: 0.28,
          state: "observed",
          observed_at: new Date(now).toISOString(),
          comparison_group: "weekly",
          blocking: true,
        },
      ],
      ...extra,
    }),
  });
}
afterEach(() => vi.useRealTimers());
it("uses only complete fresh blocking evidence, preserving measured endpoints", () => {
  expect(
    remainingBadge(
      [window(0.28), window(0.64), window(0.01, { blocking: false })],
      now,
    ),
  ).toBe(28);
  expect(remainingBadge([window(0)], now)).toBe(0);
  expect(remainingBadge([window(1)], now)).toBe(100);
  for (const entry of [
    window(NaN),
    window(Infinity),
    window(-0.1),
    window(1.1),
    window(0.2, { remaining: undefined }),
    window(0.2, { comparisonGroup: "" }),
    window(0.2, { state: "failed" }),
    window(0.2, { state: "unsupported" }),
    window(0.2, { observedAt: new Date(now + 1).toISOString() }),
    window(0.2, { observedAt: new Date(now - 300001).toISOString() }),
    window(0.2, { resetAt: new Date(now).toISOString() }),
  ])
    expect(remainingBadge([window(0.8), entry], now)).toBeUndefined();
  expect(remainingBadge([], now)).toBeUndefined();
  expect(
    remainingBadge([window(0.2, { blocking: false })], now),
  ).toBeUndefined();
});
it("projects safe independent identities and excludes disconnected/removal/recovery accounts", () => {
  const a = resource("Personal"),
    b = resource("Personal");
  expect(railAccount(a).id).not.toBe(railAccount(b).id);
  for (const extra of [
    { connection: {} },
    { removal: {} },
    { subscription: { recovery_required: true } },
  ])
    expect(railAccount(resource("Hidden", "chatgpt", extra)).connected).toBe(
      false,
    );
  expect(Object.keys(railAccount(a)).sort()).toEqual([
    "alias",
    "connected",
    "connectionId",
    "disabled",
    "generation",
    "id",
    "paidCreditState",
    "paidCredits",
    "revision",
    "service",
    "windows",
  ]);
});
function mount(
  read: (
    token: string,
  ) =>
    | { resources: Resource[]; nextPageToken?: string }
    | Promise<{ resources: Resource[]; nextPageToken?: string }>,
  capable = true,
  paidCapable: boolean | (() => boolean) = false,
  stopping: boolean | (() => boolean) = false,
) {
  const requests = vi.fn(read),
    manage = vi.fn(),
    focusFallback = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        stopping: typeof stopping === "function" ? stopping() : stopping,
        capabilities: capable
          ? [
              SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
              ...((
                typeof paidCapable === "function" ? paidCapable() : paidCapable
              )
                ? [SystemCapability.SUBSCRIPTION_PAID_CREDITS_V1]
                : []),
            ]
          : [],
      }),
    });
    router.service(ResourceService, {
      listResources: (r) => {
        expect(r.accountType).toBe(2);
        expect(r.filter?.pageSize).toBe(50);
        return requests(r.filter!.pageToken);
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (enabled = true) => (
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <SubscriptionRail
          enabled={enabled}
          manage={manage}
          focusFallback={focusFallback}
        />
      </TransportProvider>
    </QueryClientProvider>
  );
  return { ...render(view()), view, requests, manage, focusFallback, client };
}
it("shows separate accounts, read-only selected details, manages and restores opener focus", async () => {
  const values = [
    resource("Personal"),
    resource("Work"),
    resource("Claude", "claude"),
    resource("Grok", "grok"),
    resource("Disconnected", "chatgpt", { connection: {} }),
  ];
  const f = mount(() => ({ resources: values }));
  const opener = await screen.findByRole("button", {
    name: /ChatGPT · Personal/,
  });
  expect(screen.getAllByRole("button", { name: /remaining/ })).toHaveLength(4);
  // The rail keeps provider catalog artwork; Settings harness marks never leak here.
  expect(
    screen
      .getAllByRole("button", { name: /remaining/ })
      .map((button) => button.querySelector("img")?.getAttribute("src")),
  ).toEqual([
    "./subscription-marks/openai.svg",
    "./subscription-marks/openai.svg",
    "./subscription-marks/claude.svg",
    "./subscription-marks/grok.svg",
  ]);
  expect(document.querySelector("[data-harness]")).toBeNull();
  fireEvent.click(opener);
  expect(
    screen.getByRole("dialog", { name: "ChatGPT · Personal" }),
  ).toBeTruthy();
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Manage subscriptions" }));
  expect(f.manage).toHaveBeenCalledOnce();
  expect(f.requests).toHaveBeenCalledOnce();
});
it("continues exact pages with UUID deduplication", async () => {
  const first = resource("Personal");
  const f = mount((token) =>
    token
      ? { resources: [first, resource("Second")], nextPageToken: "" }
      : { resources: [first], nextPageToken: "tail" },
  );
  await screen.findByRole("button", { name: /Personal/ });
  fireEvent.click(
    screen.getByRole("button", { name: /Load more Subscriptions/ }),
  );
  await screen.findByRole("button", { name: /Second/ });
  expect(screen.getAllByRole("button", { name: /Personal/ })).toHaveLength(1);
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual(["", "tail"]);
});
it("failure makes retained badges unavailable and exact explicit read retry recovers", async () => {
  const first = resource("Personal");
  let failed = false;
  const f = mount(() => {
    if (failed) throw new ConnectError("read failed", Code.Unavailable);
    return { resources: [first] };
  });
  await screen.findByRole("button", { name: /28% remaining/ });
  failed = true;
  fireEvent(windowThis(), new Event("focus"));
  await screen.findByRole("button", { name: /Personal · Quota unavailable/ });
  expect(f.requests).toHaveBeenCalledTimes(2);
  failed = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Explain subscription read problem" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await screen.findByRole("button", { name: /28% remaining/ });
});
function windowThis() {
  return globalThis.window;
}
it("hides unsupported inventory without reading accounts", async () => {
  const f = mount(() => ({ resources: [] }), false);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Explain subscription read problem",
    }),
  );
  await screen.findByText("Update the server to read subscription accounts.");
  expect(f.requests).not.toHaveBeenCalled();
});
it("fences a deferred read after disconnection", async () => {
  let resolve!: (value: { resources: Resource[] }) => void;
  const f = mount(() => new Promise((done) => (resolve = done)));
  await waitFor(() => expect(f.requests).toHaveBeenCalledOnce());
  f.rerender(f.view(false));
  await act(async () => resolve({ resources: [resource("Late")] }));
  expect(screen.queryByRole("button", { name: /Late/ })).toBeNull();
});
it("requires Reload for repeating cursors without publishing the page", async () => {
  const first = resource("Personal");
  const f = mount((token) =>
    token
      ? { resources: [resource("Should not publish")], nextPageToken: "tail" }
      : { resources: [first], nextPageToken: "tail" },
  );
  await screen.findByRole("button", { name: /Personal/ });
  fireEvent.click(
    screen.getByRole("button", { name: /Load more Subscriptions/ }),
  );
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(2));
  expect(
    screen.queryByRole("button", { name: /Should not publish/ }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
});
it("clears private account projections on authentication loss", async () => {
  const first = resource("Private");
  let lost = false;
  const f = mount(() => {
    if (lost) throw new ConnectError("auth", Code.Unauthenticated);
    return { resources: [first] };
  });
  await screen.findByRole("button", { name: /Private/ });
  lost = true;
  fireEvent(windowThis(), new Event("focus"));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Private/ })).toBeNull(),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Explain subscription read problem",
    }),
  );
  await screen.findByText(
    "Authentication expired. Reconnect to read subscriptions.",
  );
  expect(screen.queryByRole("button", { name: /Private/ })).toBeNull();
});
it("a successful empty inventory reserves no account region", async () => {
  const f = mount(() => ({ resources: [] }));
  await waitFor(() => expect(f.requests).toHaveBeenCalledOnce());
  await waitFor(() => expect(f.container.children).toHaveLength(0));
});
it("refreshes reached pages atomically without discovering an unseen tail", async () => {
  const first = resource("Personal"),
    second = resource("Work");
  let refreshing = false;
  let finish!: (value: {
    resources: Resource[];
    nextPageToken: string;
  }) => void;
  const f = mount((token) => {
    if (!refreshing)
      return token
        ? { resources: [second], nextPageToken: "unseen" }
        : { resources: [first], nextPageToken: "tail" };
    if (token) return new Promise((done) => (finish = done));
    return {
      resources: [
        {
          ...first,
          revision: 2n,
          documentJson: encode({
            type: "subscription",
            name: "Personal",
            subscription_service: "chatgpt",
            connection: { id: newRequestId() },
            quota: [
              {
                id: "weekly",
                remaining: 0.64,
                state: "observed",
                observed_at: new Date(now).toISOString(),
                comparison_group: "weekly",
                blocking: true,
              },
            ],
          }),
        },
      ],
      nextPageToken: "renewed",
    };
  });
  await screen.findByRole("button", { name: /Personal · 28%/ });
  fireEvent.click(
    screen.getByRole("button", { name: /Load more Subscriptions/ }),
  );
  await screen.findByRole("button", { name: /Work/ });
  refreshing = true;
  fireEvent(windowThis(), new Event("focus"));
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(4));
  expect(screen.getByRole("button", { name: /Personal · 28%/ })).toBeTruthy();
  await act(async () =>
    finish({ resources: [second], nextPageToken: "renewed-unseen" }),
  );
  await screen.findByRole("button", { name: /Personal · 64%/ });
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual([
    "",
    "tail",
    "",
    "tail",
  ]);
});
it("rereads accepted saved ranges every thirty seconds regardless of window focus or visibility", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  const focus = vi.spyOn(document, "hasFocus").mockReturnValue(true);
  const f = mount(() => ({ resources: [resource("Personal")] }));
  await screen.findByRole("button", { name: /Personal/ });
  expect(f.requests).toHaveBeenCalledOnce();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(29999);
  });
  expect(f.requests).toHaveBeenCalledOnce();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1);
  });
  expect(f.requests).toHaveBeenCalledTimes(2);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30000);
  });
  expect(f.requests).toHaveBeenCalledTimes(3);
  // Rereads reuse the accepted range: no unseen tail page is requested.
  expect(f.requests.mock.calls.every((call) => call[0] === "")).toBe(true);
  // Blur keeps the same cadence without resetting the running interval.
  focus.mockReturnValue(false);
  fireEvent(windowThis(), new Event("blur"));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(29999);
  });
  expect(f.requests).toHaveBeenCalledTimes(3);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1);
  });
  expect(f.requests).toHaveBeenCalledTimes(4);
  // A hidden (minimized) document keeps reading at the same cadence.
  const visibility = vi
    .spyOn(document, "visibilityState", "get")
    .mockReturnValue("hidden");
  fireEvent(document, new Event("visibilitychange"));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15 * 60 * 1000);
  });
  expect(f.requests).toHaveBeenCalledTimes(34);
  expect(f.requests.mock.calls.every((call) => call[0] === "")).toBe(true);
  visibility.mockRestore();
  focus.mockRestore();
});
it("rereads only accepted ranges when saved subscription inventory is invalidated", async () => {
  const personal = resource("Personal"),
    work = resource("Work");
  let values = [personal, work];
  const f = mount((token) => ({
    resources: values,
    nextPageToken: token === "" ? "tail" : "",
  }));
  await screen.findByRole("button", { name: /Work/ });
  // The first page only: no unseen tail is read by default.
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual([""]);
  values = [resource("Newcomer"), personal, work];
  await act(async () => {
    await f.client.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: ResourceQuery.listResources,
        input: subscriptionInventoryRequest(""),
        cardinality: "finite",
      }),
      refetchType: "active",
    });
  });
  await screen.findByRole("button", { name: /Newcomer/ });
  expect(f.requests.mock.calls.every((call) => call[0] === "")).toBe(true);
});
it("keeps retained badges unconfirmed during a deferred retry", async () => {
  const first = resource("Personal");
  let stage = 0;
  let finish!: (value: { resources: Resource[] }) => void;
  const f = mount(() => {
    if (stage === 1) throw new ConnectError("read failed", Code.Unavailable);
    if (stage === 2) return new Promise((done) => (finish = done));
    return { resources: [first] };
  });
  await screen.findByRole("button", { name: /28% remaining/ });
  stage = 1;
  fireEvent(windowThis(), new Event("focus"));
  await screen.findByRole("button", { name: /Personal · Quota unavailable/ });
  stage = 2;
  fireEvent.click(
    screen.getByRole("button", { name: "Explain subscription read problem" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(3));
  expect(screen.queryByRole("button", { name: /28% remaining/ })).toBeNull();
  expect(
    screen.getByRole("button", { name: /Personal · Quota unavailable/ }),
  ).toBeTruthy();
  await act(async () => finish({ resources: [first] }));
  await screen.findByRole("button", { name: /28% remaining/ });
});
it("rejects unknown blocking membership and malformed reset evidence", () => {
  const quota = (extra: Record<string, unknown>) => ({
    id: "weekly",
    state: "observed",
    remaining: 0.28,
    observed_at: new Date(now).toISOString(),
    blocking: true,
    comparison_group: "weekly",
    ...extra,
  });
  for (const extra of [
    { blocking: "true" },
    { blocking: 1 },
    { blocking: null },
    { blocking: undefined },
    { reset_at: "not-a-date" },
    { reset_at: 123 },
    { reset_at: null },
    { observed_at: 123 },
    { comparison_group: 123 },
    { remaining: "0.28" },
  ]) {
    const projected = railAccount(
      resource("Malformed", "chatgpt", { quota: [quota({}), quota(extra)] }),
    );
    expect(remainingBadge(projected.windows, now)).toBeUndefined();
    expect(freshWindow(projected.windows[1]!, now)).toBe(false);
  }
  const nonblocking = railAccount(
    resource("Nonblocking", "chatgpt", {
      quota: [quota({}), quota({ blocking: false, reset_at: "bad" })],
    }),
  );
  expect(remainingBadge(nonblocking.windows, now)).toBe(28);
  expect(freshWindow(nonblocking.windows[1]!, now)).toBe(false);
  for (const reset of [undefined, ""]) {
    expect(
      remainingBadge(
        railAccount(
          resource("Valid", "chatgpt", { quota: [quota({ reset_at: reset })] }),
        ).windows,
        now,
      ),
    ).toBe(28);
  }
});
it("shows malformed observation dates as unknown without individual percentages", async () => {
  const f = mount(() => ({
    resources: [
      resource("Invalid date", "chatgpt", {
        quota: [
          {
            id: "weekly",
            state: "observed",
            remaining: 0.28,
            observed_at: "not-a-date",
            blocking: true,
            comparison_group: "weekly",
          },
        ],
      }),
    ],
  }));
  const opener = await screen.findByRole("button", {
    name: /Invalid date · Quota unavailable/,
  });
  fireEvent.click(opener);
  const dialog = screen.getByRole("dialog", { name: "ChatGPT · Invalid date" });
  expect(dialog.textContent).toContain("No current quota evidence");
  expect(dialog.textContent).not.toContain("28% remaining");
  expect(dialog.textContent).not.toContain("Stale observation");
  expect(f.requests).toHaveBeenCalledOnce();
});
it("missing observed timestamps cannot establish individual or aggregate evidence", () => {
  for (const observed_at of [undefined, ""]) {
    const projected = railAccount(
      resource("Missing observation", "chatgpt", {
        quota: [
          {
            id: "weekly",
            state: "observed",
            remaining: 0.28,
            observed_at,
            blocking: true,
            comparison_group: "weekly",
          },
        ],
      }),
    );
    expect(projected.windows[0]!.valid).toBe(false);
    expect(remainingBadge(projected.windows, now)).toBeUndefined();
  }
});

it("explains a denied partial page locally and uses only its original read retry", async () => {
  const first = resource("Retained");
  let denied = true;
  const f = mount((token) => {
    if (token && denied)
      throw new ConnectError("untrusted-private-path", Code.PermissionDenied);
    return token
      ? { resources: [resource("Later")] }
      : { resources: [first], nextPageToken: "tail" };
  });
  await screen.findByRole("button", { name: /Retained/ });
  fireEvent.click(
    screen.getByRole("button", { name: /Load more Subscriptions/ }),
  );
  const trigger = await screen.findByRole("button", {
    name: "Explain subscription read problem",
  });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(trigger);
  const popup = screen.getByRole("dialog", {
    name: "Explain subscription read problem",
  });
  expect(popup.textContent).toContain("unread pages do not establish");
  expect(popup.textContent).toContain("not authorized");
  expect(screen.queryByText(/untrusted-private-path/)).toBeNull();
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual(["", "tail"]);
  expect(f.manage).not.toHaveBeenCalled();
  denied = false;
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await screen.findByRole("button", { name: /Later/ });
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual([
    "",
    "tail",
    "tail",
  ]);
  expect(f.manage).not.toHaveBeenCalled();
});
it("offers a saved-evidence recheck in the owning account popup without quota mutation", async () => {
  const f = mount(() => ({ resources: [resource("Original")] }));
  fireEvent.click(await screen.findByRole("button", { name: /Original ·/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "Recheck saved quota evidence" }),
  );
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(2));
  expect(f.manage).not.toHaveBeenCalled();
});

it("shows sidebar reset countdowns without quota or account operations", async () => {
  const reset = new Date(Date.now() + 529200000 + 120000).toISOString();
  const row = resource("Countdown rail", "chatgpt", {
    quota: [
      {
        id: "weekly",
        remaining: 0.28,
        state: "observed",
        observed_at: new Date(now).toISOString(),
        reset_at: reset,
        comparison_group: "weekly",
        blocking: true,
      },
    ],
  });
  const read = vi.fn(() => ({ resources: [row] }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1],
      }),
    });
    router.service(ResourceService, { listResources: read });
  });
  const manage = vi.fn();
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <TransportProvider transport={transport}>
        <SubscriptionRail enabled manage={manage} />
      </TransportProvider>
    </QueryClientProvider>,
  );
  const opener = await screen.findByRole("button", { name: /Countdown rail/ });
  fireEvent.click(opener);
  expect(screen.getByText("Resets in 6 days 3 hours")).toBeTruthy();
  expect(screen.getByTitle(reset).getAttribute("datetime")).toBe(reset);
  expect(read).toHaveBeenCalledTimes(1);
  expect(manage).not.toHaveBeenCalled();
});

it.each([
  {
    name: "healthy",
    quota: [
      {
        id: "weekly",
        remaining: 0.28,
        state: "observed",
        observed_at: new Date(now).toISOString(),
        comparison_group: "weekly",
        blocking: true,
      },
    ],
    status: "Observed",
  },
  {
    name: "stale",
    quota: [
      {
        id: "weekly",
        remaining: 0.28,
        state: "observed",
        observed_at: new Date(Date.now() - 360000).toISOString(),
        comparison_group: "weekly",
        blocking: true,
      },
    ],
    status: "Stale",
  },
  {
    name: "failed",
    quota: [
      {
        id: "weekly",
        remaining: 0.28,
        state: "failed",
        observed_at: new Date(now).toISOString(),
        comparison_group: "weekly",
        blocking: true,
      },
    ],
    status: "Observation failed",
  },
  { name: "empty", quota: [], status: "No current quota evidence" },
])(
  "shows truthful $name observations without inventing an operation failure",
  async ({ quota, status }) => {
    const f = mount(() => ({
      resources: [resource("Personal", "chatgpt", { quota })],
    }));
    fireEvent.click(
      await screen.findByRole("button", { name: /ChatGPT · Personal/ }),
    );
    const dialog = screen.getByRole("dialog", { name: "ChatGPT · Personal" });
    expect(dialog.textContent).toContain(status);
    expect(dialog.textContent).not.toContain(
      "Quota observation was not confirmed.",
    );
    expect(f.requests).toHaveBeenCalledTimes(1);
    expect(f.manage).not.toHaveBeenCalled();
  },
);

it.each(["focus", "timer"])(
  "retains exhausted account nodes without a refresh-only anchor on %s",
  async (trigger) => {
    if (trigger === "timer")
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const rows = [resource("Personal"), resource("Work")];
    let finish!: (value: { resources: Resource[] }) => void;
    let refreshing = false;
    const f = mount(() =>
      refreshing ? new Promise((done) => (finish = done)) : { resources: rows },
    );
    const opener = await screen.findByRole("button", {
      name: /Personal · 28%/,
    });
    const image = opener.querySelector("img");
    expect(screen.queryByRole("button", { name: "Reload list" })).toBeNull();
    expect(f.container.querySelector(".sidebar-continuation")).toBeNull();
    fireEvent.click(opener);
    await act(async () => {
      await new Promise((done) => setTimeout(done, 0));
    });
    refreshing = true;
    if (trigger === "focus") fireEvent(windowThis(), new Event("focus"));
    else await act(async () => vi.advanceTimersByTimeAsync(60000));
    await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: /Personal · 28%/ })).toBe(opener);
    expect(opener.querySelector("img")).toBe(image);
    expect(opener.getAttribute("aria-expanded")).toBe("true");
    expect(f.container.querySelector(".sidebar-continuation")).toBeNull();
    await act(async () => finish({ resources: rows }));
    expect(screen.getByRole("button", { name: /Personal · 28%/ })).toBe(opener);
  },
);
it("retains an accepted continuation without refresh text and disables admission", async () => {
  const first = resource("Personal");
  let finish!: (value: {
    resources: Resource[];
    nextPageToken: string;
  }) => void;
  let refreshing = false;
  const f = mount(() =>
    refreshing
      ? new Promise((done) => (finish = done))
      : { resources: [first], nextPageToken: "tail" },
  );
  await screen.findByRole("button", { name: /Personal/ });
  const anchor = f.container.querySelector(".sidebar-continuation");
  const more = screen.getByRole("button", { name: /Load more Subscriptions/ });
  await act(async () => {
    await new Promise((done) => setTimeout(done, 0));
  });
  refreshing = true;
  fireEvent(windowThis(), new Event("focus"));
  await waitFor(() => expect(more.hasAttribute("disabled")).toBe(true));
  expect(f.container.querySelector(".sidebar-continuation")).toBe(anchor);
  expect(anchor?.querySelector('[role="status"]')).toBeNull();
  expect(f.requests.mock.calls.map((call) => call[0])).toEqual(["", ""]);
  await act(async () =>
    finish({ resources: [first], nextPageToken: "renewed" }),
  );
  await waitFor(() => expect(more.hasAttribute("disabled")).toBe(false));
});
it("restores navigation fallback when an accepted refresh removes the popover opener", async () => {
  const first = resource("Personal");
  let removed = false;
  const f = mount(() => ({ resources: removed ? [] : [first] }));
  fireEvent.click(await screen.findByRole("button", { name: /Personal/ }));
  removed = true;
  fireEvent(windowThis(), new Event("focus"));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(f.focusFallback).toHaveBeenCalledOnce();
  expect(screen.queryByRole("button", { name: "Reload list" })).toBeNull();
});
it("keeps explicit restart at the accepted 200-page limit", async () => {
  const first = resource("Personal");
  const f = mount((token) => ({
    resources: token ? [] : [first],
    nextPageToken: String(token ? Number(token) + 1 : 1),
  }));
  await screen.findByRole("button", { name: /Personal/ });
  for (let page = 1; page < 200; page++) {
    fireEvent.click(
      screen.getByRole("button", { name: /Load more Subscriptions/ }),
    );
    await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(page + 1));
    if (page < 199)
      await waitFor(() =>
        expect(
          screen
            .getByRole("button", { name: /Load more Subscriptions/ })
            .hasAttribute("disabled"),
        ).toBe(false),
      );
  }
  const reload = await screen.findByRole("button", { name: "Reload list" });
  expect(
    screen.queryByRole("button", { name: /Load more Subscriptions/ }),
  ).toBeNull();
  fireEvent.click(reload);
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(201));
  expect(f.requests.mock.calls.at(-1)?.[0]).toBe("");
}, 30000);

it("presents fresh native quota directly without account identity or technical disclosures", async () => {
  const observed = new Date().toISOString(),
    reset = new Date(Date.now() + 86400000).toISOString();
  const row = resource("personal@example.test", "chatgpt", {
    quota: [
      {
        id: "codex:primary",
        state: "observed",
        remaining: 0.57,
        blocking: true,
        comparison_group: "primary",
        observed_at: observed,
        reset_at: reset,
      },
    ],
  });
  const f = mount(() => ({ resources: [row] }));
  const opener = await screen.findByRole("button", {
    name: /personal@example.test/,
  });
  fireEvent.click(opener);
  const dialog = screen.getByRole("dialog");
  expect(dialog.textContent).toContain("personal@example.test");
  expect(dialog.textContent).toContain("Connected");
  expect(dialog.textContent).toContain("codex:primary");
  expect(
    dialog.querySelector(".subscription-quota-value strong")?.textContent,
  ).toBe("57%");
  expect(
    (dialog.querySelector(".subscription-quota-bar span") as HTMLElement).style
      .width,
  ).toBe("57%");
  expect(dialog.querySelectorAll("details")).toHaveLength(0);
  expect(dialog.textContent).not.toContain(row.id);
  expect(dialog.textContent).not.toMatch(
    /Account identity|Quota details|most restrictive/,
  );
  expect(dialog.querySelectorAll("time")).toHaveLength(2);
  expect(
    [...dialog.querySelectorAll("time")].map((node) =>
      node.getAttribute("title"),
    ),
  ).toEqual([reset, observed]);
  expect(f.requests).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Manage subscriptions" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.manage).toHaveBeenCalledOnce();
  expect(f.requests).toHaveBeenCalledOnce();
});

it("retains duplicate-alias ownership and every native window in original order for every service", async () => {
  const quota = (id: string) => ({
    id,
    state: "observed",
    remaining: 0.57,
    blocking: true,
    comparison_group: "shared",
    observed_at: new Date(now).toISOString(),
  });
  const rows = ["chatgpt", "claude", "grok"].flatMap((service) => [
    resource("Same alias", service, {
      quota: [quota(`${service}:second`), quota(`${service}:first`)],
    }),
    resource("Same alias", service, { quota: [quota(`${service}:other`)] }),
  ]);
  const f = mount(() => ({ resources: rows }));
  await screen.findAllByRole("button", { name: /Same alias/ });
  for (const row of rows) {
    fireEvent.click(
      screen.getByRole("button", {
        name: new RegExp(`Same alias · ${rows.indexOf(row) + 1} ·`),
      }),
    );
    const dialog = screen.getByRole("dialog");
    expect(
      [...dialog.querySelectorAll(".subscription-quota-id")].map(
        (node) => node.textContent,
      ),
    ).toEqual(railAccount(row).windows.map((window) => window.id));
    expect(dialog.textContent).not.toContain(row.id);
    fireEvent.keyDown(dialog, { key: "Escape" });
  }
  expect(f.requests).toHaveBeenCalledOnce();
});

for (const [name, extra, percent, state] of [
  ["zero", { remaining: 0 }, "0%", "Observed"],
  ["full", { remaining: 1 }, "100%", "Observed"],
  [
    "stale",
    { observed_at: new Date(Date.now() - 600000).toISOString() },
    "57%",
    "Stale observation",
  ],
  ["failed", { state: "failed" }, "57%", "Observation failed"],
  [
    "unsupported",
    { state: "unsupported" },
    undefined,
    "Quota observation unsupported",
  ],
  ["unknown", { state: "unknown" }, undefined, "No current quota evidence"],
  [
    "invalid observation",
    { observed_at: "bad-date" },
    undefined,
    "No current quota evidence",
  ],
  [
    "future observation",
    { observed_at: new Date(Date.now() + 86400000).toISOString() },
    undefined,
    "Stale observation",
  ],
  [
    "missing observation",
    { observed_at: "" },
    undefined,
    "No current quota evidence",
  ],
  [
    "invalid reset",
    { reset_at: "bad-date" },
    undefined,
    "No current quota evidence",
  ],
  [
    "expired reset",
    { reset_at: new Date(Date.now() - 60000).toISOString() },
    "57%",
    "Stale observation",
  ],
] as const)
  it(`retains truthful ${name} window presentation without restoring current evidence`, async () => {
    const row = resource(name, "chatgpt", {
      quota: [
        Object.assign(
          {
            id: "native:exact",
            state: "observed",
            remaining: 0.57,
            blocking: true,
            comparison_group: "primary",
            observed_at: new Date(now).toISOString(),
          },
          extra,
        ),
      ],
    });
    const f = mount(() => ({ resources: [row] }));
    fireEvent.click(
      await screen.findByRole("button", { name: new RegExp(name) }),
    );
    const dialog = screen.getByRole("dialog");
    expect(
      dialog.querySelector(".subscription-quota-value strong")?.textContent,
    ).toBe(percent);
    expect(dialog.textContent).toContain(state);
    expect(dialog.querySelectorAll(".subscription-quota-bar")).toHaveLength(
      percent === undefined ? 0 : 1,
    );
    if (name !== "zero" && name !== "full")
      expect(
        dialog.querySelector(".subscription-quota-historical"),
      ).toBeTruthy();
    expect(f.requests).toHaveBeenCalledOnce();
  });

it("keeps failed-read historical values and warning visible through a deferred saved recheck", async () => {
  let stage = 0,
    finish!: (result: { resources: Resource[] }) => void;
  const row = resource("Retained"),
    f = mount(() => {
      if (stage === 1)
        throw new ConnectError("saved read failed", Code.Unavailable);
      if (stage === 2)
        return new Promise((resolve) => {
          finish = resolve;
        });
      return { resources: [row] };
    });
  const opener = await screen.findByRole("button", { name: /Retained/ });
  fireEvent.click(opener);
  stage = 1;
  fireEvent(globalThis.window, new Event("focus"));
  await screen.findByText(
    "Current reads are unavailable. Previous saved values are shown.",
  );
  expect(
    screen
      .getByRole("dialog", { name: /Retained/ })
      .querySelector(".subscription-quota-historical"),
  ).toBeTruthy();
  stage = 2;
  fireEvent.click(
    screen.getByRole("button", { name: "Explain subscription read problem" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(3));
  expect(
    screen.getByText(
      "Current reads are unavailable. Previous saved values are shown.",
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Recheck saved quota evidence",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    screen.queryByRole("button", { name: /Retained · 28% remaining/ }),
  ).toBeNull();
  await act(async () => finish({ resources: [row] }));
  await screen.findByRole("button", { name: /Retained · 28% remaining/ });
  expect(
    screen.queryByText(
      "Current reads are unavailable. Previous saved values are shown.",
    ),
  ).toBeNull();
  expect(
    screen
      .getByRole("dialog", { name: /Retained/ })
      .querySelector(".subscription-quota-historical"),
  ).toBeNull();
});

it("close and outside dismissal retain the original opener without another read", async () => {
  const row = resource("Original opener"),
    f = mount(() => ({ resources: [row] }));
  const opener = await screen.findByRole("button", {
    name: /Original opener/,
  });
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Close quota details" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
  fireEvent.click(opener);
  fireEvent.pointerDown(document.body);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
  expect(f.requests).toHaveBeenCalledOnce();
});

const paidBucket = (
  balance: string | null = "60961.1135370000",
  extra = {},
) => ({
  id: "codex",
  has_credits: true,
  unlimited: false,
  balance,
  observed_at: new Date(now).toISOString(),
  ...extra,
});
const withCredits = (buckets = [paidBucket()], extra = {}) =>
  resource("Personal", "chatgpt", {
    subscription: { paid_credits: buckets, ...extra },
  });
async function openPersonal() {
  const opener = await screen.findByRole("button", {
    name: /ChatGPT · Personal/,
  });
  fireEvent.click(opener);
  return {
    opener,
    popup: screen.getByRole("dialog", { name: "ChatGPT · Personal" }),
  };
}
it("shows selected saved paid credits above quota with exact disclosure and unchanged actions", async () => {
  const f = mount(
    () => ({
      resources: [
        withCredits(),
        resource("Other", "chatgpt", {
          subscription: { paid_credits: [paidBucket("12")] },
        }),
        resource("Claude", "claude", {
          subscription: { paid_credits: [paidBucket("99")] },
        }),
      ],
    }),
    true,
    true,
  );
  const { opener, popup } = await openPersonal();
  const card = popup.querySelector(".subscription-paid-credits")!;
  expect(within(popup).getByText("60,961.11")).toBeTruthy();
  expect(within(popup).queryByText("12.00")).toBeNull();
  expect(
    card.compareDocumentPosition(
      popup.querySelector(".subscription-quota-id")!,
    ) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(within(popup).getByText("28%")).toBeTruthy();
  expect(
    within(popup).getByRole("button", { name: "Recheck saved quota evidence" }),
  ).toBeTruthy();
  const info = within(popup).getByRole("button", {
    name: "Show exact paid-credit balance",
  });
  fireEvent.focus(info);
  expect(within(popup).getByRole("tooltip").textContent).toContain(
    "60961.1135370000",
  );
  // The disclosure leaves normal flow so it cannot resize the card or popover.
  expect(within(popup).getByRole("tooltip").className).toContain(
    "subscription-paid-exact-overlay",
  );
  fireEvent.keyDown(info, { key: "Escape" });
  expect(screen.getByRole("dialog")).toBe(popup);
  expect(within(popup).queryByRole("tooltip")).toBeNull();
  fireEvent.keyDown(popup, { key: "Escape" });
  expect(document.activeElement).toBe(opener);
  fireEvent.click(
    await screen.findByRole("button", { name: /Claude · Claude/ }),
  );
  expect(
    screen.getByRole("dialog").querySelector(".subscription-paid-credits"),
  ).toBeNull();
  expect(f.requests).toHaveBeenCalledOnce();
});
it("projects validated credit evidence without numerical coercion or quota effects", () => {
  const longest = "9".repeat(60) + ".001";
  const values = [
    paidBucket("0"),
    paidBucket(null, { id: "unknown" }),
    paidBucket(longest, { id: "long" }),
  ];
  const projected = railAccount(withCredits(values));
  expect(projected.paidCredits.map((b) => b.balance)).toEqual([
    "0",
    null,
    longest,
  ]);
  expect(remainingBadge(projected.windows, Date.now())).toBe(28);
  for (const value of [
    paidBucket("bad"),
    paidBucket("1".repeat(65)),
    paidBucket("1", { observed_at: "bad" }),
    paidBucket("1", { unlimited: "true" }),
  ]) {
    expect(railAccount(withCredits([value])).paidCredits).toEqual([]);
  }
  expect(
    railAccount(
      resource("Other", "claude", { subscription: { paid_credits: values } }),
    ).paidCredits,
  ).toEqual([]);
});
it.each([
  ["0", {}, "0.00"],
  [null, {}, "Balance unknown"],
  ["0.001", {}, "<0.01"],
  ["42", { unlimited: true }, "Unlimited credits"],
])(
  "keeps distinct paid balance %s in the rail",
  async (balance, extra, label) => {
    mount(
      () => ({ resources: [withCredits([paidBucket(balance, extra)])] }),
      true,
      true,
    );
    const { popup } = await openPersonal();
    expect(within(popup).getByText(label)).toBeTruthy();
  },
);
it("renders each paid bucket in source order with separate original observations", async () => {
  mount(
    () => ({
      resources: [
        withCredits([
          paidBucket("3", { id: "first" }),
          paidBucket("7", { id: "second" }),
        ]),
      ],
    }),
    true,
    true,
  );
  const { popup } = await openPersonal();
  expect(
    [...popup.querySelectorAll(".subscription-paid-bucket")].map(
      (n) => n.textContent,
    ),
  ).toEqual(["first", "second"]);
  expect(within(popup).getByText("3.00")).toBeTruthy();
  expect(within(popup).getByText("7.00")).toBeTruthy();
  expect(within(popup).queryByText("10.00")).toBeNull();
});
it("retains credit values and timestamps across failed and deferred reads until complete replacement", async () => {
  const original = withCredits();
  const replacement = {
    ...withCredits([
      paidBucket("12", { observed_at: new Date(now + 1000).toISOString() }),
    ]),
    id: original.id,
    revision: 2n,
  };
  let phase = 0;
  let finish!: (value: { resources: Resource[] }) => void;
  const f = mount(
    () => {
      if (phase === 1) throw new ConnectError("read failed", Code.Unavailable);
      if (phase === 2)
        return new Promise((resolve) => {
          finish = resolve;
        });
      return { resources: [original] };
    },
    true,
    true,
  );
  const { popup } = await openPersonal();
  const originalTimes = [
    ...popup.querySelectorAll(".subscription-paid-observation time"),
  ].map((n) => n.getAttribute("datetime"));
  phase = 1;
  fireEvent.click(
    within(popup).getByRole("button", { name: "Recheck saved quota evidence" }),
  );
  await within(popup).findByText(/Refresh failed; retained observation/);
  expect(within(popup).getByText("60,961.11")).toBeTruthy();
  expect(
    [...popup.querySelectorAll(".subscription-paid-observation time")].map(
      (n) => n.getAttribute("datetime"),
    ),
  ).toEqual(originalTimes);
  phase = 2;
  fireEvent.click(
    screen.getByRole("button", { name: "Explain subscription read problem" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(3));
  expect(
    within(popup).getByText(/Refresh failed; retained observation/),
  ).toBeTruthy();
  await act(async () => finish({ resources: [replacement] }));
  await within(popup).findByText("12.00");
  expect(within(popup).queryByText("60,961.11")).toBeNull();
  expect(
    within(popup).queryByText(/Refresh failed; retained observation/),
  ).toBeNull();
});
const paidTimes = (popup: HTMLElement) =>
  [...popup.querySelectorAll(".subscription-paid-observation time")].map((n) =>
    n.getAttribute("datetime"),
  );
const refreshStatus = (f: { client: QueryClient }) =>
  act(async () => {
    await f.client.invalidateQueries({
      predicate: (q) => JSON.stringify(q.queryKey).includes("GetStatus"),
    });
  });
describe("paid credits while saved reads are paused", () => {
  it("keeps accepted credits without a failure label while the server is stopping", async () => {
    let stopping = false;
    const f = mount(
      () => ({ resources: [withCredits()] }),
      true,
      true,
      () => stopping,
    );
    const { popup } = await openPersonal();
    const times = paidTimes(popup);
    expect(times).not.toHaveLength(0);
    stopping = true;
    await refreshStatus(f);
    await waitFor(() =>
      expect(
        within(popup)
          .getByRole("button", { name: "Recheck saved quota evidence" })
          .hasAttribute("disabled"),
      ).toBe(true),
    );
    expect(
      within(popup).queryByText(/Refresh failed; retained observation/),
    ).toBeNull();
    expect(within(popup).getByText("60,961.11")).toBeTruthy();
    expect(paidTimes(popup)).toEqual(times);
    // Pausing never admits another saved read.
    expect(f.requests).toHaveBeenCalledOnce();
  });
  it("keeps reading and showing accepted credits without a failure label while the document is hidden", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const f = mount(() => ({ resources: [withCredits()] }), true, true);
    const { popup } = await openPersonal();
    const times = paidTimes(popup);
    const visibility = vi
      .spyOn(document, "visibilityState", "get")
      .mockReturnValue("hidden");
    fireEvent(document, new Event("visibilitychange"));
    expect(
      within(popup)
        .getByRole("button", { name: "Recheck saved quota evidence" })
        .hasAttribute("disabled"),
    ).toBe(false);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(f.requests).toHaveBeenCalledTimes(2);
    expect(
      within(popup).queryByText(/Refresh failed; retained observation/),
    ).toBeNull();
    expect(within(popup).getByText("60,961.11")).toBeTruthy();
    expect(paidTimes(popup)).toEqual(times);
    visibility.mockRestore();
  });
  it("keeps ageing retained credits to stale while the server is stopping", async () => {
    let stopping = false;
    const f = mount(
      () => ({ resources: [withCredits()] }),
      true,
      true,
      () => stopping,
    );
    const { popup } = await openPersonal();
    const paidStatus = () =>
      popup.querySelector(".subscription-paid-observation")?.textContent ?? "";
    expect(paidStatus()).toMatch(/^Observed/);
    stopping = true;
    await refreshStatus(f);
    await waitFor(() =>
      expect(
        within(popup)
          .getByRole("button", { name: "Recheck saved quota evidence" })
          .hasAttribute("disabled"),
      ).toBe(true),
    );
    // Only the wall clock advances; no saved read is admitted by the pause.
    const real = Date.now.bind(Date);
    const clock = vi
      .spyOn(Date, "now")
      .mockImplementation(() => real() + 301000);
    await waitFor(() => expect(paidStatus()).toMatch(/^Stale observation/), {
      timeout: 3000,
    });
    expect(
      within(popup).queryByText(/Refresh failed; retained observation/),
    ).toBeNull();
    expect(f.requests).toHaveBeenCalledOnce();
    clock.mockRestore();
  });
  it("keeps the failure label for a real saved-read failure through a later pause", async () => {
    let failed = false;
    let stopping = false;
    const f = mount(
      () => {
        if (failed) throw new ConnectError("read failed", Code.Unavailable);
        return { resources: [withCredits()] };
      },
      true,
      true,
      () => stopping,
    );
    const { popup } = await openPersonal();
    const times = paidTimes(popup);
    failed = true;
    fireEvent.click(
      within(popup).getByRole("button", {
        name: "Recheck saved quota evidence",
      }),
    );
    await within(popup).findByText(/Refresh failed; retained observation/);
    stopping = true;
    await refreshStatus(f);
    expect(
      within(popup).getByText(/Refresh failed; retained observation/),
    ).toBeTruthy();
    expect(within(popup).getByText("60,961.11")).toBeTruthy();
    expect(paidTimes(popup)).toEqual(times);
  });
  it("keeps the unsupported state distinct while paused", async () => {
    let supported = true;
    let stopping = false;
    const f = mount(
      () => ({ resources: [withCredits()] }),
      true,
      () => supported,
      () => stopping,
    );
    const { popup } = await openPersonal();
    supported = false;
    stopping = true;
    await refreshStatus(f);
    await within(popup).findByText(
      "Unavailable. Update the server and original Runner Device.",
    );
    expect(
      within(popup).queryByText(/Refresh failed; retained observation/),
    ).toBeNull();
    expect(within(popup).queryByText("60,961.11")).toBeNull();
    expect(f.requests).toHaveBeenCalledOnce();
  });
});
it.each([now - 300001, now + 60000])(
  "marks stale/future paid observation independently of fresh quota",
  async (at) => {
    mount(
      () => ({
        resources: [
          withCredits([
            paidBucket("9", { observed_at: new Date(at).toISOString() }),
          ]),
        ],
      }),
      true,
      true,
    );
    const { popup } = await openPersonal();
    expect(within(popup).getByText("Stale observation")).toBeTruthy();
    expect(within(popup).getByText("28%")).toBeTruthy();
  },
);
it("removes paid display support independently without new saved-resource reads", async () => {
  let supported = true;
  const f = mount(
    () => ({ resources: [withCredits()] }),
    true,
    () => supported,
  );
  const { popup } = await openPersonal();
  expect(within(popup).getByText("60,961.11")).toBeTruthy();
  supported = false;
  await act(async () => {
    await f.client.invalidateQueries({
      predicate: (q) => JSON.stringify(q.queryKey).includes("GetStatus"),
    });
  });
  await within(popup).findByText(
    "Unavailable. Update the server and original Runner Device.",
  );
  expect(within(popup).queryByText("60,961.11")).toBeNull();
  expect(within(popup).getByText("28%")).toBeTruthy();
  expect(f.requests).toHaveBeenCalledOnce();
});
it("clears credits on credential replacement and authentication loss", async () => {
  const first = withCredits();
  let phase = 0;
  mount(
    () => {
      if (phase === 2) throw new ConnectError("auth", Code.Unauthenticated);
      return {
        resources: [
          phase ? { ...withCredits([]), id: first.id, revision: 2n } : first,
        ],
      };
    },
    true,
    true,
  );
  const { popup } = await openPersonal();
  fireEvent.focus(
    within(popup).getByRole("button", {
      name: "Show exact paid-credit balance",
    }),
  );
  phase = 1;
  fireEvent.click(
    within(popup).getByRole("button", { name: "Recheck saved quota evidence" }),
  );
  await within(popup).findByText("Balance unknown");
  expect(within(popup).queryByRole("tooltip")).toBeNull();
  phase = 2;
  fireEvent.click(
    within(popup).getByRole("button", { name: "Recheck saved quota evidence" }),
  );
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.queryByText("60,961.11")).toBeNull();
});

it.each([
  {},
  { quota_state: "failed" },
  { paid_credits: [paidBucket("invalid")] },
])(
  "never manufactures a balance for sparse, failed or malformed saved evidence %j",
  async (subscription) => {
    mount(
      () => ({
        resources: [resource("Personal", "chatgpt", { subscription })],
      }),
      true,
      true,
    );
    const { popup } = await openPersonal();
    expect(
      within(popup).getByText(
        "quota_state" in subscription
          ? "Balance unavailable"
          : "Balance unknown",
      ),
    ).toBeTruthy();
    expect(within(popup).queryByText("0.00")).toBeNull();
  },
);
it("preserves a 64-character source decimal in the selected rail disclosure", async () => {
  const balance = "9".repeat(60) + ".001";
  mount(
    () => ({ resources: [withCredits([paidBucket(balance)])] }),
    true,
    true,
  );
  const { popup } = await openPersonal();
  fireEvent.focus(
    within(popup).getByRole("button", {
      name: "Show exact paid-credit balance",
    }),
  );
  expect(within(popup).getByRole("tooltip").textContent).toContain(balance);
});

describe("credential generation identity", () => {
  const connection = { id: newRequestId() };
  const generationA = newRequestId();
  const generationB = newRequestId();
  const accountAt = (id: string, generation: string, revision: bigint) => ({
    ...withCredits([paidBucket("1.2300")], { generation }),
    id,
    revision,
    documentJson: encode({
      type: "subscription",
      name: "Personal",
      subscription_service: "chatgpt",
      connection,
      subscription: { paid_credits: [paidBucket("1.2300")], generation },
    }),
  });
  async function pin() {
    const { popup } = await openPersonal();
    const info = within(popup).getByRole("button", {
      name: "Show exact paid-credit balance",
    });
    fireEvent.click(info);
    expect(info.getAttribute("aria-expanded")).toBe("true");
    expect(within(popup).getByRole("tooltip").textContent).toContain("1.2300");
    return popup;
  }
  it("closes a pinned exact disclosure when only the credential generation changes", async () => {
    const id = newRequestId();
    let current = accountAt(id, generationA, 1n);
    const f = mount(() => ({ resources: [current] }), true, true);
    const popup = await pin();
    current = accountAt(id, generationB, 2n);
    fireEvent(globalThis.window, new Event("focus"));
    await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        within(popup)
          .getByRole("button", {
            name: "Show exact paid-credit balance",
          })
          .getAttribute("aria-expanded"),
      ).toBe("false"),
    );
    expect(within(popup).queryByRole("tooltip")).toBeNull();
    expect(within(popup).getByText("1.23")).toBeTruthy();
  });
  it("keeps the pinned disclosure across a same-generation saved refresh", async () => {
    const id = newRequestId();
    let current = accountAt(id, generationA, 1n);
    const f = mount(() => ({ resources: [current] }), true, true);
    const popup = await pin();
    current = accountAt(id, generationA, 2n);
    fireEvent(globalThis.window, new Event("focus"));
    await waitFor(() => expect(f.requests).toHaveBeenCalledTimes(2));
    expect(
      within(popup)
        .getByRole("button", { name: "Show exact paid-credit balance" })
        .getAttribute("aria-expanded"),
    ).toBe("true");
    expect(within(popup).getByRole("tooltip").textContent).toContain("1.2300");
  });
  it("sends no extra request when interacting with the disclosure", async () => {
    const id = newRequestId();
    const f = mount(
      () => ({ resources: [accountAt(id, generationA, 1n)] }),
      true,
      true,
    );
    await pin();
    expect(f.requests).toHaveBeenCalledOnce();
  });
});
