// SPDX-License-Identifier: Apache-2.0
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { document, encode, object } from "./documents";
import { SubagentRows, Subagents, retainSubagentLabels } from "./subagents";
import { subagentDisplayName, validateSubagentPage } from "./subagent-record";
import { subagentFixture } from "./subagent-test-fixture";

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function named(session: string, names: (string | undefined)[]) {
  const rows = names.map((name) => subagentFixture(session));
  const first = document(rows[0]);
  rows.forEach((row, index) => {
    const record = document(row);
    record.execution_id = first.execution_id;
    record.root_id = first.root_id;
    object(record.observation).parent_id = first.root_id;
    row.documentJson = encode(record);
    row.displayName = names[index] ?? "";
  });
  return rows;
}

function mount(session: string, rows: Resource[]) {
  const labels = new Map<string, number>();
  retainSubagentLabels(labels, validateSubagentPage(rows, session)!);
  return render(
    <SubagentRows
      rows={rows}
      sessionId={session}
      technical={false}
      labels={labels}
    />,
  );
}

const advance = (ms: number) =>
  act(() => {
    vi.advanceTimersByTime(ms);
  });

test("lists only names: five exact native names, equal names stay separate and one fallback ordinal", () => {
  const session = newRequestId();
  const rows = named(session, [
    "Atlas",
    "Nova",
    "Nova",
    undefined,
    "Sage",
    "Iris",
  ]);
  const view = mount(session, rows);
  const items = within(view.container).getAllByRole("listitem");
  expect(items.map((item) => item.textContent)).toEqual([
    "Atlas",
    "Nova",
    "Nova",
    "Subagent 4",
    "Sage",
    "Iris",
  ]);
  // Idle Info exposes no table, controls, output, usage, status or paragraph.
  expect(view.container.querySelector("table")).toBeNull();
  expect(view.container.querySelector("p")).toBeNull();
  expect(screen.queryAllByRole("button")).toHaveLength(0);
  expect(screen.queryByText("Original child output")).toBeNull();
  expect(screen.queryByText(/Observed:/)).toBeNull();
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("invalid or oversized names use the stable ordinal without invalidating the child", () => {
  const session = newRequestId();
  const limit = "가".repeat(256);
  const rows = named(session, [
    "  ",
    "a".repeat(257),
    "line\nbreak",
    limit,
    "Plain",
  ]);
  const view = mount(session, rows);
  expect(
    within(view.container)
      .getAllByRole("listitem")
      .map((item) => item.textContent),
  ).toEqual(["Subagent 1", "Subagent 2", "Subagent 3", limit, "Plain"]);
  expect(subagentDisplayName("Atlas")).toBe("Atlas");
  expect(subagentDisplayName(undefined)).toBeUndefined();
  expect(subagentDisplayName("a\u0000b")).toBeUndefined();
  expect(subagentDisplayName("\ud800")).toBeUndefined();
});

test("hover opens details only after 300ms and a pointer move into the card keeps it within the 150ms grace", () => {
  const session = newRequestId();
  const view = mount(session, named(session, ["Atlas", "Nova"]));
  const trigger = within(view.container).getByText("Nova");
  fireEvent.pointerEnter(trigger);
  advance(299);
  expect(screen.queryByRole("tooltip")).toBeNull();
  advance(1);
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Nova")).toBeTruthy();
  expect(within(card).getByText("Root session")).toBeTruthy();
  expect(within(card).getByText("running")).toBeTruthy();
  expect(within(card).getByText("Observed: observed-model")).toBeTruthy();
  expect(within(card).getByText("Requested: requested-only")).toBeTruthy();
  expect(within(card).getByText("Partial native output")).toBeTruthy();
  expect(within(card).getByText("Original child output")).toBeTruthy();
  expect(within(card).getByText("Unavailable")).toBeTruthy();
  expect(trigger.getAttribute("aria-describedby")).toBe(card.id);
  // The card is noninteractive: no focusable action or form control.
  expect(card.querySelectorAll("button, a, input, [tabindex]")).toHaveLength(0);
  fireEvent.pointerLeave(trigger);
  advance(100);
  fireEvent.pointerEnter(card);
  advance(500);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.pointerLeave(card);
  advance(149);
  expect(screen.getByRole("tooltip")).toBe(card);
  advance(1);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(trigger.getAttribute("aria-describedby")).toBeNull();
});

test("keyboard focus opens immediately, Escape dismisses until re-entry and outside pointer closes", () => {
  const session = newRequestId();
  const view = mount(session, named(session, ["Atlas"]));
  const trigger = within(view.container).getByText("Atlas");
  act(() => trigger.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(globalThis.document, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();
  act(() => trigger.blur());
  act(() => trigger.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  // A touch tap focuses the same trigger; an outside tap dismisses it.
  fireEvent.pointerDown(globalThis.document.body);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test.each(["scroll", "resize"])(
  "external %s dismisses the card",
  (kind) => {
    const session = newRequestId();
    const view = mount(session, named(session, ["Atlas"]));
    act(() => within(view.container).getByText("Atlas").focus());
    expect(screen.getByRole("tooltip")).toBeTruthy();
    if (kind === "scroll") fireEvent.scroll(globalThis.document.body);
    else fireEvent(window, new Event("resize"));
    expect(screen.queryByRole("tooltip")).toBeNull();
  },
);

test("source removal and page eviction dismiss and dispose the card payload", () => {
  const session = newRequestId();
  const rows = named(session, ["Atlas", "Nova"]);
  const labels = new Map<string, number>();
  retainSubagentLabels(labels, validateSubagentPage(rows, session)!);
  const view = render(
    <SubagentRows
      rows={rows}
      sessionId={session}
      technical={false}
      labels={labels}
    />,
  );
  act(() => within(view.container).getByText("Atlas").focus());
  expect(screen.getByText("Original child output")).toBeTruthy();
  view.rerender(
    <SubagentRows
      rows={[rows[1]]}
      sessionId={session}
      technical={false}
      labels={labels}
    />,
  );
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(screen.queryByText("Original child output")).toBeNull();
  act(() => within(view.container).getByText("Nova").focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  view.unmount();
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("switching sessions dismisses the card and disposes its payload", () => {
  const session = newRequestId(),
    other = newRequestId();
  const rows = named(session, ["Atlas"]);
  const view = mount(session, rows);
  act(() => within(view.container).getByText("Atlas").focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  const next = named(other, ["Nova"]);
  view.rerender(
    <SubagentRows rows={next} sessionId={other} technical={false} />,
  );
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(screen.queryByText("Original child output")).toBeNull();
});

test("exact usage keeps its unavailable and partial markers inside the card", () => {
  const session = newRequestId();
  const [row] = named(session, ["Atlas"]);
  const record = document(row);
  const child = object(record.observation);
  child.usage = {
    scope: "child-cumulative",
    total: "9223372036854775807",
    input: "0",
    output: "0",
    native_report:
      '{"total":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":9223372036854775807},"last":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":0},"modelContextWindow":null}',
  };
  (record.sources as Record<string, unknown>[])[0].usage = child.usage;
  child.observed_model = null;
  row.documentJson = encode(record);
  const view = mount(session, [row]);
  act(() => within(view.container).getByText("Atlas").focus());
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Total: 9223372036854775807")).toBeTruthy();
  expect(within(card).getByText("Input: 0")).toBeTruthy();
  expect(within(card).getByText("Output: 0")).toBeTruthy();
  expect(within(card).getByText("Observed: Unavailable")).toBeTruthy();
});

test("opening and dismissing cards sends no additional request or mutation", async () => {
  vi.useRealTimers();
  const session = newRequestId();
  const rows = named(session, ["Atlas", "Nova"]);
  let reads = 0;
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SUBAGENT_OBSERVATION_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: () => {
        reads++;
        return { resources: rows };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <Subagents sessionId={session} revision="1" />
      </TransportProvider>
    </QueryClientProvider>,
  );
  const atlas = await screen.findByText("Atlas");
  await waitFor(() => expect(reads).toBeGreaterThan(0));
  const before = reads;
  act(() => atlas.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(globalThis.document, { key: "Escape" });
  act(() => (screen.getByText("Nova") as HTMLElement).focus());
  fireEvent.pointerDown(globalThis.document.body);
  expect(reads).toBe(before);
  expect(screen.queryAllByRole("button")).toHaveLength(0);
  view.unmount();
  client.clear();
});

test("cards prefer the left of the rail, flip and clamp inside an 8px viewport inset", () => {
  const session = newRequestId();
  const view = mount(session, named(session, ["Atlas"]));
  const trigger = within(view.container).getByText("Atlas");
  const rect = (left: number, top: number, width: number, height: number) =>
    ({
      left,
      top,
      width,
      height,
      right: left + width,
      bottom: top + height,
      x: left,
      y: top,
      toJSON: () => ({}),
    }) as DOMRect;
  let anchor = rect(1000, 100, 360, 32);
  const original = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function () {
    if (this === trigger) return anchor;
    if ((this as HTMLElement).getAttribute("role") === "tooltip")
      return rect(0, 0, 320, 200);
    return original.call(this);
  };
  Object.defineProperty(window, "innerWidth", {
    value: 1400,
    configurable: true,
  });
  Object.defineProperty(window, "innerHeight", {
    value: 700,
    configurable: true,
  });
  try {
    act(() => trigger.focus());
    let card = screen.getByRole("tooltip");
    expect(card.style.left).toBe("672px");
    expect(card.style.top).toBe("100px");
    // Not enough room on the left: flip to the right side of the source.
    act(() => trigger.blur());
    advance(200);
    anchor = rect(100, 650, 360, 32);
    act(() => trigger.focus());
    card = screen.getByRole("tooltip");
    expect(card.style.left).toBe("468px");
    expect(card.style.top).toBe("492px");
  } finally {
    Element.prototype.getBoundingClientRect = original;
  }
});

test("long unbroken names and card content wrap at the 360px rail", () => {
  const css = readFileSync(join(process.cwd(), "src/session.css"), "utf8");
  const rule = (selector: string) =>
    css.slice(css.indexOf(`${selector} {`), css.indexOf("}", css.indexOf(`${selector} {`)));
  expect(rule(".subagent-name")).toContain("overflow-wrap: anywhere");
  expect(rule(".subagent-name")).toContain("min-height: 32px");
  expect(rule(".subagent-name")).toContain("font-size: 14px");
  const styles = readFileSync(join(process.cwd(), "src/styles.css"), "utf8");
  const card = styles.slice(
    styles.indexOf(".sidebar-session-tooltip {"),
    styles.indexOf("}", styles.indexOf(".sidebar-session-tooltip {")),
  );
  expect(card).toContain("width: 320px");
  expect(card).toContain("padding: 16px");
  expect(card).toContain("border-radius: 8px");
  expect(card).toContain("overflow: auto");
  expect(card).toContain("overflow-wrap: anywhere");
});
