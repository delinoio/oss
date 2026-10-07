// SPDX-License-Identifier: Apache-2.0
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useMemo, useRef, type ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { ScrollContinuation } from "./scroll-continuation";
import { usePaginationChain } from "./scroll-pagination-query";
import { type PaginationReader } from "./scroll-pagination";

type Row = { id: string; revision: bigint };
afterEach(() => vi.restoreAllMocks());
function List({ read, active = true, initialRead = true }: { read: PaginationReader<Row>; active?: boolean; initialRead?: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  const reader = useMemo(() => read, [read]);
  const query = usePaginationChain("test-scope", active, reader, initialRead);
  return <div ref={root}><button type="button" onClick={query.reload}>Load</button>{query.rows.map(row => <p key={row.id}>{row.id}</p>)}<ScrollContinuation query={query} label="items" root={root} active={active} /></div>;
}
function renderValue(value: ReactNode) { return render(<QueryClientProvider client={new QueryClient()}>{value}</QueryClientProvider>); }
function viewport() {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(400);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 0, bottom: 300, left: 0, right: 200, width: 200, height: 300, x: 0, y: 0, toJSON: () => ({}) });
}
it("fills an underfilled viewport serially and stops at exhaustion", async () => {
  viewport(); let concurrent = 0, max = 0;
  const read = vi.fn<PaginationReader<Row>>(async token => {
    max = Math.max(max, ++concurrent); await Promise.resolve(); concurrent--;
    return { rows: [{ id: token || "first", revision: 1n }], nextPageToken: token === "second" ? "" : "second" };
  });
  renderValue(<List read={read} />);
  await screen.findByText("second");
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "second"]);
  expect(max).toBe(1);
  expect(screen.getByText("first")).toBeTruthy();
  expect(screen.getByText("All loaded items are shown.")).toBeTruthy();
});
it("never starts an explicit read on mount or scroll", async () => {
  viewport(); const read = vi.fn<PaginationReader<Row>>(async () => ({ rows: [], nextPageToken: "" }));
  const view = renderValue(<List read={read} initialRead={false} />);
  fireEvent.scroll(view.container.firstElementChild!);
  await act(async () => { await Promise.resolve(); });
  expect(read).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Load" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
});
it("retains accepted data after continuation failure and requires an explicit exact retry", async () => {
  viewport(); const read = vi.fn<PaginationReader<Row>>(async token => {
    if (token) throw new ConnectError("offline", Code.Unavailable);
    return { rows: [{ id: "accepted", revision: 1n }], nextPageToken: "exact-request" };
  });
  const view = renderValue(<List read={read} />);
  await screen.findByRole("button", { name: "Retry" });
  fireEvent.scroll(view.container.firstElementChild!); fireEvent.scroll(view.container.firstElementChild!);
  expect(read).toHaveBeenCalledTimes(2);
  expect(screen.getByText("accepted")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(read.mock.calls.map(([token]) => token)).toEqual(["", "exact-request", "exact-request"]);
});
it("suppresses automatic reads when inactive, document-hidden or under a closed disclosure", async () => {
  viewport(); const read = vi.fn<PaginationReader<Row>>(async () => ({ rows: [], nextPageToken: "next" }));
  const inactive = renderValue(<List read={read} active={false} />);
  await act(async () => { await Promise.resolve(); }); expect(read).not.toHaveBeenCalled(); inactive.unmount();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  const hidden = renderValue(<List read={read} />);
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await act(async () => { await Promise.resolve(); }); expect(read).toHaveBeenCalledTimes(1); hidden.unmount();
  vi.restoreAllMocks(); viewport(); read.mockClear();
  renderValue(<details><summary>Closed</summary><List read={read} /></details>);
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await act(async () => { await Promise.resolve(); }); expect(read).toHaveBeenCalledTimes(1);
});
