// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ScrollPayloadWindow, type PayloadWindowQuery } from "./scroll-payload-window";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";

type Row = { id: string; revision: bigint; text: string };
const a = { id: "a", revision: 1n, text: "Original A" }, b = { id: "b", revision: 1n, text: "Original B" }, newer = { ...a, revision: 2n, text: "Updated A" }, c = { id: "c", revision: 1n, text: "Original C" };
function View({ query }: { query: PayloadWindowQuery<Row, Row> }) {
  const root = useRef<HTMLDivElement>(null);
  return <div ref={root}><ScrollPayloadWindow query={query} root={root} active identity={paginationIdentity} revision={paginationRevision}>{payload => payload.map(row => <p key={row.id}>{row.text}</p>)}</ScrollPayloadWindow></div>;
}
it("deduplicates full payloads at the first server position while showing the highest revision", () => {
  const query = { pages: [{ token: "", nextPageToken: "next", rows: [a, b] }, { token: "next", nextPageToken: "", rows: [newer, c] }], payloadPages: [{ token: "", payload: [a, b] }, { token: "next", payload: [newer, c] }], measure: vi.fn(), restore: vi.fn() };
  const view = render(<View query={query} />);
  expect([...view.container.querySelectorAll("p")].map(node => node.textContent)).toEqual(["Updated A", "Original B", "Original C"]);
  expect(screen.queryByText("Original A")).toBeNull();
});
it("keeps measured prior content reachable by an explicit exact-token restoration", () => {
  const query = { pages: [{ token: "", nextPageToken: "next", rows: [a], height: 132 }, { token: "next", nextPageToken: "", rows: [b] }], payloadPages: [{ token: "next", payload: [b] }], measure: vi.fn(), restore: vi.fn() };
  const view = render(<View query={query} />);
  const placeholder = view.container.querySelector<HTMLElement>("[data-payload-page]")!;
  expect(placeholder.style.minHeight).toBe("132px");
  expect(query.restore).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Restore previously loaded items" }));
  expect(query.restore).toHaveBeenCalledExactlyOnceWith("");
});
it("protects the focused page until focus leaves its full payload", () => {
  const query = { pages: [{ token: "", nextPageToken: "", rows: [a] }], payloadPages: [{ token: "", payload: [a] }], measure: vi.fn(), restore: vi.fn(), protect: vi.fn() };
  const root = { current: null as HTMLDivElement | null };
  render(<div ref={root}><ScrollPayloadWindow query={query} root={root} active identity={paginationIdentity}>{() => <button>Original action</button>}</ScrollPayloadWindow></div>);
  const action = screen.getByRole("button", { name: "Original action" });
  fireEvent.focus(action); expect(query.protect).toHaveBeenLastCalledWith("");
  fireEvent.blur(action); expect(query.protect).toHaveBeenLastCalledWith();
});
it("preserves connected focus when a restored form has automatic focus", () => {
  const root = { current: null as HTMLDivElement | null };
  const query = { pages: [{ token: "", nextPageToken: "", rows: [a] }], payloadPages: [] as { token: string; payload: Row[] }[], measure: vi.fn(), restore: vi.fn() };
  const content = (value: typeof query) => <div ref={root}><input aria-label="Composer" /><ScrollPayloadWindow query={value} root={root} active>{() => <input aria-label="Restored form" autoFocus />}</ScrollPayloadWindow></div>;
  const view = render(content(query));
  const composer = screen.getByRole("textbox", { name: "Composer" }); composer.focus();
  view.rerender(content({ ...query, payloadPages: [{ token: "", payload: [a] }] }));
  expect(document.activeElement).toBe(composer);
});
