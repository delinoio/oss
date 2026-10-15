// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  PayloadPageState,
  ScrollPayloadWindow,
  type PayloadWindowQuery,
} from "./scroll-payload-window";
import { orderedPageEntries, PageEntryKind } from "./interaction-presentation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";

type Row = { id: string; revision: bigint; text: string };
const a = { id: "a", revision: 1n, text: "Original A" },
  b = { id: "b", revision: 1n, text: "Original B" },
  newer = { ...a, revision: 2n, text: "Updated A" },
  c = { id: "c", revision: 1n, text: "Original C" };
function View({ query }: { query: PayloadWindowQuery<Row, Row> }) {
  const root = useRef<HTMLDivElement>(null);
  return (
    <div ref={root}>
      <ScrollPayloadWindow
        query={query}
        root={root}
        active
        identity={paginationIdentity}
        revision={paginationRevision}
      >
        {(payload) => payload.map((row) => <p key={row.id}>{row.text}</p>)}
      </ScrollPayloadWindow>
    </div>
  );
}
it("deduplicates full payloads at the first server position while showing the highest revision", () => {
  const query = {
    pages: [
      { token: "", nextPageToken: "next", rows: [a, b] },
      { token: "next", nextPageToken: "", rows: [newer, c] },
    ],
    payloadPages: [
      { token: "", payload: [a, b] },
      { token: "next", payload: [newer, c] },
    ],
    measure: vi.fn(),
    restore: vi.fn(),
  };
  const view = render(<View query={query} />);
  expect(
    [...view.container.querySelectorAll("p")].map((node) => node.textContent),
  ).toEqual(["Updated A", "Original B", "Original C"]);
  expect(screen.queryByText("Original A")).toBeNull();
});
it("keeps measured prior content reachable by an explicit exact-token restoration", () => {
  const query = {
    pages: [
      { token: "", nextPageToken: "next", rows: [a], height: 132 },
      { token: "next", nextPageToken: "", rows: [b] },
    ],
    payloadPages: [{ token: "next", payload: [b] }],
    measure: vi.fn(),
    restore: vi.fn(),
  };
  const view = render(<View query={query} />);
  const placeholder = view.container.querySelector<HTMLElement>(
    "[data-payload-page]",
  )!;
  expect(placeholder.style.minHeight).toBe("132px");
  expect(query.restore).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Restore previously loaded items" }),
  );
  expect(query.restore).toHaveBeenCalledExactlyOnceWith("");
});
it("protects the focused page until focus leaves its full payload", () => {
  const query = {
    pages: [{ token: "", nextPageToken: "", rows: [a] }],
    payloadPages: [{ token: "", payload: [a] }],
    measure: vi.fn(),
    restore: vi.fn(),
    protect: vi.fn(),
  };
  const root = { current: null as HTMLDivElement | null };
  render(
    <div ref={root}>
      <ScrollPayloadWindow
        query={query}
        root={root}
        active
        identity={paginationIdentity}
      >
        {() => <button>Original action</button>}
      </ScrollPayloadWindow>
    </div>,
  );
  const action = screen.getByRole("button", { name: "Original action" });
  fireEvent.focus(action);
  expect(query.protect).toHaveBeenLastCalledWith("");
  fireEvent.blur(action);
  expect(query.protect).toHaveBeenLastCalledWith();
});
it("preserves connected focus when a restored form has automatic focus", () => {
  const root = { current: null as HTMLDivElement | null };
  const query = {
    pages: [{ token: "", nextPageToken: "", rows: [a] }],
    payloadPages: [] as { token: string; payload: Row[] }[],
    measure: vi.fn(),
    restore: vi.fn(),
  };
  const content = (value: typeof query) => (
    <div ref={root}>
      <input aria-label="Composer" />
      <ScrollPayloadWindow query={value} root={root} active>
        {() => <input aria-label="Restored form" autoFocus />}
      </ScrollPayloadWindow>
    </div>
  );
  const view = render(content(query));
  const composer = screen.getByRole("textbox", { name: "Composer" });
  composer.focus();
  view.rerender(
    content({ ...query, payloadPages: [{ token: "", payload: [a] }] }),
  );
  expect(document.activeElement).toBe(composer);
});

type Query = PayloadWindowQuery<Row, Row>;
const pageRow = (id: string): Row => ({ id, revision: 1n, text: id });
const pageRows = [
  [pageRow("q1"), pageRow("r1")],
  [pageRow("q2"), pageRow("r2")],
  [pageRow("q3"), pageRow("r3")],
  [pageRow("q4"), pageRow("r4")],
];
const fourPages = (resident: number[]): Query => ({
  pages: pageRows.map((rows, index) => ({
    token: `page-${index}`,
    nextPageToken: index < 3 ? `page-${index + 1}` : "",
    rows,
  })),
  payloadPages: resident.map((index) => ({
    token: `page-${index}`,
    payload: pageRows[index],
  })),
  measure: vi.fn(),
  restore: vi.fn(),
  protect: vi.fn(),
});
// Mirrors the session request tray: receipts replace evicted question rows at
// their retained row position and resident rows render as normal forms.
function Tray({ query, receipts }: { query: Query; receipts: string[] }) {
  const root = useRef<HTMLDivElement>(null);
  const known = new Map(receipts.map((id) => [id, id]));
  return (
    <div ref={root}>
      <ScrollPayloadWindow
        query={query}
        root={root}
        active
        identity={paginationIdentity}
        revision={paginationRevision}
        projected={(rows, payload) =>
          orderedPageEntries(
            rows.map((row) => row.id),
            payload ?? [],
            known,
          ).map((entry) =>
            entry.kind === PageEntryKind.Receipt ? (
              <button key={entry.id}>{`Receipt ${entry.id}`}</button>
            ) : (
              <p key={entry.id}>{`Form ${entry.id}`}</p>
            ),
          )
        }
      />
    </div>
  );
}
const restore = "Restore previously loaded items";
const labels = (container: HTMLElement) =>
  [...container.querySelectorAll("p, button")].map(
    (node) => node.textContent ?? "",
  );
it("keeps an evicted older receipt before newer page requests", () => {
  // Page one lost its payload: its receipt keeps page one's position and the
  // resident later pages follow it.
  const view = render(<Tray query={fourPages([1, 2, 3])} receipts={["q1"]} />);
  expect(labels(view.container)).toEqual([
    "Receipt q1",
    restore,
    "Form q2",
    "Form r2",
    "Form q3",
    "Form r3",
    "Form q4",
    "Form r4",
  ]);
});
it("follows retained page and row order regardless of receipt observation order", () => {
  const view = render(
    <Tray query={fourPages([2, 3])} receipts={["q2", "q1", "r1"]} />,
  );
  expect(labels(view.container)).toEqual([
    "Receipt q1",
    "Receipt r1",
    restore,
    "Receipt q2",
    restore,
    "Form q3",
    "Form r3",
    "Form q4",
    "Form r4",
  ]);
});
it("renders one receipt per original question across restore and eviction cycles", () => {
  const view = render(<Tray query={fourPages([1, 2, 3])} receipts={["q1"]} />);
  const expected = (restored: boolean) =>
    restored
      ? ["Form q1", "Form r1", "Form q2", "Form r2", "Form q3", "Form r3"]
      : ["Receipt q1", restore, "Form q2", "Form r2", "Form q3", "Form r3"];
  for (let cycle = 0; cycle < 3; cycle++) {
    // Restored page one has a resident form and no receipt; page four is evicted.
    view.rerender(<Tray query={fourPages([0, 1, 2])} receipts={[]} />);
    expect(labels(view.container)).toEqual([...expected(true), restore]);
    // Evicting page one again restores the same single receipt at its position.
    view.rerender(<Tray query={fourPages([1, 2, 3])} receipts={["q1"]} />);
    expect(
      labels(view.container).filter((value) => value === "Receipt q1"),
    ).toHaveLength(1);
    expect(labels(view.container).slice(0, 6)).toEqual(expected(false));
  }
});
it("protects a focused receipt page while the control keeps focus", () => {
  const query = fourPages([1, 2, 3]);
  const view = render(<Tray query={query} receipts={["q1"]} />);
  const receipt = screen.getByRole("button", { name: "Receipt q1" });
  fireEvent.focus(receipt);
  expect(query.protect).toHaveBeenLastCalledWith("page-0");
  fireEvent.blur(receipt);
  expect(query.protect).toHaveBeenLastCalledWith();
  expect(labels(view.container)[0]).toBe("Receipt q1");
});

function providerWindow(
  overrides: Partial<PayloadWindowQuery<Row, Row>> = {},
  heights: Record<string, number> = { "": 1200 },
) {
  const rows = ["a", "b", "c", "d"].map((id) => ({
    id,
    revision: 1n,
    text: `Provider ${id}`,
  }));
  const pages = rows.map((row, index) => ({
    token: index === 0 ? "" : `page-${index}`,
    nextPageToken: index === 3 ? "" : `page-${index + 1}`,
    rows: [row],
    height: heights[index === 0 ? "" : `page-${index}`],
  }));
  const query: PayloadWindowQuery<Row, Row> = {
    pages,
    // Only the newest three pages stay resident; page one is a placeholder.
    payloadPages: pages.slice(1).map((page) => ({
      token: page.token,
      payload: page.rows,
    })),
    measure: vi.fn(),
    restore: vi.fn(),
    ...overrides,
  };
  function Picker() {
    const root = useRef<HTMLDivElement>(null);
    return (
      <div ref={root}>
        <div className="account-provider-choices">
          <ScrollPayloadWindow query={query} root={root} active>
            {(payload) =>
              payload.map((row) => (
                <button className="account-provider-action" key={row.id}>
                  {row.text}
                </button>
              ))
            }
          </ScrollPayloadWindow>
        </div>
      </div>
    );
  }
  return { query, view: render(<Picker />) };
}

it("marks resident wrappers and measured placeholders with their closed state", () => {
  const { view } = providerWindow();
  const wrappers = [
    ...view.container.querySelectorAll<HTMLElement>("[data-payload-page]"),
  ];
  expect(wrappers.map((node) => node.dataset.payloadState)).toEqual([
    PayloadPageState.Placeholder,
    PayloadPageState.Resident,
    PayloadPageState.Resident,
    PayloadPageState.Resident,
  ]);
  expect(wrappers.map((node) => node.style.minHeight)).toEqual([
    "1200px",
    "",
    "",
    "",
  ]);
});

it("renders a retained page with a large previous height as one content-sized Restore control", () => {
  const { query, view } = providerWindow({}, { "": 1800 });
  const placeholder = view.container.querySelector<HTMLElement>(
    '[data-payload-state="placeholder"]',
  )!;
  expect(placeholder.style.minHeight).toBe("1800px");
  expect(placeholder.parentElement).toBe(
    view.container.querySelector(".account-provider-choices"),
  );
  // The Restore control is the wrapper's only content: the wrapper's
  // stylesheet rule must size it to its content instead of the min-height.
  expect(placeholder.children).toHaveLength(1);
  const button = placeholder.firstElementChild as HTMLButtonElement;
  expect(button.tagName).toBe("BUTTON");
  expect(button.textContent).toBe(restore);
  expect(button.style.height).toBe("");
  expect(button.style.minHeight).toBe("");
  fireEvent.click(button);
  expect(query.restore).toHaveBeenCalledExactlyOnceWith("");
});

it.each([
  { label: "loading", overrides: { loading: true } },
  { label: "failed", overrides: { error: new Error("read failed") } },
])(
  "keeps the same placeholder layout while Restore is disabled during a $label read",
  ({ overrides }) => {
    const { query, view } = providerWindow(overrides);
    const placeholder = view.container.querySelector<HTMLElement>(
      '[data-payload-state="placeholder"]',
    )!;
    expect(placeholder.style.minHeight).toBe("1200px");
    expect(placeholder.children).toHaveLength(1);
    const button = screen.getByRole("button", { name: restore });
    expect(placeholder.contains(button)).toBe(true);
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
    expect(query.restore).not.toHaveBeenCalled();
  },
);

it("falls back to the minimum placeholder height before a page was measured", () => {
  const { view } = providerWindow({}, {});
  expect(
    view.container.querySelector<HTMLElement>(
      '[data-payload-state="placeholder"]',
    )!.style.minHeight,
  ).toBe("48px");
});
