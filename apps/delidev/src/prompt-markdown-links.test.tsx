// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  SkillProvenance,
  SkillService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { useSkillCompletion, MAX_PROMPT_BYTES } from "./skill-completion";
import { ExternalLinkFailure } from "./external-links";

const native = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}));
vi.mock("@tauri-apps/api/core", () => native);

const URL = "https://example.com/a?b=1";
const LINK = `[${URL}](${URL})`;

beforeEach(() => {
  native.invoke.mockReset();
  native.invoke.mockResolvedValue(undefined);
  native.isTauri.mockReturnValue(true);
});

interface Replacement {
  text: string;
  start: number;
  end: number;
}
const machine = newRequestId(),
  agent = newRequestId();
const available = {
  name: "add-issue",
  description: "fixture",
  provenance: SkillProvenance.USER,
  selection: {
    $typeName: "delidev.v1.SkillSelection" as const,
    inventoryId: newRequestId(),
    workerDeviceId: newRequestId(),
    skillId: newRequestId(),
    contentRevision: "a".repeat(64),
  },
};
function Composer({
  initial = "",
  replacement,
  inventory = false,
}: {
  initial?: string;
  replacement?: Replacement;
  inventory?: boolean;
}) {
  const [value, setValue] = useState(initial);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change: (next) => {
      if (new TextEncoder().encode(next).byteLength > MAX_PROMPT_BYTES)
        return false;
      setValue(next);
    },
    textarea,
    machineId: inventory ? machine : "",
    agentId: inventory ? agent : "",
  });
  return (
    <>
      {skills.wrap(
        <textarea
          aria-label="Message"
          ref={textarea}
          value={skills.displayValue}
          onChange={(e) =>
            skills.onChange(e.target.value, e.target.selectionStart)
          }
          onSelect={skills.onSelect}
          onKeyDown={skills.onKeyDown}
          onCompositionStart={skills.onCompositionStart}
          onCompositionEnd={skills.onCompositionEnd}
          {...skills.attributes}
        />,
      )}
      {skills.warning}
      {replacement ? (
        <button
          onClick={() =>
            skills.replaceUnbound(
              replacement.text,
              replacement.start,
              replacement.end,
            )
          }
        >
          Replace
        </button>
      ) : null}
      <output data-testid="canonical">{value}</output>
    </>
  );
}
function view(initial?: string, replacement?: Replacement, inventory = false) {
  const transport = createRouterTransport((router) => {
    if (inventory)
      router.service(SkillService, {
        listSkills: async () => ({ skills: [available] }),
      });
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Composer
          initial={initial}
          replacement={replacement}
          inventory={inventory}
        />
      </QueryClientProvider>
    </TransportProvider>,
  );
  return screen.getByRole("textbox") as HTMLTextAreaElement;
}
const canonical = () => screen.getByTestId("canonical").textContent;
function paste(input: HTMLTextAreaElement, text: string, files: File[] = []) {
  return fireEvent.paste(input, {
    clipboardData: { getData: () => text, files },
  });
}
function select(input: HTMLTextAreaElement, start: number, end = start) {
  input.setSelectionRange(start, end);
  fireEvent.select(input);
}
function type(input: HTMLTextAreaElement, text: string) {
  fireEvent.change(input, {
    target: {
      value: text,
      selectionStart: text.length,
      selectionEnd: text.length,
    },
  });
}

it("converts a pasted bare URL immediately and collapses it to its label", () => {
  const input = view();
  expect(paste(input, `see ${URL} now`)).toBe(false);
  expect(canonical()).toBe(`see ${LINK} now`);
  expect(input.value).toBe(`see ${URL} now`);
  const link = document.querySelector(".prompt-markdown-link");
  expect(link?.textContent).toBe(URL);
});

it("converts a typed URL only when a delimiter terminates it", () => {
  const input = view();
  type(input, `go ${URL}`);
  expect(canonical()).toBe(`go ${URL}`);
  type(input, `go ${URL} `);
  expect(canonical()).toBe(`go ${LINK} `);
});

it("converts a typed URL terminated by a newline and keeps the newline", () => {
  const input = view();
  type(input, `go ${URL}`);
  type(input, `go ${URL}\n`);
  expect(canonical()).toBe(`go ${LINK}\n`);
  type(input, `go ${LINK}\nplain ${URL}`);
  expect(canonical()).toBe(`go ${LINK}\nplain ${URL}`);
});

it("leaves non-HTTP URLs, plain pastes and restored text unconverted", () => {
  const input = view(`restored ${URL} text`);
  expect(canonical()).toBe(`restored ${URL} text`);
  expect(paste(input, "plain text only")).toBe(true);
  expect(paste(input, "www.example.com mailto:a@b.c javascript:x")).toBe(true);
  expect(canonical()).toBe(`restored ${URL} text`);
});

it("keeps image paste ownership with the attachment input", () => {
  const input = view();
  const file = new File(["x"], "a.png", { type: "image/png" });
  expect(paste(input, URL, [file])).toBe(true);
  expect(canonical()).toBe("");
});

it("reveals the source for an interior caret and collapses when leaving", () => {
  const input = view();
  paste(input, `a ${URL} b`);
  select(input, 5);
  expect(input.value).toBe(`a ${LINK} b`);
  expect(canonical()).toBe(`a ${LINK} b`);
  select(input, 0);
  expect(input.value).toBe(`a ${URL} b`);
  select(input, 0, input.value.length);
  expect(input.value).toBe(`a ${LINK} b`);
  expect(canonical()).toBe(`a ${LINK} b`);
});

it("keeps invalid Markdown links as visible source", () => {
  const input = view(
    "[a](javascript:alert(1)) [b](https://user:pw@example.com)",
  );
  expect(input.value).toBe(
    "[a](javascript:alert(1)) [b](https://user:pw@example.com)",
  );
});

it("undoes and redoes a conversion together with its trigger", () => {
  const input = view();
  type(input, `go ${URL}`);
  type(input, `go ${URL} `);
  expect(canonical()).toBe(`go ${LINK} `);
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(canonical()).toBe(`go ${URL}`);
  fireEvent.keyDown(input, { key: "z", ctrlKey: true, shiftKey: true });
  expect(canonical()).toBe(`go ${LINK} `);
  paste(input, ` ${URL}`);
  expect(canonical()).toBe(`go ${LINK}  ${LINK}`);
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(canonical()).toBe(`go ${LINK} `);
  fireEvent.keyDown(input, { key: "y", ctrlKey: true });
  expect(canonical()).toBe(`go ${LINK}  ${LINK}`);
});

it("leaves a conversion that exceeds the prompt limit unconverted atomically", () => {
  const input = view();
  const filler = "a".repeat(MAX_PROMPT_BYTES - URL.length - 2);
  type(input, filler);
  expect(paste(input, ` ${URL}`)).toBe(true);
  expect(canonical()).toBe(filler);
  type(input, `${filler} ${URL}`);
  expect(canonical()).toBe(`${filler} ${URL}`);
  type(input, `${filler} ${URL} `);
  expect(canonical()).toBe(`${filler} ${URL} `);
});

it("copies and cuts canonical Markdown", () => {
  const input = view();
  paste(input, `a ${URL} b`);
  select(input, 0, input.value.length);
  const data = { setData: vi.fn(), getData: () => "", files: [] };
  expect(fireEvent.copy(input, { clipboardData: data })).toBe(false);
  expect(data.setData).toHaveBeenCalledWith("text/plain", `a ${LINK} b`);
  fireEvent.cut(input, { clipboardData: data });
  expect(canonical()).toBe("");
});

it("opens only the parsed URL once on Ctrl+click", async () => {
  const input = view();
  paste(input, `a ${URL} b`);
  select(input, 3);
  fireEvent.click(input);
  fireEvent.click(input, { metaKey: true });
  fireEvent.click(input, { ctrlKey: true, shiftKey: true });
  fireEvent.click(input, { ctrlKey: true, altKey: true });
  fireEvent.click(input, { ctrlKey: true, button: 1 });
  expect(native.invoke).not.toHaveBeenCalled();
  await act(async () => {
    fireEvent.click(input, { ctrlKey: true });
  });
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", { url: URL });
  expect(canonical()).toBe(`a ${LINK} b`);
});

it("keeps links editable without a native opener and reports native failures", async () => {
  const input = view();
  paste(input, URL);
  select(input, 3);
  native.isTauri.mockReturnValue(false);
  expect(fireEvent.click(input, { ctrlKey: true })).toBe(true);
  expect(native.invoke).not.toHaveBeenCalled();
  native.isTauri.mockReturnValue(true);
  native.invoke.mockRejectedValue(ExternalLinkFailure.BrowserUnavailable);
  await act(async () => {
    fireEvent.click(input, { ctrlKey: true });
  });
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(canonical()).toBe(LINK);
});

it("does not convert during IME composition", () => {
  const input = view();
  fireEvent.compositionStart(input);
  type(input, `${URL} `);
  expect(canonical()).toBe(`${URL} `);
  fireEvent.compositionEnd(input);
  expect(canonical()).toBe(`${URL} `);
});

it("maps a canonical replacement selection to display offsets", () => {
  const text = `a ${LINK} tail`;
  // Canonical offsets: select "tail" after the collapsed link.
  const start = text.indexOf("tail");
  const input = view("", { text, start, end: text.length });
  fireEvent.click(screen.getByText("Replace"));
  expect(canonical()).toBe(text);
  expect(input.value).toBe(`a ${URL} tail`);
  const display = input.value.indexOf("tail");
  expect([input.selectionStart, input.selectionEnd]).toEqual([
    display,
    input.value.length,
  ]);
});

const OVERLAY_SPANS = ".skill-text-overlay span";
const collapsedLinks = () =>
  document.querySelectorAll(".prompt-markdown-link").length;
const spanCount = () => document.querySelectorAll(OVERLAY_SPANS).length;
const SMALL_LINK = "[x](https://a) ";

it("shows a 240 KiB link draft as complete plain source without decoration spans", () => {
  const draft = SMALL_LINK.repeat(16384);
  expect(new TextEncoder().encode(draft).byteLength).toBeLessThan(
    MAX_PROMPT_BYTES,
  );
  const input = view(draft);
  expect(input.value).toBe(draft);
  expect(canonical()).toBe(draft);
  expect(collapsedLinks()).toBe(0);
  expect(spanCount()).toBeLessThanOrEqual(512);
  type(input, `${draft}z`);
  expect(canonical()).toBe(`${draft}z`);
  expect(spanCount()).toBeLessThanOrEqual(512);
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(canonical()).toBe(draft);
  expect(input.value).toBe(draft);
  expect(spanCount()).toBeLessThanOrEqual(512);
});

it.each([
  [255, true],
  [256, true],
  [257, false],
])(
  "collapses %s links only while they fit the painted budget",
  (count, fits) => {
    const draft = SMALL_LINK.repeat(count);
    const input = view(draft);
    expect(canonical()).toBe(draft);
    expect(collapsedLinks()).toBe(fits ? count : 0);
    expect(input.value).toBe(fits ? "x ".repeat(count) : draft);
    expect(spanCount()).toBeLessThanOrEqual(512);
  },
);

it("restores collapse when edits return below the budget and keeps canonical copy", () => {
  const input = view(SMALL_LINK.repeat(257));
  expect(collapsedLinks()).toBe(0);
  type(input, SMALL_LINK.repeat(256));
  expect(canonical()).toBe(SMALL_LINK.repeat(256));
  expect(collapsedLinks()).toBe(256);
  expect(input.value).toBe("x ".repeat(256));
  select(input, 0, input.value.length);
  const data = { setData: vi.fn(), getData: () => "", files: [] };
  fireEvent.copy(input, { clipboardData: data });
  expect(data.setData).toHaveBeenCalledWith(
    "text/plain",
    SMALL_LINK.repeat(256),
  );
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(canonical()).toBe(SMALL_LINK.repeat(257));
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(SMALL_LINK.repeat(257));
});

it("keeps ordinary link reveal and open behavior for small drafts", async () => {
  const input = view(`a ${LINK} b`);
  expect(collapsedLinks()).toBe(1);
  select(input, 5);
  expect(input.value).toBe(`a ${LINK} b`);
  select(input, 0);
  await act(async () => {
    select(input, 3);
    fireEvent.click(input, { ctrlKey: true });
  });
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", { url: URL });
});

it("shares one painted budget between links and skill ranges", async () => {
  const skill = (count: number) => "$gone ".repeat(count);
  const link = (count: number) => SMALL_LINK.repeat(count);
  const unavailable = () =>
    document.querySelectorAll(".skill-token-unavailable").length;
  // 128 + 128 fits the combined budget: both kinds are decorated.
  const input = view(skill(128) + link(128), undefined, true);
  await waitFor(() => expect(unavailable()).toBe(128));
  expect(collapsedLinks()).toBe(128);
  expect(spanCount()).toBeLessThanOrEqual(512);
  // One link too many: links yield as a whole, skills keep their decoration.
  type(input, skill(128) + link(129));
  await waitFor(() => expect(collapsedLinks()).toBe(0));
  expect(unavailable()).toBe(128);
  expect(input.value).toBe(skill(128) + link(129));
  expect(canonical()).toBe(skill(128) + link(129));
  expect(spanCount()).toBeLessThanOrEqual(512);
  // Back within the budget: the collapse returns with the skill decoration.
  type(input, skill(128) + link(128));
  await waitFor(() => expect(collapsedLinks()).toBe(128));
  expect(unavailable()).toBe(128);
  expect(spanCount()).toBeLessThanOrEqual(512);
  // Skills beyond the budget paint nothing but still report their exact count;
  // the links then own the whole budget.
  type(input, skill(300) + link(256));
  await waitFor(() => expect(unavailable()).toBe(0));
  expect(collapsedLinks()).toBe(256);
  expect(spanCount()).toBeLessThanOrEqual(512);
  expect(
    document.querySelector(".sidebar-sr-only[aria-hidden='true']")?.textContent,
  ).toBe("300 skill tokens are unavailable in this scope.");
  type(input, skill(300) + link(257));
  await waitFor(() => expect(collapsedLinks()).toBe(0));
  expect(unavailable()).toBe(0);
  expect(spanCount()).toBe(0);
  expect(input.value).toBe(skill(300) + link(257));
});

const SKILL = "$gone ";
const skills = (count: number) => SKILL.repeat(count);
const links = (count: number) => SMALL_LINK.repeat(count);
const unavailableTokens = () =>
  document.querySelectorAll(".skill-token-unavailable").length;
const selectionOf = (input: HTMLTextAreaElement) => [
  input.selectionStart,
  input.selectionEnd,
];

it("keeps links plain while composing when skill ranges suppressed them", async () => {
  // 128 painted skill ranges leave room for 128 links, so 129 stay plain.
  const draft = skills(128) + links(129);
  const input = view(draft, undefined, true);
  await waitFor(() => expect(unavailableTokens()).toBe(128));
  expect(collapsedLinks()).toBe(0);
  select(input, draft.length);
  fireEvent.compositionStart(input);
  // Composition empties the skill analysis, but the display must not change.
  expect(unavailableTokens()).toBe(0);
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(draft);
  expect(selectionOf(input)).toEqual([draft.length, draft.length]);
  type(input, `${draft}あ`);
  expect(canonical()).toBe(`${draft}あ`);
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(`${draft}あ`);
  expect(selectionOf(input)).toEqual([draft.length + 1, draft.length + 1]);
  fireEvent.compositionEnd(input);
  await waitFor(() => expect(unavailableTokens()).toBe(128));
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(`${draft}あ`);
  expect(canonical()).toBe(`${draft}あ`);
  expect(selectionOf(input)).toEqual([draft.length + 1, draft.length + 1]);
});

it("keeps collapsed links collapsed while composing and when composition ends", async () => {
  const draft = skills(128) + links(128);
  const collapsedDisplay = skills(128) + "x ".repeat(128);
  const input = view(draft, undefined, true);
  await waitFor(() => expect(unavailableTokens()).toBe(128));
  expect(collapsedLinks()).toBe(128);
  expect(input.value).toBe(collapsedDisplay);
  select(input, collapsedDisplay.length);
  fireEvent.compositionStart(input);
  expect(collapsedLinks()).toBe(128);
  expect(input.value).toBe(collapsedDisplay);
  type(input, `${collapsedDisplay}あ`);
  expect(canonical()).toBe(`${draft}あ`);
  expect(collapsedLinks()).toBe(128);
  fireEvent.compositionEnd(input);
  await waitFor(() => expect(unavailableTokens()).toBe(128));
  expect(collapsedLinks()).toBe(128);
  expect(input.value).toBe(`${collapsedDisplay}あ`);
  expect(canonical()).toBe(`${draft}あ`);
});

it("recomputes the link budget once composition ends and keeps the canonical caret", async () => {
  // The skills are replaced while composing, so the budget grows only after
  // composition ends and the links then collapse under a stable caret.
  const draft = skills(128) + links(129);
  const composed = `あ${links(129)}`;
  const input = view(draft, undefined, true);
  await waitFor(() => expect(unavailableTokens()).toBe(128));
  expect(collapsedLinks()).toBe(0);
  select(input, 0);
  fireEvent.compositionStart(input);
  type(input, composed);
  expect(canonical()).toBe(composed);
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(composed);
  select(input, composed.length);
  fireEvent.compositionEnd(input);
  await waitFor(() => expect(collapsedLinks()).toBe(129));
  expect(canonical()).toBe(composed);
  expect(input.value).toBe(`あ${"x ".repeat(129)}`);
  expect(selectionOf(input)).toEqual([input.value.length, input.value.length]);
});

it("suppresses links only after composition ends when new skill ranges exhaust the budget", async () => {
  const draft = links(256);
  const collapsedDisplay = "x ".repeat(256);
  const input = view(draft, undefined, true);
  await waitFor(() => expect(collapsedLinks()).toBe(256));
  expect(input.value).toBe(collapsedDisplay);
  select(input, collapsedDisplay.length);
  fireEvent.compositionStart(input);
  type(input, `${collapsedDisplay}$gone `);
  expect(canonical()).toBe(`${draft}$gone `);
  // The new skill token is not painted, and the links stay collapsed.
  expect(collapsedLinks()).toBe(256);
  expect(input.value).toBe(`${collapsedDisplay}$gone `);
  fireEvent.compositionEnd(input);
  await waitFor(() => expect(unavailableTokens()).toBe(1));
  // 256 links no longer fit beside one painted skill range.
  expect(collapsedLinks()).toBe(0);
  expect(input.value).toBe(`${draft}$gone `);
  expect(canonical()).toBe(`${draft}$gone `);
  expect(selectionOf(input)).toEqual([input.value.length, input.value.length]);
});

describe("link collapse state during IME composition", () => {
  const collapsedDisplay = (count: number) => "x ".repeat(count);
  const atEnd = (input: HTMLTextAreaElement) =>
    expect(selectionOf(input)).toEqual([
      input.value.length,
      input.value.length,
    ]);

  it("keeps 256 collapsed links collapsed when a composed link makes 257, and settles after composition", async () => {
    const draft = links(256);
    const input = view(draft);
    expect(collapsedLinks()).toBe(256);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    type(input, collapsedDisplay(256) + SMALL_LINK);
    expect(canonical()).toBe(links(257));
    // The new link stays plain source and the 256 do not switch to canonical.
    expect(collapsedLinks()).toBe(256);
    expect(input.value).toBe(collapsedDisplay(256) + SMALL_LINK);
    type(input, collapsedDisplay(256) + SMALL_LINK + "あ");
    expect(collapsedLinks()).toBe(256);
    expect(input.value).toBe(collapsedDisplay(256) + SMALL_LINK + "あ");
    fireEvent.compositionEnd(input);
    // 257 links exceed the budget, so the whole draft becomes plain source.
    await waitFor(() => expect(collapsedLinks()).toBe(0));
    expect(canonical()).toBe(links(257) + "あ");
    expect(input.value).toBe(links(257) + "あ");
    atEnd(input);
    expect(spanCount()).toBeLessThanOrEqual(512);
  });

  it("keeps 257 plain links plain when composing removes one, and collapses after composition", async () => {
    const draft = links(257);
    const input = view(draft);
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(draft);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    type(input, links(256));
    expect(canonical()).toBe(links(256));
    // 256 links would fit, but the display must not change while composing.
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(links(256));
    type(input, links(256) + "あ");
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(links(256) + "あ");
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(collapsedLinks()).toBe(256));
    expect(canonical()).toBe(links(256) + "あ");
    expect(input.value).toBe(collapsedDisplay(256) + "あ");
    atEnd(input);
  });

  it("does not toggle while a composed draft crosses the boundary and back", async () => {
    const input = view(links(256));
    expect(collapsedLinks()).toBe(256);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    const steps = [
      collapsedDisplay(256) + SMALL_LINK,
      collapsedDisplay(256) + SMALL_LINK + SMALL_LINK,
      collapsedDisplay(256) + SMALL_LINK,
      collapsedDisplay(256),
    ];
    for (const display of steps) {
      type(input, display);
      expect(collapsedLinks()).toBe(256);
      expect(input.value).toBe(display);
    }
    expect(canonical()).toBe(links(256));
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(canonical()).toBe(links(256)));
    expect(collapsedLinks()).toBe(256);
    expect(input.value).toBe(collapsedDisplay(256));
    atEnd(input);
  });

  it("holds the suppressed state when composing shrinks and regrows the link count", async () => {
    const input = view(links(257));
    expect(collapsedLinks()).toBe(0);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    for (const text of [links(256), links(255), links(257), links(258)]) {
      type(input, text);
      expect(collapsedLinks()).toBe(0);
      expect(input.value).toBe(text);
    }
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(canonical()).toBe(links(258)));
    // 258 links still exceed the budget after composition.
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(links(258));
    atEnd(input);
  });

  it("holds the collapsed state across the mixed skill and link boundary", async () => {
    const draft = skills(128) + links(128);
    const display = skills(128) + collapsedDisplay(128);
    const input = view(draft, undefined, true);
    await waitFor(() => expect(unavailableTokens()).toBe(128));
    expect(collapsedLinks()).toBe(128);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    // Skill decoration is paused, and one more link makes 129 beside 128 skills.
    type(input, display + SMALL_LINK);
    expect(canonical()).toBe(draft + SMALL_LINK);
    expect(unavailableTokens()).toBe(0);
    expect(collapsedLinks()).toBe(128);
    expect(input.value).toBe(display + SMALL_LINK);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(unavailableTokens()).toBe(128));
    // 129 links no longer fit beside 128 painted skill ranges.
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(draft + SMALL_LINK);
    atEnd(input);
    expect(spanCount()).toBeLessThanOrEqual(512);
  });

  it("holds the plain state across the mixed boundary and collapses after composition", async () => {
    const draft = skills(128) + links(129);
    const input = view(draft, undefined, true);
    await waitFor(() => expect(unavailableTokens()).toBe(128));
    expect(collapsedLinks()).toBe(0);
    select(input, input.value.length);
    fireEvent.compositionStart(input);
    const shrunk = skills(128) + links(128);
    type(input, shrunk);
    expect(canonical()).toBe(shrunk);
    expect(unavailableTokens()).toBe(0);
    expect(collapsedLinks()).toBe(0);
    expect(input.value).toBe(shrunk);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(collapsedLinks()).toBe(128));
    expect(unavailableTokens()).toBe(128);
    expect(input.value).toBe(skills(128) + collapsedDisplay(128));
    expect(canonical()).toBe(shrunk);
    atEnd(input);
    expect(spanCount()).toBeLessThanOrEqual(512);
  });
});
