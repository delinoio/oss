// SPDX-License-Identifier: Apache-2.0
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useMemo, useRef, type ReactNode } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { ScrollContinuation } from "./scroll-continuation";
import { TransportProvider } from "@connectrpc/connect-query";
import { ResourceQuery, EntityKind } from "@delinoio/delidev-api-client";
import { createRouterTransport } from "@connectrpc/connect";
import {
  usePaginationChain,
  usePaginationRefresh,
} from "./scroll-pagination-query";
import { type PaginationReader } from "./scroll-pagination";
import { i18n, SupportedLanguage } from "./localization";

type Row = { id: string; revision: bigint };
afterEach(() => vi.restoreAllMocks());
function List({
  read,
  active = true,
  initialRead = true,
  showProgress = false,
}: {
  read: PaginationReader<Row>;
  active?: boolean;
  initialRead?: boolean;
  showProgress?: boolean;
}) {
  const root = useRef<HTMLDivElement>(null);
  const reader = useMemo(() => read, [read]);
  const query = usePaginationChain("test-scope", active, reader, initialRead);
  return (
    <div ref={root}>
      <button type="button" onClick={query.reload}>
        Load
      </button>
      {query.rows.map((row) => (
        <p key={row.id}>{row.id}</p>
      ))}
      <ScrollContinuation
        query={query}
        label="items"
        root={root}
        active={active}
        showProgress={showProgress}
      />
    </div>
  );
}
function renderValue(value: ReactNode) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      {value}
    </QueryClientProvider>,
  );
}
function viewport() {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(400);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    top: 0,
    bottom: 300,
    left: 0,
    right: 200,
    width: 200,
    height: 300,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  });
}
it("fills an underfilled viewport serially and stops at exhaustion", async () => {
  viewport();
  let concurrent = 0,
    max = 0;
  const read = vi.fn<PaginationReader<Row>>(async (token) => {
    max = Math.max(max, ++concurrent);
    await Promise.resolve();
    concurrent--;
    return {
      rows: [{ id: token || "first", revision: 1n }],
      nextPageToken: token === "second" ? "" : "second",
    };
  });
  const view = renderValue(<List read={read} />);
  await screen.findByText("second");
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "second"]);
  expect(max).toBe(1);
  expect(screen.getByText("first")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Load more items" })).toBeNull();
  expect(screen.queryByRole("status")).toBeNull();
  expect(
    view.container.querySelector("[data-continuation=items]"),
  ).toBeTruthy();
  fireEvent.scroll(view.container.firstElementChild!);
  await act(async () => {
    await Promise.resolve();
  });
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "second"]);
});
it.each(
  [SupportedLanguage.English, SupportedLanguage.Korean].flatMap((language) =>
    [true, false].map((empty) => ({ language, empty })),
  ),
)(
  "retains the exhausted anchor without a completion announcement ($language, empty=$empty)",
  async ({ language, empty }) => {
    await i18n.changeLanguage(language);
    const read = vi.fn<PaginationReader<Row>>(async () => ({
      rows: empty ? [] : [{ id: "accepted", revision: 1n }],
      nextPageToken: "",
    }));
    const view = renderValue(<List read={read} />);
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
    const anchor = view.container.querySelector("[data-continuation=items]");
    expect(anchor).toBeTruthy();
    expect(anchor?.textContent).toBe("");
    expect(anchor?.querySelector("button")).toBeNull();
    expect(screen.queryByText("accepted") !== null).toBe(!empty);
  },
);
it("never starts an explicit read on mount or scroll", async () => {
  viewport();
  const read = vi.fn<PaginationReader<Row>>(async () => ({
    rows: [],
    nextPageToken: "",
  }));
  const view = renderValue(<List read={read} initialRead={false} />);
  fireEvent.scroll(view.container.firstElementChild!);
  await act(async () => {
    await Promise.resolve();
  });
  expect(read).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Load" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
});
it("retains accepted data after continuation failure and requires an explicit exact retry", async () => {
  viewport();
  const read = vi.fn<PaginationReader<Row>>(async (token) => {
    if (token) throw new ConnectError("offline", Code.Unavailable);
    return {
      rows: [{ id: "accepted", revision: 1n }],
      nextPageToken: "exact-request",
    };
  });
  const view = renderValue(<List read={read} />);
  await screen.findByRole("button", { name: "Retry" });
  fireEvent.scroll(view.container.firstElementChild!);
  fireEvent.scroll(view.container.firstElementChild!);
  expect(read).toHaveBeenCalledTimes(2);
  expect(screen.getByText("accepted")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(read.mock.calls.map(([token]) => token)).toEqual([
    "",
    "exact-request",
    "exact-request",
  ]);
});
it("suppresses automatic reads when inactive, document-hidden or under a closed disclosure", async () => {
  viewport();
  const read = vi.fn<PaginationReader<Row>>(async () => ({
    rows: [],
    nextPageToken: "next",
  }));
  const inactive = renderValue(<List read={read} active={false} />);
  await act(async () => {
    await Promise.resolve();
  });
  expect(read).not.toHaveBeenCalled();
  inactive.unmount();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  const hidden = renderValue(<List read={read} />);
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await act(async () => {
    await Promise.resolve();
  });
  expect(read).toHaveBeenCalledTimes(1);
  hidden.unmount();
  vi.restoreAllMocks();
  viewport();
  read.mockClear();
  renderValue(
    <details>
      <summary>Closed</summary>
      <List read={read} />
    </details>,
  );
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await act(async () => {
    await Promise.resolve();
  });
  expect(read).toHaveBeenCalledTimes(1);
});

it("keeps identical refresh inputs isolated from an inactive mounted owner", async () => {
  const client = new QueryClient(),
    active = vi.fn(),
    inactive = vi.fn();
  function Marker({
    enabled,
    refresh,
  }: {
    enabled: boolean;
    refresh: () => void;
  }) {
    usePaginationRefresh(
      ResourceQuery.listResources,
      { filter: { kind: EntityKind.PROJECT, pageSize: 50 } },
      enabled,
      refresh,
    );
    return null;
  }
  render(
    <TransportProvider transport={createRouterTransport(() => {})}>
      <QueryClientProvider client={client}>
        <Marker enabled={false} refresh={inactive} />
        <Marker enabled refresh={active} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await act(() => client.invalidateQueries({ refetchType: "active" }));
  expect(active).toHaveBeenCalledTimes(1);
  expect(inactive).not.toHaveBeenCalled();
});

it("applies a marker invalidation that arrived during an active read once that read settles", async () => {
  const client = new QueryClient();
  const releases: (() => void)[] = [];
  let version = 0;
  const read = vi.fn<PaginationReader<Row>>(
    () =>
      new Promise((resolve) => {
        const current = ++version;
        releases.push(() =>
          resolve({
            rows: [{ id: `row-${current}`, revision: BigInt(current) }],
            nextPageToken: "",
          }),
        );
      }),
  );
  function Owner() {
    const chain = usePaginationChain("queued-invalidation", true, read);
    usePaginationRefresh(
      ResourceQuery.listResources,
      { filter: { kind: EntityKind.PROJECT, pageSize: 50 } },
      true,
      chain.refresh,
    );
    return (
      <>
        {chain.rows.map((row) => (
          <p key={row.id}>{row.id}</p>
        ))}
      </>
    );
  }
  render(
    <TransportProvider transport={createRouterTransport(() => {})}>
      <QueryClientProvider client={client}>
        <Owner />
      </QueryClientProvider>
    </TransportProvider>,
  );
  expect(read).toHaveBeenCalledTimes(1);
  // Two invalidations while the initial read is open coalesce into one refresh.
  await act(() => client.invalidateQueries({ refetchType: "active" }));
  await act(() => client.invalidateQueries({ refetchType: "active" }));
  expect(read).toHaveBeenCalledTimes(1);
  await act(async () => {
    releases.shift()!();
  });
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  await act(async () => {
    releases.shift()!();
  });
  await waitFor(() => expect(screen.getByText("row-2")).toBeTruthy());
  expect(read).toHaveBeenCalledTimes(2);
});

it.each([SupportedLanguage.English, SupportedLanguage.Korean])(
  "opt-in progress shows guidance only while another page exists and a distinct exhausted state afterward (%s)",
  async (language) => {
    await i18n.changeLanguage(language);
    const guidance = i18n.t("pagination.moreAvailable", { label: "items" }),
      exhausted = i18n.t("pagination.exhausted", { label: "items" });
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const read = vi.fn<PaginationReader<Row>>(async (token) => {
      if (token === "second") await gate;
      return {
        rows: [{ id: token || "first", revision: 1n }],
        nextPageToken: token === "second" ? "" : "second",
      };
    });
    renderValue(<List read={read} showProgress />);
    await screen.findByText("first");
    // A next page exists, so only guidance and the fallback control appear.
    expect(screen.queryByText(guidance)).not.toBeNull();
    expect(screen.queryByText(exhausted)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /items/ }));
    await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
    // The pending read neither offers guidance nor claims exhaustion.
    expect(screen.queryByText(guidance)).toBeNull();
    expect(screen.queryByText(exhausted)).toBeNull();
    await act(async () => {
      finish();
      await gate;
    });
    await screen.findByText(exhausted);
    expect(screen.queryByText(guidance)).toBeNull();
    expect(screen.queryByRole("button", { name: /items/ })).toBeNull();
  },
);
it("opt-in progress never claims exhaustion after a failed or empty-token-less read", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const read = vi.fn<PaginationReader<Row>>(async (token) => {
    if (token === "second") throw new ConnectError("failed", Code.Unavailable);
    return { rows: [{ id: "first", revision: 1n }], nextPageToken: "second" };
  });
  renderValue(<List read={read} showProgress />);
  await screen.findByText("first");
  fireEvent.click(screen.getByRole("button", { name: /Load more items/ }));
  await screen.findByRole("button", { name: "Retry" });
  expect(screen.queryByText("No further page of items is available.")).toBeNull();
  expect(screen.queryByText("Scroll to load more items.")).toBeNull();
});
