// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  CodexReasoning,
  codexReasoning,
  joinReasoningParts,
  reasoningBodyParts,
} from "./codex-reasoning";
import { TranscriptItem } from "./session";
import { transcriptResource } from "./transcript-role-fixtures";
import { copy, i18n } from "./localization";
import { AppearanceProvider, Theme, type AppearanceBridge } from "./appearance";
import {
  defaultPreferences,
  DisclosureDefault,
} from "./appearance-preferences";
import { statusLabel } from "./product-status";

const snapshot = (summary: string[] = [], content: string[] = []) => ({
  kind: "reasoning",
  text: "",
  summary,
  content,
});
const artifact = () => ({ started: snapshot(), completed: snapshot() });
const row = (value: unknown, state = "complete", revision = 1n) =>
  transcriptResource({ role: "artifact", state, artifact: value }, 2, revision);
afterEach(async () => {
  await i18n.changeLanguage("en");
});
it.each(["en", "ko"])(
  "retains an empty, initially collapsed disclosure without routine artifact decoration in %s",
  async (locale) => {
    await act(() => i18n.changeLanguage(locale));
    const { container } = render(<TranscriptItem resource={row(artifact())} />);
    const trigger = screen.getByText(copy("native-reasoning.thinking"));
    expect(container.querySelector("details")?.open).toBe(false);
    expect(container.querySelector("article > header")).toBeNull();
    expect(container.querySelector("article")?.className).toContain(
      "message-codex-reasoning",
    );
    expect(screen.queryByText("Artifact")).toBeNull();
    expect(
      screen.queryByText(copy("session.completedArtifact_de26fd")),
    ).toBeNull();
    expect(screen.queryByText(statusLabel("complete"))).toBeNull();
    fireEvent.click(trigger);
    expect(container.querySelector("details")?.open).toBe(true);
    expect(
      screen.getByText(copy("native-reasoning.noSuppliedContent")),
    ).toBeTruthy();
    expect(container.querySelector("h4")).toBeNull();
  },
);
it("selects supplied completed then initial summary parts without changing Unicode or whitespace", () => {
  const value = {
    started: snapshot(["Initial summary"]),
    completed: snapshot(["", " \n ", "  Final 한글🙂\nsummary.  "]),
  };
  const { container, rerender } = render(
    <TranscriptItem resource={row(value)} />,
  );
  const full = value.completed.summary[2];
  expect(container.querySelector("summary")?.textContent).toBe(full);
  expect(
    container.querySelector("summary .codex-reasoning-summary")?.textContent,
  ).toBe(full);
  fireEvent.click(container.querySelector("summary")!);
  expect(bodyText(container)).toBe("Final 한글🙂\nsummary.");
  rerender(
    <TranscriptItem
      resource={row({ ...value, completed: snapshot([" "]) }, "complete", 2n)}
    />,
  );
  expect(container.querySelector("summary")?.textContent).toBe(
    "Initial summary",
  );
  rerender(<TranscriptItem resource={row(artifact(), "complete", 3n)} />);
  expect(container.querySelector("summary")?.textContent).toBe("Thinking");
  expect(container.querySelector("details")?.open).toBe(true);
});
const delta = (
  sequence: number,
  text: string,
  kind = "reasoning-content",
  index = 0,
) => ({ sequence, delta: { kind, index, text } });
const bodyText = (container: HTMLElement) =>
  container.querySelector(".codex-reasoning-text")?.textContent;
const expand = (container: HTMLElement) =>
  fireEvent.click(container.querySelector("summary")!);
it("renders one inert body without observation headings or kind and index labels", () => {
  const { container } = render(
    <TranscriptItem
      resource={row(
        {
          started: snapshot(),
          deltas: [
            delta(3, "The user typed"),
            delta(4, ' "테스트" which is'),
            delta(5, "Korean for test."),
          ],
        },
        "streaming",
      )}
    />,
  );
  expand(container);
  expect(container.querySelectorAll(".codex-reasoning-text")).toHaveLength(1);
  expect(bodyText(container)).toBe(
    'The user typed "테스트" which is Korean for test.',
  );
  const body = container.querySelector(".codex-reasoning-body")!;
  expect(body.querySelector("section, h4, small, pre")).toBeNull();
  expect(body.children).toHaveLength(1);
  expect(body.textContent).not.toMatch(
    /Streamed observations|Initial observation|Final observation|reasoning-content|part/i,
  );
});
it("replaces the streamed body with the completed content snapshot instead of appending", () => {
  const streamed = {
    started: snapshot(),
    deltas: [delta(3, "streamed words")],
  };
  const { container, rerender } = render(
    <TranscriptItem resource={row(streamed, "streaming")} />,
  );
  expand(container);
  const trigger = container.querySelector("summary")!;
  trigger.focus();
  expect(bodyText(container)).toBe("streamed words");
  rerender(
    <TranscriptItem
      resource={row(
        { ...streamed, completed: snapshot(["done"], ["completed", "text"]) },
        "complete",
        2n,
      )}
    />,
  );
  expect(bodyText(container)).toBe("completed text");
  expect(container.textContent).not.toContain("streamed words");
  expect(container.querySelector("details")?.open).toBe(true);
  expect(document.activeElement).toBe(trigger);
});
it("falls back through the body sources in the documented order without mixing families", () => {
  const full = {
    started: snapshot(["started summary"], ["started content"]),
    completed: snapshot(["completed summary"], ["completed content"]),
    deltas: [
      delta(2, "streamed summary", "reasoning-summary"),
      delta(3, "streamed content"),
    ],
  };
  const steps: [unknown, string][] = [
    [full, "completed content"],
    [
      { ...full, completed: snapshot(["completed summary"], ["", " "]) },
      "streamed content",
    ],
    [
      {
        ...full,
        completed: snapshot(["completed summary"]),
        deltas: full.deltas.slice(0, 1),
      },
      "started content",
    ],
    [
      {
        started: snapshot(["started summary"]),
        completed: snapshot(["completed summary"]),
        deltas: full.deltas.slice(0, 1),
      },
      "completed summary",
    ],
    [
      {
        started: snapshot(["started summary"]),
        completed: snapshot([""]),
        deltas: full.deltas.slice(0, 1),
      },
      "streamed summary",
    ],
    [
      {
        started: snapshot(["started summary"]),
        completed: snapshot(),
      },
      "started summary",
    ],
  ];
  for (const [value, expected] of steps) {
    const { container, unmount } = render(
      <TranscriptItem resource={row(value)} />,
    );
    expand(container);
    expect(bodyText(container)).toBe(expected);
    unmount();
  }
  const { container } = render(<TranscriptItem resource={row(artifact())} />);
  expand(container);
  expect(bodyText(container)).toBeUndefined();
  expect(
    screen.getByText(copy("native-reasoning.noSuppliedContent")),
  ).toBeTruthy();
});
it("orders streamed content by sequence and ignores other delta kinds", () => {
  const { container } = render(
    <TranscriptItem
      resource={row(
        {
          started: snapshot(),
          deltas: [
            delta(2, "summary only", "reasoning-summary"),
            delta(3, "first"),
            delta(4, "", "reasoning-summary-added", 1),
            delta(5, "second", "reasoning-content", 1),
          ],
        },
        "streaming",
      )}
    />,
  );
  expand(container);
  expect(bodyText(container)).toBe("first second");
});
it.each([
  [["Hello", "world"], "Hello world"],
  [["Hello ", " world"], "Hello world"],
  [["Hello\n\n", "\tworld"], "Hello world"],
  [["Hello", " ", "world"], "Hello world"],
  [["Hello", "world "], "Hello world"],
  [["  Hello", "", "world.  "], "Hello world."],
  [["Hello", ", world", "!"], "Hello, world!"],
  [["Wait", "…", "ok"], "Wait… ok"],
  [["See (", "this", ")"], "See (this)"],
  [["He said", '"', "hi", '"', "now"], 'He said "hi" now'],
  [["He said “", "hi", "” now"], "He said “hi” now"],
  [["It", "'s", "fine"], "It's fine"],
  [["테스트", "입니다", "."], "테스트 입니다."],
  [["단어를", "「", "분리", "」", "합니다"], "단어를 「분리」 합니다"],
  [["Con", "tinued"], "Con tinued"],
  [["a  b", "c\n d"], "a  b c\n d"],
  [["", " ", ""], ""],
])("formats part boundaries for %j", (parts, expected) => {
  expect(joinReasoningParts(parts)).toBe(expected);
});
it("formats for display only and never mutates the retained record", () => {
  const value = {
    started: snapshot(["  a "], ["  <b>x</b> ", "y\n"]),
    completed: snapshot([], [" c", "d "]),
    deltas: [delta(2, " e ", "reasoning-content", 4), delta(9, "f")],
  };
  const before = JSON.stringify(value);
  const data = codexReasoning(value, "complete")!;
  const { container } = render(<CodexReasoning data={data} />);
  expand(container);
  expect(bodyText(container)).toBe("c d");
  expect(JSON.stringify(value)).toBe(before);
  expect(JSON.stringify(data)).toBe(before);
  expect(reasoningBodyParts(data)).toEqual([" c", "d "]);
});
it("keeps HTML-like and shell-like source as inert text", () => {
  const exact = "<script>danger()</script> $(touch /tmp/never) 한글🙂";
  const { container } = render(
    <TranscriptItem
      resource={row({ started: snapshot(), completed: snapshot([], [exact]) })}
    />,
  );
  expand(container);
  expect(bodyText(container)).toBe(exact);
  expect(container.querySelector("script, a, button, img")).toBeNull();
});
it("keeps the same disclosure and focused trigger through streaming completion and locale changes", async () => {
  const initial = {
    started: snapshot(["Initial summary"]),
    deltas: [
      {
        sequence: 3,
        delta: {
          kind: "reasoning-summary",
          index: 0,
          text: "delta stays separate",
        },
      },
    ],
  };
  const { container, rerender } = render(
    <TranscriptItem resource={row(initial, "streaming")} />,
  );
  const trigger = container.querySelector("summary")!,
    details = container.querySelector("details")!;
  expect(screen.getByText(statusLabel("streaming"))).toBeTruthy();
  trigger.focus();
  fireEvent.click(trigger);
  rerender(
    <TranscriptItem
      resource={row(
        { ...initial, completed: snapshot(["Final summary"]) },
        "complete",
        2n,
      )}
    />,
  );
  await act(() => i18n.changeLanguage("ko"));
  expect(container.querySelector("details")).toBe(details);
  expect(details.open).toBe(true);
  expect(document.activeElement).toBe(trigger);
  expect(trigger.textContent).toBe("Final summary");
  expect(container.querySelector("article > header")).toBeNull();
});
it.each([
  DisclosureDefault.Original,
  DisclosureDefault.Expanded,
  DisclosureDefault.Collapsed,
])(
  "applies %s only initially and retains the user's later choice",
  async (preference) => {
    const preferences = {
      ...defaultPreferences(),
      reasoning_disclosure: preference,
    };
    const bridge: AppearanceBridge = {
      read: vi.fn(async () => ({
        revision: 1,
        theme: Theme.Light,
        problem: null,
        preferences,
      })),
      subscribe: vi.fn(async () => () => {}),
      update: vi.fn(),
    };
    const data = codexReasoning(artifact(), "complete")!;
    const view = (summary: string) => (
      <AppearanceProvider bridge={bridge}>
        <CodexReasoning
          data={{
            ...data,
            completed: snapshot([summary]) as typeof data.started,
          }}
        />
      </AppearanceProvider>
    );
    const { container, rerender } = render(view("Original"));
    await waitFor(() => expect(bridge.read).toHaveBeenCalled());
    await waitFor(() =>
      expect(container.querySelector("details")?.open).toBe(
        preference === DisclosureDefault.Expanded,
      ),
    );
    const details = container.querySelector("details")!;
    fireEvent.click(container.querySelector("summary")!);
    const chosen = details.open;
    rerender(view("Updated"));
    await act(() => i18n.changeLanguage("ko"));
    expect(details.open).toBe(chosen);
  },
);
it.each(["interrupted", "failed", "unavailable", "recovery-required"])(
  "preserves the actual %s state without a completion claim",
  (state) => {
    const { container } = render(
      <TranscriptItem
        resource={row({ started: snapshot(["Supplied summary"]) }, state)}
      />,
    );
    expect(screen.getByText(statusLabel(state))).toBeTruthy();
    expect(container.querySelector("summary")?.textContent).toBe(
      "Supplied summary",
    );
  },
);
it.each([
  {
    name: "null summary",
    value: { started: { ...snapshot(), summary: null } },
  },
  {
    name: "null part",
    value: { started: snapshot([null as unknown as string]) },
  },
  {
    name: "foreign text field",
    value: { started: { ...snapshot(), text: "Not indexed reasoning" } },
  },
  {
    name: "encrypted field",
    value: { started: { ...snapshot(), encrypted_content: "private" } },
  },
  { name: "missing completed", value: { started: snapshot() } },
  {
    name: "cross-kind completed",
    value: { started: snapshot(), completed: { ...snapshot(), kind: "plan" } },
  },
  {
    name: "negative index",
    value: {
      ...artifact(),
      deltas: [
        {
          sequence: 2,
          delta: { kind: "reasoning-summary", index: -1, text: "bad" },
        },
      ],
    },
  },
  {
    name: "foreign delta",
    value: {
      ...artifact(),
      deltas: [
        {
          sequence: 2,
          delta: { kind: "reasoning-text", index: 0, text: "bad" },
        },
      ],
    },
  },
  {
    name: "out-of-order sequence",
    value: {
      ...artifact(),
      deltas: [2, 2].map((sequence) => ({
        sequence,
        delta: { kind: "reasoning-summary", index: 0, text: "bad" },
      })),
    },
  },
  {
    name: "summary-added text",
    value: {
      ...artifact(),
      deltas: [
        {
          sequence: 2,
          delta: { kind: "reasoning-summary-added", index: 0, text: "bad" },
        },
      ],
    },
  },
  {
    name: "unknown artifact",
    value: { started: { ...snapshot(), kind: "future" } },
  },
])("retains the generic artifact fallback for $name", ({ value }) => {
  expect(codexReasoning(value, "complete")).toBeUndefined();
  const { container } = render(<TranscriptItem resource={row(value)} />);
  expect(container.querySelector(".message-codex-reasoning")).toBeNull();
  expect(container.querySelector("article > header")).toBeTruthy();
});
const envelope = (extra: Record<string, unknown>, state = "complete") =>
  transcriptResource({
    role: "artifact",
    state,
    artifact: artifact(),
    ...extra,
  });
it.each([
  { name: "phase", extra: { phase: "analysis" } },
  {
    name: "input_id",
    extra: { input_id: "01960dcb-e1fa-7000-8000-000000000001" },
  },
  {
    name: "inherited",
    extra: {
      inherited: { source_session_id: "01960dcb-e1fa-7000-8000-000000000002" },
    },
  },
  { name: "turn_timing", extra: { turn_timing: {} } },
  { name: "unknown field", extra: { future_field: true } },
  { name: "empty tool", extra: { tool: {} } },
  { name: "progress", extra: { progress: {} } },
  { name: "numeric text", extra: { text: 42 } },
  { name: "object text", extra: { text: { content: "retained" } } },
  { name: "array text", extra: { text: ["retained"] } },
  { name: "boolean text", extra: { text: false } },
  { name: "null text", extra: { text: null } },
  { name: "nonempty text", extra: { text: "retained" } },
  { name: "numeric native_id", extra: { native_id: 7 } },
  { name: "negative sequence", extra: { first_sequence: -1 } },
  { name: "non-array attachments", extra: { attachments: {} } },
])(
  "retains the generic artifact fallback for the mixed envelope $name",
  ({ extra }) => {
    for (const state of ["complete", "streaming"]) {
      const value = artifact();
      if (state === "streaming")
        delete (value as { completed?: unknown }).completed;
      const { container, unmount } = render(
        <TranscriptItem
          resource={transcriptResource({
            role: "artifact",
            state,
            artifact: value,
            ...extra,
          })}
        />,
      );
      expect(container.querySelector(".message-codex-reasoning")).toBeNull();
      expect(container.querySelector("article > header")).toBeTruthy();
      unmount();
    }
  },
);
it.each([
  { name: "absent text", extra: {} },
  { name: "empty text", extra: { text: "" } },
  {
    name: "common metadata",
    extra: {
      text: "",
      execution_id: "01960dcb-e1fa-7000-8000-000000000003",
      native_thread_id: "thread",
      native_turn_id: "turn",
      native_id: "native",
      first_sequence: 1,
      last_sequence: 4,
      context_revision: 2,
      attachments: [],
    },
  },
])(
  "selects the dedicated disclosure for a valid envelope with $name",
  ({ extra }) => {
    const { container } = render(<TranscriptItem resource={envelope(extra)} />);
    expect(container.querySelector(".message-codex-reasoning")).toBeTruthy();
    expect(container.querySelector("details")?.open).toBe(false);
  },
);
it.each([
  ["streaming", { started: snapshot() }],
  [
    "complete",
    {
      started: snapshot([" Résumé 한국어 "], ["  content\n\t "]),
      completed: snapshot([" Résumé 한국어 "], ["  content\n\t "]),
    },
  ],
  ["complete", { started: snapshot([], [""]), completed: snapshot([], [""]) }],
])("keeps the dedicated disclosure for a valid %s snapshot", (state, value) => {
  const { container } = render(
    <TranscriptItem
      resource={transcriptResource({
        role: "artifact",
        state,
        artifact: value,
      })}
    />,
  );
  expect(container.querySelector(".message-codex-reasoning")).toBeTruthy();
  expect(container.querySelector("details")?.open).toBe(false);
});
it.each(["\u0000", "\ud800", "한".repeat(90000)])(
  "rejects invalid or oversized source text without summary generation",
  (text) => {
    expect(
      codexReasoning({ started: snapshot([text]) }, "streaming"),
    ).toBeUndefined();
  },
);
it("retains independent OpenCode reasoning and ordinary plan artifact presentation", () => {
  const opencode = {
    kind: "reasoning-text",
    text: "Original OpenCode",
    summary: null,
    content: null,
  };
  const { container } = render(
    <>
      <TranscriptItem
        resource={row({ started: opencode, completed: opencode })}
      />
      <TranscriptItem
        resource={row({
          started: {
            kind: "plan",
            text: "Original plan",
            summary: null,
            content: null,
          },
          completed: {
            kind: "plan",
            text: "Final plan",
            summary: null,
            content: null,
          },
        })}
      />
    </>,
  );
  expect(screen.getByText("Reasoning · Complete")).toBeTruthy();
  expect(screen.getByText("Completed artifact")).toBeTruthy();
  expect(container.querySelector(".codex-reasoning")).toBeNull();
});
