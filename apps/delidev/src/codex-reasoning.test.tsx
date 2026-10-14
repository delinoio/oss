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
it.each([
  // Straight single quote opening a part without boundary whitespace.
  [["The file is called", "'foo'"], "The file is called 'foo'"],
  [["The file is called", "'foo", "'"], "The file is called 'foo'"],
  [["The file is called", "'", "foo", "'"], "The file is called 'foo'"],
  [["Done.", "'foo' is next"], "Done. 'foo' is next"],
  [["Is it", "'foo'?"], "Is it 'foo'?"],
  [["Open (", "'foo')"], "Open ('foo')"],
  [["He said \u201c", "'hi'", "\u201d"], "He said \u201c'hi'\u201d"],
  [["He said", ' "', "'hi'", '"'], "He said \"'hi'\""],
  // A quote that is open is closed by a leading single quote.
  [["called 'foo", "'"], "called 'foo'"],
  [["called 'foo", "' now"], "called 'foo' now"],
  [["called 'foo", "'."], "called 'foo'."],
  [["a 'b' c", "'d'"], "a 'b' c 'd'"],
  [["called 'foo", "'bar'"], "called 'foo 'bar'"],
  [
    ["the user's 'name", "is odd. It is called", "'foo'"],
    "the user's 'name is odd. It is called 'foo'",
  ],
  [["the '90s", "and", "'tis"], "the '90s and 'tis"],
  [["called '4", "'"], "called '4'"],
  [["Emphasis 'em", "'phasis'"], "Emphasis 'em 'phasis'"],
  [["a 'b", "'", "'c'"], "a 'b' 'c'"],
  [["nested 'a \u2018b", "\u2019", "'"], "nested 'a \u2018b\u2019'"],
  // Boundary whitespace is preserved as a single space.
  [["called ", "'foo'"], "called 'foo'"],
  [["called", " 'foo'"], "called 'foo'"],
  [["called", "\n'foo'"], "called 'foo'"],
  [["called 'foo' ", "'bar'"], "called 'foo' 'bar'"],
  [["called 'foo", " '"], "called 'foo '"],
  // Apostrophes inside words, contractions and possessives are not quotes.
  [["don't", "'foo'"], "don't 'foo'"],
  [["It", "'s", "fine"], "It's fine"],
  [["It", "'s"], "It's"],
  [["We", "'re", "here"], "We're here"],
  [["They", "'ve", "'ll", "'d"], "They've'll'd"],
  [["I", "'m", "sure"], "I'm sure"],
  [["Don", "'t", "go"], "Don't go"],
  [["dogs'", "bowl"], "dogs' bowl"],
  [["the dog's", "'bowl'"], "the dog's 'bowl'"],
  [["the dogs'", "'bowl'"], "the dogs' 'bowl'"],
  [["rock 'n' roll", "'now'"], "rock 'n' roll 'now'"],
  [["Is that", "'tis"], "Is that 'tis"],
  [["since", "'90s"], "since '90s"],
  [["a", "'em", "all"], "a 'em all"],
  [["it", "'s", "'foo'"], "it's 'foo'"],
  [["called 'foo", "'s", "bar"], "called 'foo's bar"],
  // Nested and mixed quotes.
  [
    ["She said", '"he called', "'foo'", 'and left"'],
    "She said \"he called 'foo' and left\"",
  ],
  [["\u2018", "foo", "\u2019"], "\u2018foo\u2019"],
  [["called", "\u2018foo\u2019"], "called \u2018foo\u2019"],
  [["called \u2018foo", "'"], "called \u2018foo'"],
  // Punctuation and brackets.
  [["Wait", ";", "ok", ":", "go"], "Wait; ok: go"],
  [["See", "[1]", "and", "{x}"], "See [1] and {x}"],
  [["See [", "1", "]"], "See [1]"],
  [["Is", "it", "?"], "Is it?"],
  [["50", "%", "done"], "50% done"],
  [["Wait\u2026", "ok"], "Wait\u2026 ok"],
  [["(", "(", "x", ")", ")"], "((x))"],
  [["\u00ab", "x", "\u00bb", "y"], "\u00abx\u00bb y"],
  [["\u00bfQu\u00e9", "\u00a1s\u00ed!"], "\u00bfQu\u00e9 \u00a1s\u00ed!"],
  // Markdown and inline code stay inert and unchanged.
  [["Run", "`ls -la`", "now"], "Run `ls -la` now"],
  [["Run `echo 'x", "'`", "now"], "Run `echo 'x'` now"],
  [["Run", "`echo 'x'`", "'y'"], "Run `echo 'x'` 'y'"],
  [["Use", "**bold**", "and", "_it_", "."], "Use **bold** and _it_."],
  [["- item", "- item"], "- item - item"],
  // CJK and Korean text.
  [
    ["\ud14c\uc2a4\ud2b8", "'foo'", "\uc785\ub2c8\ub2e4"],
    "\ud14c\uc2a4\ud2b8 'foo' \uc785\ub2c8\ub2e4",
  ],
  [
    [
      "\ud30c\uc77c\uc774\ub984\uc740",
      "'\uc548\ub155'",
      "\uc785\ub2c8\ub2e4",
      ".",
    ],
    "\ud30c\uc77c\uc774\ub984\uc740 '\uc548\ub155' \uc785\ub2c8\ub2e4.",
  ],
  [
    [
      "\u8a00\u8449\u306f",
      "\u300c",
      "\u3053\u3093\u306b\u3061\u306f",
      "\u300d",
      "\u3067\u3059",
      "\u3002",
    ],
    "\u8a00\u8449\u306f \u300c\u3053\u3093\u306b\u3061\u306f\u300d \u3067\u3059\u3002",
  ],
  [
    ["\ud14c\uc2a4\ud2b8", "\u3001", "\uc2dc\ud5d8"],
    "\ud14c\uc2a4\ud2b8\u3001 \uc2dc\ud5d8",
  ],
  [["\ud55c\uae00'", "\uac12'"], "\ud55c\uae00' \uac12'"],
  // Empty, whitespace-only and edge parts.
  [["", "'foo'", ""], "'foo'"],
  [["called", "", "'foo'"], "called 'foo'"],
  [["called", " ", "'foo'"], "called 'foo'"],
  [["called", "\n\t", "'foo'"], "called 'foo'"],
  [["'", "foo", "'"], "'foo'"],
  [["'", "'"], "''"],
  [["x", "'"], "x '"],
  [[], ""],
])("keeps quotes readable across part boundaries for %j", (parts, expected) => {
  expect(joinReasoningParts(parts)).toBe(expected);
});
it("joins very large quote-heavy parts without quadratic work", () => {
  const quotes = "'".repeat(200000);
  const joined = joinReasoningParts(["a", quotes, "b ' c", quotes]);
  expect(joined.startsWith("a '")).toBe(true);
  expect(joined.length).toBeGreaterThan(400000);
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
