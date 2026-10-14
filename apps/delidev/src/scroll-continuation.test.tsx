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
import { ContinuationAxis, ScrollContinuation } from "./scroll-continuation";
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
  axis,
  showProgress = false,
}: {
  read: PaginationReader<Row>;
  active?: boolean;
  initialRead?: boolean;
  axis?: ContinuationAxis;
  showProgress?: boolean;
}) {
  const root = useRef<HTMLDivElement>(null);
  const horizontalTail = useRef<HTMLParagraphElement>(null);
  const reader = useMemo(() => read, [read]);
  const query = usePaginationChain("test-scope", active, reader, initialRead);
  return (
    <>
      <div ref={root}>
        <button type="button" onClick={query.reload}>
          Load
        </button>
        <button type="button" onClick={query.refreshExplicit}>
          Refresh
        </button>
        {query.rows.map((row, index) => (
          <p
            key={row.id}
            ref={
              axis === ContinuationAxis.Horizontal &&
              index === query.rows.length - 1
                ? horizontalTail
                : undefined
            }
          >
            {row.id}
          </p>
        ))}
      </div>
      <ScrollContinuation
        axis={axis}
        horizontalTail={
          axis === ContinuationAxis.Horizontal ? horizontalTail : undefined
        }
        query={query}
        label="items"
        root={root}
        active={active}
        showProgress={showProgress}
      />
    </>
  );
}
function horizontalViewport() {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(40);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(320);
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(
    function (this: HTMLElement) {
      return this.querySelectorAll("p").length * 100;
    },
  );
}
it("keeps horizontal continuation at the loaded tail across scroll and refresh", async () => {
  horizontalViewport();
  const read = vi.fn<PaginationReader<Row>>(async (token) => {
    const page = Number(token || 0);
    return {
      rows: Array.from({ length: 50 }, (_, index) => ({
        id: `terminal-${page * 50 + index}`,
        revision: 1n,
      })),
      nextPageToken: page < 19 ? String(page + 1) : "",
    };
  });
  const view = renderValue(
    <List read={read} axis={ContinuationAxis.Horizontal} />,
  );
  const strip = view.container.firstElementChild as HTMLElement;
  await screen.findByText("terminal-49");
  expect(read.mock.calls.map(([token]) => token)).toEqual([""]);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", ""]);

  strip.scrollLeft = strip.scrollWidth - strip.clientWidth - 96;
  fireEvent.scroll(strip);
  await screen.findByText("terminal-99");
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "", "1"]);
  fireEvent.scroll(strip);
  expect(read).toHaveBeenCalledTimes(3);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(5));
  expect(read.mock.calls.map(([token]) => token)).toEqual([
    "",
    "",
    "1",
    "",
    "1",
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Load more items" }));
  await screen.findByText("terminal-149");
  expect(read.mock.calls.at(-1)?.[0]).toBe("2");
});

it("fills an underfilled horizontal strip only until its loaded tail overflows", async () => {
  horizontalViewport();
  const read = vi.fn<PaginationReader<Row>>(async (token) => ({
    rows: Array.from({ length: token ? 4 : 1 }, (_, index) => ({
      id: `${token || "first"}-${index}`,
      revision: 1n,
    })),
    nextPageToken: token ? "third" : "second",
  }));
  const view = renderValue(
    <List read={read} axis={ContinuationAxis.Horizontal} />,
  );
  await screen.findByText("second-3");
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "second"]);
  fireEvent.scroll(view.container.firstElementChild!);
  expect(read).toHaveBeenCalledTimes(2);
});

it("measures the reached horizontal tail when synthetic children follow it", async () => {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(40);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(320);
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(
    function (this: HTMLElement) {
      return this.matches(".synthetic-tail-list") ? 700 : 0;
    },
  );
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.matches(".synthetic-tail-list")) {
        return {
          top: 0,
          bottom: 40,
          left: 0,
          right: 320,
          width: 320,
          height: 40,
          x: 0,
          y: 0,
          toJSON: () => ({}),
        };
      }
      if (this.tagName === "P") {
        const container = this.parentElement!;
        const index = [...container.querySelectorAll("p")].indexOf(
          this as HTMLParagraphElement,
        );
        const right = (index + 1) * 100 - container.scrollLeft;
        return {
          top: 0,
          bottom: 40,
          left: right - 100,
          right,
          width: 100,
          height: 40,
          x: right - 100,
          y: 0,
          toJSON: () => ({}),
        };
      }
      return {
        top: 0,
        bottom: 0,
        left: 0,
        right: 0,
        width: 0,
        height: 0,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      };
    },
  );

  function SyntheticTailList() {
    const root = useRef<HTMLDivElement>(null);
    const tail = useRef<HTMLParagraphElement>(null);
    const read = useMemo<PaginationReader<Row>>(
      () => async (token) => ({
        rows: Array.from({ length: 5 }, (_, index) => ({
          id: `${token || "first"}-${index}`,
          revision: 1n,
        })),
        nextPageToken: token ? "" : "next",
      }),
      [],
    );
    const query = usePaginationChain("synthetic-tail", true, read);
    return (
      <div className="synthetic-tail-list" ref={root}>
        {query.rows.map((row, index) => (
          <p
            key={row.id}
            ref={index === query.rows.length - 1 ? tail : undefined}
          >
            {row.id}
          </p>
        ))}
        <button type="button">Selected terminal</button>
        <ScrollContinuation
          axis={ContinuationAxis.Horizontal}
          horizontalTail={tail}
          query={query}
          label="items"
          root={root}
          active
        />
      </div>
    );
  }

  const view = renderValue(<SyntheticTailList />);
  const strip = view.container.querySelector<HTMLElement>(
    ".synthetic-tail-list",
  )!;
  await screen.findByText("first-4");
  expect(strip.scrollLeft).toBe(0);
  expect(screen.queryByText("next-4")).toBeNull();

  // Revealing the synthetic child leaves the reached history tail behind the
  // viewport edge, so it must not trigger another history read.
  strip.scrollLeft = 380;
  fireEvent.scroll(strip);
  await act(async () => {
    await Promise.resolve();
  });
  expect(screen.queryByText("next-4")).toBeNull();

  // Scrolling back until the reached history tail is at the edge resumes
  // automatic paging even though the synthetic child remains mounted after it.
  strip.scrollLeft = 180;
  fireEvent.scroll(strip);
  await screen.findByText("next-4");
});

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
  expect(
    screen.queryByText("No further page of items is available."),
  ).toBeNull();
  expect(screen.queryByText("Scroll to load more items.")).toBeNull();
});
