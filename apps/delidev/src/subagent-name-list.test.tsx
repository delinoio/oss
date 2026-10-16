// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { readFileSync } from "node:fs";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ResourceService,
  SubagentDisplayNameSchema,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { afterEach, expect, test, vi } from "vitest";
import { document as readDocument, encode, object } from "./documents";
import { Subagents, SubagentRows, retainSubagentLabels } from "./subagents";
import {
  subagentDisplayName,
  subagentRowDisplayName,
  validateSubagentPage,
} from "./subagent-record";
import {
  openCodeSubagentFixture,
  openSubagentCard,
  subagentFixture,
  subagentTriggers,
  withSubagentName,
} from "./subagent-test-fixture";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function advance(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

function labelled(session: string, rows: ReturnType<typeof subagentFixture>[]) {
  const labels = new Map<string, number>();
  retainSubagentLabels(labels, validateSubagentPage(rows, session)!);
  return labels;
}

function sharedExecution(rows: ReturnType<typeof subagentFixture>[]) {
  const first = readDocument(rows[0]!);
  for (const row of rows) {
    const record = readDocument(row);
    record.execution_id = first.execution_id;
    record.root_id = first.root_id;
    object(record.observation).parent_id = first.root_id;
    row.documentJson = encode(record);
  }
}

test("lists one name per child, keeps equal names separate and falls back to stable ordinals", () => {
  const session = newRequestId();
  const rows = Array.from({ length: 6 }, () => subagentFixture(session));
  sharedExecution(rows);
  const names = ["Atlas", "Nova", "Nova", "Sage", "Iris"];
  names.forEach((name, index) => withSubagentName(rows[index]!, name));
  render(
    <SubagentRows
      rows={rows}
      sessionId={session}
      labels={labelled(session, rows)}
    />,
  );
  const items = screen.getAllByRole("listitem");
  expect(items.map((item) => item.textContent)).toEqual([
    "Atlas",
    "Nova",
    "Nova",
    "Sage",
    "Iris",
    "Subagent 6",
  ]);
  // Idle rows expose neither details nor controls nor the old explanation.
  for (const unwanted of [
    "Parent",
    "Status",
    "Model",
    "Recent output",
    "Original child output",
    "Observed: observed-model",
  ])
    expect(screen.queryByText(new RegExp(unwanted))).toBeNull();
  expect(screen.queryByRole("table")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(screen.getByRole("list").tagName).toBe("UL");
  expect(subagentTriggers().every((node) => node.tabIndex === 0)).toBe(true);
});

test.each([
  ["blank", "   "],
  ["empty", ""],
  ["oversized", "가".repeat(257)],
  ["control", "At\nlas"],
])(
  "%s names use the localized ordinal without invalidating the child",
  (_, name) => {
    const session = newRequestId(),
      row = subagentFixture(session);
    withSubagentName(row, name);
    expect(subagentDisplayName(row)).toBeUndefined();
    expect(validateSubagentPage([row], session)).toHaveLength(1);
    render(
      <SubagentRows
        rows={[row]}
        sessionId={session}
        labels={labelled(session, [row])}
      />,
    );
    expect(screen.getByRole("listitem").textContent).toBe("Subagent 1");
  },
);

test("accepts a 256-character name and rejects a foreign child join", () => {
  const session = newRequestId(),
    row = subagentFixture(session);
  withSubagentName(row, "가".repeat(256));
  expect(subagentDisplayName(row)).toBe("가".repeat(256));
  row.subagentDisplay = create(SubagentDisplayNameSchema, {
    childId: newRequestId(),
    name: "Atlas",
  });
  expect(subagentDisplayName(row)).toBeUndefined();
  row.subagentDisplay = undefined;
  expect(subagentDisplayName(row)).toBeUndefined();
});

test("opens after 300ms of pointer hover, bridges to the card and closes after the 150ms grace", () => {
  vi.useFakeTimers();
  const session = newRequestId(),
    row = withSubagentName(subagentFixture(session), "Nova");
  render(<SubagentRows rows={[row]} sessionId={session} />);
  const trigger = screen.getByText("Nova");
  fireEvent.pointerEnter(trigger);
  advance(299);
  expect(screen.queryByRole("tooltip")).toBeNull();
  advance(1);
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Original child output")).toBeTruthy();
  expect(trigger.getAttribute("aria-describedby")).toBe(card.id);
  expect(card.querySelector("button, a, input, [tabindex]")).toBeNull();
  fireEvent.pointerLeave(trigger);
  advance(149);
  fireEvent.pointerEnter(card);
  advance(1000);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.pointerLeave(card);
  advance(149);
  expect(screen.getByRole("tooltip")).toBe(card);
  advance(1);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("opens immediately on keyboard focus and Escape suppresses it until re-entry", () => {
  vi.useFakeTimers();
  const session = newRequestId(),
    row = withSubagentName(subagentFixture(session), "Nova");
  render(<SubagentRows rows={[row]} sessionId={session} />);
  const trigger = screen.getByText("Nova");
  act(() => trigger.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(trigger, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();
  // Movement over the source is not re-entry, so the card stays suppressed.
  fireEvent.pointerMove(trigger);
  advance(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(trigger);
  advance(300);
  expect(screen.getByRole("tooltip")).toBeTruthy();
});

test("dismisses on outside pointer, scroll outside the card and resize but not inside the card", () => {
  const session = newRequestId(),
    row = withSubagentName(subagentFixture(session), "Nova");
  render(
    <>
      <p>Outside</p>
      <SubagentRows rows={[row]} sessionId={session} />
    </>,
  );
  const trigger = screen.getByText("Nova");
  fireEvent.focus(trigger);
  let card = screen.getByRole("tooltip");
  fireEvent.pointerDown(card);
  fireEvent.scroll(card);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.pointerDown(screen.getByText("Outside"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.blur(trigger);
  fireEvent.focus(trigger);
  card = screen.getByRole("tooltip");
  fireEvent.scroll(globalThis.document);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.blur(trigger);
  fireEvent.focus(trigger);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent(window, new Event("resize"));
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("source removal, session switch and disabled ownership dismiss the card", () => {
  const session = newRequestId(),
    other = newRequestId();
  const one = withSubagentName(subagentFixture(session), "Nova"),
    two = withSubagentName(subagentFixture(session), "Orion");
  const view = (rows: (typeof one)[], id = session, enabled = true) => (
    <SubagentRows rows={rows} sessionId={id} enabled={enabled} />
  );
  const mounted = render(view([one, two]));
  openSubagentCard(0);
  mounted.rerender(view([two]));
  expect(screen.queryByRole("tooltip")).toBeNull();
  openSubagentCard(0);
  mounted.rerender(view([two], session, false));
  expect(screen.queryByRole("tooltip")).toBeNull();
  mounted.rerender(view([two]));
  openSubagentCard(0);
  const next = subagentFixture(other);
  mounted.rerender(view([withSubagentName(next, "Orion")], other));
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("opening a card sends no request and Subagents has no child controls", async () => {
  const session = newRequestId();
  const row = withSubagentName(subagentFixture(session), "Nova");
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
        return { resources: [row] };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <Subagents sessionId={session} revision="1" />
      </TransportProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(subagentTriggers()).toHaveLength(1));
  expect(screen.queryByText(/Read-only native hierarchy/)).toBeNull();
  await waitFor(() => expect(reads).toBeGreaterThan(0));
  const before = reads;
  fireEvent.focus(subagentTriggers()[0]!);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(subagentTriggers()[0]!, { key: "Escape" });
  expect(reads).toBe(before);
  expect(screen.queryAllByRole("button")).toHaveLength(0);
  client.clear();
});

test("prefers the left of the rail, flips right and clamps inside the viewport", () => {
  const session = newRequestId(),
    row = withSubagentName(subagentFixture(session), "Nova");
  render(<SubagentRows rows={[row]} sessionId={session} />);
  const trigger = screen.getByText("Nova");
  vi.stubGlobal("innerWidth", 1280);
  vi.stubGlobal("innerHeight", 700);
  const rect = (left: number, top: number, width: number, height: number) => ({
    x: left,
    y: top,
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    toJSON: () => ({}),
  });
  let source = rect(920, 100, 340, 32);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return this === trigger
        ? source
        : this.classList.contains("sidebar-session-tooltip")
          ? rect(0, 0, 320, 240)
          : rect(0, 0, 0, 0);
    },
  );
  fireEvent.focus(trigger);
  let card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("592px");
  expect(card.style.top).toBe("100px");
  fireEvent.blur(trigger);
  fireEvent(window, new Event("resize"));
  // No room on the left flips to the right of the source.
  source = rect(100, 650, 100, 32);
  fireEvent.focus(trigger);
  card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("208px");
  // Vertical position clamps with an 8px inset.
  expect(card.style.top).toBe("452px");
});

test("the name list wraps long text inside the narrow rail", () => {
  const css = readFileSync("src/session.css", "utf8");
  const rule = /\.subagent-name \{([^}]*)\}/.exec(css)?.[1] ?? "";
  expect(rule).toContain("overflow-wrap: anywhere");
  expect(rule).toContain("min-height: 32px");
  expect(rule).toContain("font-size: 14px");
  expect(rule).not.toContain("white-space: nowrap");
});

test.each([
  ["OpenCode", "opencode"],
  ["Claude Code", "claude-code"],
])(
  "ignores display metadata on %s children and keeps the localized ordinal",
  (_, harness) => {
    const session = newRequestId(),
      row = openCodeSubagentFixture(session);
    const record = readDocument(row);
    record.harness = harness;
    row.documentJson = encode(record);
    withSubagentName(row, "Impostor");
    const page = validateSubagentPage([row], session);
    // The harness-specific validator may reject an edited fixture; either way no
    // name may be retained or rendered for a non-Codex child.
    if (page) {
      expect(subagentRowDisplayName(page[0]!)).toBeUndefined();
      const labels = new Map<string, number>(),
        names = new Map<string, string>();
      retainSubagentLabels(labels, page, names);
      expect(names.size).toBe(0);
      render(
        <SubagentRows
          rows={[row]}
          sessionId={session}
          labels={labels}
          names={names}
        />,
      );
      expect(screen.getByRole("listitem").textContent).toBe("Subagent 1");
      expect(screen.queryByText("Impostor")).toBeNull();
    } else expect(harness).toBe("claude-code");
  },
);

test("Codex children keep an accepted name", () => {
  const session = newRequestId(),
    row = withSubagentName(subagentFixture(session), "Atlas");
  const page = validateSubagentPage([row], session)!;
  expect(subagentRowDisplayName(page[0]!)).toBe("Atlas");
});
