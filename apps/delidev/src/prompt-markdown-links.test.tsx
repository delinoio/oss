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
import * as externalLinks from "./external-links";
import { ExternalLinkFailure } from "./external-links";
import { buildLayout, scanMarkdown } from "./prompt-markdown-links";

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
  onRender,
}: {
  initial?: string;
  replacement?: Replacement;
  inventory?: boolean;
  /** Receives the textarea text committed by every render. */
  onRender?: (display: string) => void;
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
  onRender?.(skills.displayValue);
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
function view(
  initial?: string,
  replacement?: Replacement,
  inventory = false,
  onRender?: (display: string) => void,
) {
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
          onRender={onRender}
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

describe("IME composition at link boundaries", () => {
  const DRAFT = "a [bc](https://a) d";
  const DISPLAY = "a bc d";
  const CHARS = ["!", "[", "]", "(", ")", "\\", "あ", "![", "]("];
  const escapeLabel = (text: string) => text.replace(/[\\[\]]/gu, "\\$&");
  const insertAt = (display: string, at: number, text: string) =>
    display.slice(0, at) + text + display.slice(at);

  // Display offset of the composition and the draft it must produce.
  const PLACES = [
    {
      name: "before the link",
      at: 2,
      draft: (text: string) => `a ${text}[bc](https://a) d`,
    },
    {
      name: "inside the label",
      at: 3,
      draft: (text: string) => `a [b${escapeLabel(text)}c](https://a) d`,
    },
    {
      name: "after the link",
      at: 4,
      draft: (text: string) => `a [bc](https://a)${text} d`,
    },
  ];

  describe.each(PLACES)("composing $name", ({ at, draft }) => {
    it.each(CHARS)(
      "holds the display, selection and collapsed link for %j",
      async (char) => {
        const input = view(DRAFT);
        expect(input.value).toBe(DISPLAY);
        expect(collapsedLinks()).toBe(1);
        // A caret inside a label would reveal the source, so the composition
        // starts collapsed and edits the label from the outside.
        select(input, 0);
        fireEvent.compositionStart(input);
        const selection = vi.spyOn(input, "setSelectionRange");
        // The first update inserts the character; the next one grows it.
        const first = insertAt(DISPLAY, at, char);
        compose(input, first, at + char.length);
        expect(input.value).toBe(first);
        expect(selectionOf(input)).toEqual([
          at + char.length,
          at + char.length,
        ]);
        expect(canonical()).toBe(draft(char));
        expect(collapsedLinks()).toBe(1);
        const grown = insertAt(DISPLAY, at, `${char}あ`);
        compose(input, grown, at + char.length + 1);
        expect(input.value).toBe(grown);
        expect(selectionOf(input)).toEqual([
          at + char.length + 1,
          at + char.length + 1,
        ]);
        expect(canonical()).toBe(draft(`${char}あ`));
        expect(collapsedLinks()).toBe(1);
        // The IME owns the selection: nothing was written back to it.
        expect(selection).not.toHaveBeenCalled();
        fireEvent.compositionEnd(input);
        await waitFor(() => expect(canonical()).toBe(draft(`${char}あ`)));
        // Only now is the display re-derived from the final draft.
        const settled = canonical()!;
        expect([buildLayout(settled).display, settled]).toContain(input.value);
      },
    );

    it("keeps the link when the composition is replaced and cancelled", async () => {
      const input = view(DRAFT);
      select(input, 0);
      fireEvent.compositionStart(input);
      compose(input, insertAt(DISPLAY, at, "!"), at + 1);
      compose(input, insertAt(DISPLAY, at, "[あ"), at + 2);
      compose(input, DISPLAY, at);
      expect(input.value).toBe(DISPLAY);
      expect(canonical()).toBe(DRAFT);
      expect(collapsedLinks()).toBe(1);
      fireEvent.compositionEnd(input);
      await waitFor(() => expect(canonical()).toBe(DRAFT));
      // A caret inside the label reveals the source once composition ends.
      expect([DISPLAY, DRAFT]).toContain(input.value);
    });
  });

  it("keeps a link collapsed when composing an exclamation mark at offset 0", async () => {
    const input = view("[x](https://a)");
    expect(input.value).toBe("x");
    select(input, 0);
    fireEvent.compositionStart(input);
    compose(input, "!x", 1);
    expect(input.value).toBe("!x");
    expect(selectionOf(input)).toEqual([1, 1]);
    expect(canonical()).toBe("![x](https://a)");
    expect(collapsedLinks()).toBe(1);
    compose(input, "!あx", 2);
    expect(input.value).toBe("!あx");
    expect(canonical()).toBe("!あ[x](https://a)");
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(collapsedLinks()).toBe(1));
    expect(input.value).toBe("!あx");
  });

  it("shows the image source once composition ends when the link became an image", async () => {
    const input = view("[x](https://a)");
    select(input, 0);
    fireEvent.compositionStart(input);
    compose(input, "!x", 1);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(collapsedLinks()).toBe(0));
    expect(canonical()).toBe("![x](https://a)");
    expect(input.value).toBe("![x](https://a)");
  });

  it("keeps the other links collapsed while one is edited", async () => {
    const draft = "[ab](https://a) and [cd](https://b)";
    const input = view(draft);
    expect(input.value).toBe("ab and cd");
    select(input, 0);
    fireEvent.compositionStart(input);
    compose(input, "a]b and cd", 2);
    expect(input.value).toBe("a]b and cd");
    expect(canonical()).toBe("[a\\]b](https://a) and [cd](https://b)");
    expect(collapsedLinks()).toBe(2);
    compose(input, "a]b and c(d", 11);
    expect(input.value).toBe("a]b and c(d");
    expect(canonical()).toBe("[a\\]b](https://a) and [c(d](https://b)");
    expect(collapsedLinks()).toBe(2);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(collapsedLinks()).toBe(2));
    expect(input.value).toBe("a]b and c(d");
  });

  it("deletes label characters while composing and drops a link with no label", async () => {
    const input = view(DRAFT);
    select(input, 0);
    fireEvent.compositionStart(input);
    compose(input, "a b d", 3);
    expect(input.value).toBe("a b d");
    expect(canonical()).toBe("a [b](https://a) d");
    expect(collapsedLinks()).toBe(1);
    compose(input, "a  d", 2);
    expect(input.value).toBe("a  d");
    expect(canonical()).toBe("a  d");
    expect(collapsedLinks()).toBe(0);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(canonical()).toBe("a  d"));
    expect(input.value).toBe("a  d");
  });

  it("replaces a selection that crosses a link edge without rewriting the textarea", async () => {
    const input = view(DRAFT);
    select(input, 0);
    fireEvent.compositionStart(input);
    compose(input, "aあc d", 2);
    expect(input.value).toBe("aあc d");
    expect(canonical()).toBe("aあ[c](https://a) d");
    expect(collapsedLinks()).toBe(1);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(canonical()).toBe("aあ[c](https://a) d"));
    expect(input.value).toBe("aあc d");
  });

  describe.each(CHARS)("composing %j inside a revealed link source", (char) => {
    const SOURCE = "[x](https://a)";
    it.each([
      ["after the opening bracket", 1],
      ["in the label end", 2],
      ["in the url", 6],
      ["before the closing parenthesis", SOURCE.length - 1],
    ])("%s", async (_name, at) => {
      const input = view(SOURCE);
      expect(input.value).toBe("x");
      // Selecting the label reveals the full source for editing.
      select(input, 0, 1);
      expect(input.value).toBe(SOURCE);
      select(input, at);
      const shown = input.value;
      fireEvent.compositionStart(input);
      const selection = vi.spyOn(input, "setSelectionRange");
      const next = insertAt(shown, at, char);
      compose(input, next, at + char.length);
      expect(input.value).toBe(next);
      expect(selectionOf(input)).toEqual([at + char.length, at + char.length]);
      expect(canonical()).toBe(next);
      const grown = insertAt(shown, at, `${char}あ`);
      compose(input, grown, at + char.length + 1);
      expect(input.value).toBe(grown);
      expect(canonical()).toBe(grown);
      expect(selection).not.toHaveBeenCalled();
      fireEvent.compositionEnd(input);
      await waitFor(() => expect(canonical()).toBe(grown));
    });
  });
});

// The end-of-composition transition for every combination of the link state
// when composition starts, where the composed edit lands and what the edit does
// to the link. A composed edit never rewrites the textarea; when composition
// ends the final draft is parsed once, the revealed set is derived from that
// parse and the canonical selection, and the display settles in one write.
enum LinkStart {
  /** The link is collapsed to its label. */
  Collapsed = "collapsed",
  /** The caret is inside the link, so its source is shown. */
  Revealed = "revealed",
  /** No link exists in the draft yet. */
  Absent = "absent",
}
enum EditWhere {
  Before = "before",
  Label = "label",
  Url = "url",
  After = "after",
  Spanning = "spanning",
}
enum EditEffect {
  /** The link is still a valid link (or a new valid link appears). */
  Valid = "valid",
  /** The source no longer parses as a link or an image. */
  Invalid = "invalid",
  /** The source parses as an image. */
  Image = "image",
  /** Another link-shaped region swallows the link. */
  Merged = "merged",
}

interface ComposedEdit {
  /** Display offset where the composed edit starts. */
  at: number;
  /** Display characters the IME replaced. */
  remove: number;
  insert: string;
}
interface MatrixCell {
  start: LinkStart;
  where: EditWhere;
  effect: EditEffect;
  /** Why this combination cannot occur; absent for a tested scenario. */
  unreachable?: string;
  draft?: string;
  /** Collapsed-display offset whose caret reveals the link (Revealed). */
  reveal?: number;
  /** Textarea selection when composition starts. */
  from?: [number, number];
  /** Textarea text when composition starts, if it differs from the draft. */
  shown?: string;
  edit?: ComposedEdit;
  /** Draft right after the composed edit, before composition ends. */
  composed?: string;
  /** State after composition ends. */
  settled?: { display: string; selection: [number, number]; links: number };
}

const BASE = "a [bc](https://a) d";
const WRAPPED = "a [bc](https://a) )";
const NESTED = "a [p [bc](https://a) d";
const NO_OPENER = "a bc](https://a) d";
const NO_OPENER_WRAPPED = "a bc](https://a) )";
const NO_CLOSER = "a [bc](https://a d";
const NO_CLOSER_IMAGE = "a ![bc](https://a d";
const BROKEN_LABEL = "a [b\nc](https://a) d";
const BROKEN_IMAGE_LABEL = "a ![b\nc](https://a) d";
const SWALLOW_URL = "a [bc](https://a)[de](https://b))";
const SWALLOW_OPENER = "a [bc](https://a [de](https://b) d";
const OUTER = "[p](";

function reachable(
  start: LinkStart,
  where: EditWhere,
  effect: EditEffect,
  cell: Omit<MatrixCell, "start" | "where" | "effect" | "unreachable">,
): MatrixCell {
  return { start, where, effect, ...cell };
}
function unreachable(
  start: LinkStart,
  where: EditWhere,
  effect: EditEffect,
  reason: string,
): MatrixCell {
  return { start, where, effect, unreachable: reason };
}

const HIDDEN_URL =
  "A collapsed link hides its destination: no edit lands in it.";
const MARKER_OUTSIDE =
  "An image marker must directly precede the opening bracket, outside the label and the destination.";
const LABEL_ESCAPES =
  "A label edit changes only its own link: inserted delimiters are escaped and joining links needs an edit across their boundaries (see spanning).";
const AFTER_IS_INERT =
  "Text after the closing parenthesis cannot change how an earlier link parses.";

const MATRIX: MatrixCell[] = [
  // Collapsed: the caret is outside the link, which shows only its label.
  reachable(LinkStart.Collapsed, EditWhere.Before, EditEffect.Valid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "あ" },
    composed: "a あ[bc](https://a) d",
    settled: { display: "a あbc d", selection: [3, 3], links: 1 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Before, EditEffect.Invalid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "\\" },
    composed: "a \\[bc](https://a) d",
    settled: { display: "a \\[bc](https://a) d", selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Before, EditEffect.Image, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "!" },
    composed: "a ![bc](https://a) d",
    settled: { display: "a ![bc](https://a) d", selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Before, EditEffect.Merged, {
    draft: WRAPPED,
    shown: "a bc )",
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: OUTER },
    composed: "a [p]([bc](https://a) )",
    settled: {
      display: "a [p]([bc](https://a) )",
      selection: [6, 6],
      links: 0,
    },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Label, EditEffect.Valid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 3, remove: 0, insert: "あ" },
    composed: "a [bあc](https://a) d",
    // The caret ends up inside the label, which reveals the source once.
    settled: { display: "a [bあc](https://a) d", selection: [5, 5], links: 0 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Label, EditEffect.Invalid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 3, remove: 0, insert: "\n" },
    composed: "a [b\nc](https://a) d",
    settled: { display: "a [b\nc](https://a) d", selection: [5, 5], links: 0 },
  }),
  unreachable(
    LinkStart.Collapsed,
    EditWhere.Label,
    EditEffect.Image,
    MARKER_OUTSIDE,
  ),
  unreachable(
    LinkStart.Collapsed,
    EditWhere.Label,
    EditEffect.Merged,
    LABEL_ESCAPES,
  ),
  ...[
    EditEffect.Valid,
    EditEffect.Invalid,
    EditEffect.Image,
    EditEffect.Merged,
  ].map((effect) =>
    unreachable(LinkStart.Collapsed, EditWhere.Url, effect, HIDDEN_URL),
  ),
  reachable(LinkStart.Collapsed, EditWhere.After, EditEffect.Valid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 4, remove: 0, insert: "あ" },
    composed: "a [bc](https://a)あ d",
    settled: { display: "a bcあ d", selection: [5, 5], links: 1 },
  }),
  unreachable(
    LinkStart.Collapsed,
    EditWhere.After,
    EditEffect.Invalid,
    AFTER_IS_INERT,
  ),
  unreachable(
    LinkStart.Collapsed,
    EditWhere.After,
    EditEffect.Image,
    MARKER_OUTSIDE,
  ),
  reachable(LinkStart.Collapsed, EditWhere.After, EditEffect.Merged, {
    draft: NESTED,
    shown: "a [p bc d",
    from: [0, 0],
    edit: { at: 7, remove: 0, insert: "](https://z)" },
    composed: "a [p [bc](https://a)](https://z) d",
    settled: {
      display: "a [p [bc](https://a)](https://z) d",
      selection: [32, 32],
      links: 0,
    },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Spanning, EditEffect.Valid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "あ" },
    composed: "aあ[c](https://a) d",
    settled: { display: "aあc d", selection: [2, 2], links: 1 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Spanning, EditEffect.Invalid, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "\\" },
    composed: "a\\[c](https://a) d",
    settled: { display: "a\\[c](https://a) d", selection: [2, 2], links: 0 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Spanning, EditEffect.Image, {
    draft: BASE,
    shown: "a bc d",
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "!" },
    composed: "a![c](https://a) d",
    settled: { display: "a![c](https://a) d", selection: [2, 2], links: 0 },
  }),
  reachable(LinkStart.Collapsed, EditWhere.Spanning, EditEffect.Merged, {
    draft: WRAPPED,
    shown: "a bc )",
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "X[p](" },
    composed: "aX[p]([c](https://a) )",
    settled: { display: "aX[p]([c](https://a) )", selection: [6, 6], links: 0 },
  }),

  // Revealed: the caret is inside the link, which shows its source.
  reachable(LinkStart.Revealed, EditWhere.Before, EditEffect.Valid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 2, remove: 0, insert: "あ" },
    composed: "a あ[bc](https://a) d",
    // The edit moved the caret out of the link, so it collapses.
    settled: { display: "a あbc d", selection: [3, 3], links: 1 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Before, EditEffect.Invalid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 2, remove: 0, insert: "\\" },
    composed: "a \\[bc](https://a) d",
    settled: { display: "a \\[bc](https://a) d", selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Before, EditEffect.Image, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 2, remove: 0, insert: "!" },
    composed: "a ![bc](https://a) d",
    settled: { display: "a ![bc](https://a) d", selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Before, EditEffect.Merged, {
    draft: WRAPPED,
    reveal: 3,
    from: [4, 4],
    edit: { at: 2, remove: 0, insert: OUTER },
    composed: "a [p]([bc](https://a) )",
    settled: {
      display: "a [p]([bc](https://a) )",
      selection: [6, 6],
      links: 0,
    },
  }),
  reachable(LinkStart.Revealed, EditWhere.Label, EditEffect.Valid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 4, remove: 0, insert: "!" },
    composed: "a [b!c](https://a) d",
    // The caret is still inside the valid link: it stays revealed.
    settled: { display: "a [b!c](https://a) d", selection: [5, 5], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Label, EditEffect.Invalid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 4, remove: 0, insert: "]" },
    composed: "a [b]c](https://a) d",
    settled: { display: "a [b]c](https://a) d", selection: [5, 5], links: 0 },
  }),
  unreachable(
    LinkStart.Revealed,
    EditWhere.Label,
    EditEffect.Image,
    MARKER_OUTSIDE,
  ),
  unreachable(
    LinkStart.Revealed,
    EditWhere.Label,
    EditEffect.Merged,
    LABEL_ESCAPES,
  ),
  reachable(LinkStart.Revealed, EditWhere.Url, EditEffect.Valid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 16, remove: 0, insert: "/x" },
    composed: "a [bc](https://a/x) d",
    settled: {
      display: "a [bc](https://a/x) d",
      selection: [18, 18],
      links: 0,
    },
  }),
  reachable(LinkStart.Revealed, EditWhere.Url, EditEffect.Invalid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 16, remove: 0, insert: " " },
    composed: "a [bc](https://a ) d",
    settled: { display: "a [bc](https://a ) d", selection: [17, 17], links: 0 },
  }),
  unreachable(
    LinkStart.Revealed,
    EditWhere.Url,
    EditEffect.Image,
    MARKER_OUTSIDE,
  ),
  reachable(LinkStart.Revealed, EditWhere.Url, EditEffect.Merged, {
    // Deleting the closing parenthesis lets the destination swallow the next
    // link, which was collapsed when composition started.
    draft: SWALLOW_URL,
    reveal: 3,
    from: [4, 4],
    shown: "a [bc](https://a)de)",
    edit: { at: 16, remove: 1, insert: "" },
    composed: "a [bc](https://a[de](https://b))",
    settled: {
      display: "a [bc](https://a[de](https://b))",
      selection: [16, 16],
      links: 0,
    },
  }),
  reachable(LinkStart.Revealed, EditWhere.After, EditEffect.Valid, {
    draft: BASE,
    reveal: 3,
    from: [4, 4],
    edit: { at: 17, remove: 0, insert: "あ" },
    composed: "a [bc](https://a)あ d",
    settled: { display: "a bcあ d", selection: [5, 5], links: 1 },
  }),
  unreachable(
    LinkStart.Revealed,
    EditWhere.After,
    EditEffect.Invalid,
    AFTER_IS_INERT,
  ),
  unreachable(
    LinkStart.Revealed,
    EditWhere.After,
    EditEffect.Image,
    MARKER_OUTSIDE,
  ),
  reachable(LinkStart.Revealed, EditWhere.After, EditEffect.Merged, {
    draft: NESTED,
    reveal: 6,
    from: [7, 7],
    edit: { at: 20, remove: 0, insert: "](https://z)" },
    composed: "a [p [bc](https://a)](https://z) d",
    settled: {
      display: "a [p [bc](https://a)](https://z) d",
      selection: [32, 32],
      links: 0,
    },
  }),
  reachable(LinkStart.Revealed, EditWhere.Spanning, EditEffect.Valid, {
    draft: BASE,
    reveal: 3,
    from: [1, 4],
    edit: { at: 1, remove: 3, insert: "X[B" },
    composed: "aX[Bc](https://a) d",
    settled: { display: "aX[Bc](https://a) d", selection: [4, 4], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Spanning, EditEffect.Invalid, {
    draft: BASE,
    reveal: 3,
    from: [1, 4],
    edit: { at: 1, remove: 3, insert: "あ" },
    composed: "aあc](https://a) d",
    settled: { display: "aあc](https://a) d", selection: [2, 2], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Spanning, EditEffect.Image, {
    draft: BASE,
    reveal: 3,
    from: [1, 4],
    edit: { at: 1, remove: 3, insert: "X![B" },
    composed: "aX![Bc](https://a) d",
    settled: { display: "aX![Bc](https://a) d", selection: [5, 5], links: 0 },
  }),
  reachable(LinkStart.Revealed, EditWhere.Spanning, EditEffect.Merged, {
    draft: WRAPPED,
    reveal: 3,
    from: [1, 4],
    edit: { at: 1, remove: 3, insert: "X[p]([B" },
    composed: "aX[p]([Bc](https://a) )",
    settled: {
      display: "aX[p]([Bc](https://a) )",
      selection: [8, 8],
      links: 0,
    },
  }),

  // Absent: the draft has no link when composition starts.
  reachable(LinkStart.Absent, EditWhere.Before, EditEffect.Valid, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "[" },
    composed: BASE,
    // The new link holds the caret, so it is revealed rather than collapsed.
    settled: { display: BASE, selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Before, EditEffect.Invalid, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "x" },
    composed: "a xbc](https://a) d",
    settled: { display: "a xbc](https://a) d", selection: [3, 3], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Before, EditEffect.Image, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "![" },
    composed: "a ![bc](https://a) d",
    settled: { display: "a ![bc](https://a) d", selection: [4, 4], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Before, EditEffect.Merged, {
    draft: "a )",
    from: [0, 0],
    edit: { at: 2, remove: 0, insert: "[p]([y](https://z) " },
    composed: "a [p]([y](https://z) )",
    settled: {
      display: "a [p]([y](https://z) )",
      selection: [21, 21],
      links: 0,
    },
  }),
  reachable(LinkStart.Absent, EditWhere.Label, EditEffect.Valid, {
    draft: BROKEN_LABEL,
    from: [0, 0],
    edit: { at: 4, remove: 1, insert: "" },
    composed: BASE,
    settled: { display: BASE, selection: [4, 4], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Label, EditEffect.Invalid, {
    draft: BROKEN_LABEL,
    from: [0, 0],
    edit: { at: 4, remove: 0, insert: "x" },
    composed: "a [bx\nc](https://a) d",
    settled: { display: "a [bx\nc](https://a) d", selection: [5, 5], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Label, EditEffect.Image, {
    draft: BROKEN_IMAGE_LABEL,
    from: [0, 0],
    edit: { at: 5, remove: 1, insert: "" },
    composed: "a ![bc](https://a) d",
    settled: { display: "a ![bc](https://a) d", selection: [5, 5], links: 0 },
  }),
  unreachable(
    LinkStart.Absent,
    EditWhere.Label,
    EditEffect.Merged,
    "With no link present, a label edit has no second region to join or swallow.",
  ),
  reachable(LinkStart.Absent, EditWhere.Url, EditEffect.Valid, {
    draft: NO_CLOSER,
    from: [0, 0],
    edit: { at: 16, remove: 0, insert: ")" },
    composed: BASE,
    // The caret ends on the new link's edge: it collapses once.
    settled: { display: "a bc d", selection: [4, 4], links: 1 },
  }),
  reachable(LinkStart.Absent, EditWhere.Url, EditEffect.Invalid, {
    draft: NO_CLOSER,
    from: [0, 0],
    edit: { at: 16, remove: 0, insert: "x" },
    composed: "a [bc](https://ax d",
    settled: { display: "a [bc](https://ax d", selection: [17, 17], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Url, EditEffect.Image, {
    draft: NO_CLOSER_IMAGE,
    from: [0, 0],
    edit: { at: 17, remove: 0, insert: ")" },
    composed: "a ![bc](https://a) d",
    settled: { display: "a ![bc](https://a) d", selection: [18, 18], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Url, EditEffect.Merged, {
    // Closing the first destination swallows the collapsed link inside it.
    draft: SWALLOW_OPENER,
    shown: "a [bc](https://a de d",
    from: [0, 0],
    edit: { at: 21, remove: 0, insert: ")" },
    composed: "a [bc](https://a [de](https://b) d)",
    settled: {
      display: "a [bc](https://a [de](https://b) d)",
      selection: [35, 35],
      links: 0,
    },
  }),
  reachable(LinkStart.Absent, EditWhere.After, EditEffect.Valid, {
    draft: "a bc d",
    from: [0, 0],
    edit: { at: 6, remove: 0, insert: "[x](https://z)" },
    composed: "a bc d[x](https://z)",
    settled: { display: "a bc dx", selection: [7, 7], links: 1 },
  }),
  reachable(LinkStart.Absent, EditWhere.After, EditEffect.Invalid, {
    draft: "a bc d",
    from: [0, 0],
    edit: { at: 6, remove: 0, insert: "[x](https://z" },
    composed: "a bc d[x](https://z",
    settled: { display: "a bc d[x](https://z", selection: [19, 19], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.After, EditEffect.Image, {
    draft: "a bc d",
    from: [0, 0],
    edit: { at: 6, remove: 0, insert: "![x](https://z)" },
    composed: "a bc d![x](https://z)",
    settled: {
      display: "a bc d![x](https://z)",
      selection: [21, 21],
      links: 0,
    },
  }),
  reachable(LinkStart.Absent, EditWhere.After, EditEffect.Merged, {
    draft: "a [p b",
    from: [0, 0],
    edit: { at: 6, remove: 0, insert: "[x](https://z)](https://q)" },
    composed: "a [p b[x](https://z)](https://q)",
    settled: {
      display: "a [p b[x](https://z)](https://q)",
      selection: [32, 32],
      links: 0,
    },
  }),
  reachable(LinkStart.Absent, EditWhere.Spanning, EditEffect.Valid, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "X[B" },
    composed: "aX[Bc](https://a) d",
    settled: { display: "aX[Bc](https://a) d", selection: [4, 4], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Spanning, EditEffect.Invalid, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "あ" },
    composed: "aあc](https://a) d",
    settled: { display: "aあc](https://a) d", selection: [2, 2], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Spanning, EditEffect.Image, {
    draft: NO_OPENER,
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "X![B" },
    composed: "aX![Bc](https://a) d",
    settled: { display: "aX![Bc](https://a) d", selection: [5, 5], links: 0 },
  }),
  reachable(LinkStart.Absent, EditWhere.Spanning, EditEffect.Merged, {
    draft: NO_OPENER_WRAPPED,
    from: [0, 0],
    edit: { at: 1, remove: 2, insert: "X[p]([B" },
    composed: "aX[p]([Bc](https://a) )",
    settled: {
      display: "aX[p]([Bc](https://a) )",
      selection: [8, 8],
      links: 0,
    },
  }),
];

function compose(input: HTMLTextAreaElement, display: string, caret: number) {
  fireEvent.change(input, {
    target: { value: display, selectionStart: caret, selectionEnd: caret },
  });
}

describe("composition end matrix", () => {
  const cellName = (cell: MatrixCell) =>
    `${cell.start} link, edit ${cell.where}, ${cell.effect}`;
  const cells = MATRIX.filter((cell) => !cell.unreachable);

  it("covers every combination exactly once", () => {
    const keys = MATRIX.map(
      (cell) => `${cell.start}/${cell.where}/${cell.effect}`,
    );
    expect(new Set(keys).size).toBe(keys.length);
    const all = (values: object) => Object.values(values) as string[];
    const expected = all(LinkStart).flatMap((start) =>
      all(EditWhere).flatMap((where) =>
        all(EditEffect).map((effect) => `${start}/${where}/${effect}`),
      ),
    );
    expect([...keys].sort()).toEqual([...expected].sort());
    for (const cell of MATRIX)
      expect(cell.unreachable ?? cell.settled).toBeTruthy();
    expect(cells.length).toBeGreaterThanOrEqual(40);
  });

  it.each(cells)(
    "settles once: $start link, edit $where, $effect",
    async (cell) => {
      const validate = vi.spyOn(externalLinks, "validHttpUrl");
      try {
        const renders: string[] = [];
        const input = view(cell.draft, undefined, false, (display) =>
          renders.push(display),
        );
        const links = scanMarkdown(cell.draft!).links;
        expect(collapsedLinks()).toBe(
          cell.start === LinkStart.Collapsed ? 1 : links.length,
        );
        if (cell.reveal !== undefined) {
          select(input, cell.reveal);
          expect(collapsedLinks()).toBeLessThan(links.length);
        }
        select(input, ...cell.from!);
        const before = input.value;
        expect(before).toBe(cell.shown ?? cell.draft);

        // Composition: the IME owns the text and the selection.
        fireEvent.compositionStart(input);
        const written = vi.spyOn(input, "setSelectionRange");
        const rendered = renders.length;
        validate.mockClear();
        const { at, remove, insert } = cell.edit!;
        const display =
          before.slice(0, at) + insert + before.slice(at + remove);
        const caret = at + insert.length;
        compose(input, display, caret);
        expect(input.value).toBe(display);
        expect(selectionOf(input)).toEqual([caret, caret]);
        expect(canonical()).toBe(cell.composed);
        expect(written).not.toHaveBeenCalled();
        expect(validate).not.toHaveBeenCalled();
        // The textarea was never given any text but what the IME produced.
        expect(new Set(renders.slice(rendered))).toEqual(new Set([display]));

        // Composition ends: one parse, one settled display, no bounce.
        const settled = cell.settled!;
        fireEvent.compositionEnd(input);
        await waitFor(() => expect(input.value).toBe(settled.display));
        expect(canonical()).toBe(cell.composed);
        expect(selectionOf(input)).toEqual(settled.selection);
        expect(collapsedLinks()).toBe(settled.links);
        // Exactly one parse of the final draft, however many renders followed.
        const parsed = validate.mock.calls.length;
        validate.mockClear();
        scanMarkdown(cell.composed!);
        expect(parsed).toBe(validate.mock.calls.length);
        const sequence = [display, ...renders.slice(rendered)].filter(
          (value, index, all) => index === 0 || value !== all[index - 1],
        );
        expect(sequence).toEqual(
          display === settled.display ? [display] : [display, settled.display],
        );
        // The browser reports the restored selection: nothing changes again.
        select(input, ...settled.selection);
        await act(async () => {});
        expect(input.value).toBe(settled.display);
        expect(canonical()).toBe(cell.composed);
        expect(selectionOf(input)).toEqual(settled.selection);
        expect(collapsedLinks()).toBe(settled.links);
        expect(
          renders
            .slice(rendered)
            .filter(
              (value, index, all) => index === 0 || value !== all[index - 1],
            ).length,
        ).toBeLessThanOrEqual(2);
      } finally {
        validate.mockRestore();
      }
    },
  );
});

describe("revealed link through composition end", () => {
  const SOURCE = "[x](https://a)";

  function revealed(initial: string, renders: string[]) {
    const input = view(initial, undefined, false, (display) =>
      renders.push(display),
    );
    // Selecting the label reveals the source for editing.
    select(input, 0, 1);
    expect(input.value).toBe(initial);
    expect(collapsedLinks()).toBe(0);
    return input;
  }

  it("keeps a revealed link revealed while an IME grows its label", async () => {
    const validate = vi.spyOn(externalLinks, "validHttpUrl");
    try {
      const renders: string[] = [];
      const input = revealed(SOURCE, renders);
      select(input, 2);
      fireEvent.compositionStart(input);
      const written = vi.spyOn(input, "setSelectionRange");
      validate.mockClear();
      // The first update inserts "!", the next one replaces it with "!あ".
      compose(input, "[x!](https://a)", 3);
      compose(input, "[x!あ](https://a)", 4);
      expect(canonical()).toBe("[x!あ](https://a)");
      expect(input.value).toBe("[x!あ](https://a)");
      expect(selectionOf(input)).toEqual([4, 4]);
      expect(written).not.toHaveBeenCalled();
      expect(validate).not.toHaveBeenCalled();
      const rendered = renders.length;
      fireEvent.compositionEnd(input);
      await act(async () => {});
      // The link is still valid and holds the caret: nothing changes.
      expect(input.value).toBe("[x!あ](https://a)");
      expect(collapsedLinks()).toBe(0);
      expect(selectionOf(input)).toEqual([4, 4]);
      expect(new Set(renders.slice(rendered))).toEqual(
        new Set(["[x!あ](https://a)"]),
      );
      expect(validate).toHaveBeenCalledTimes(1);
      // The caret settling in the browser does not reveal or collapse again.
      select(input, 4);
      await act(async () => {});
      expect(input.value).toBe("[x!あ](https://a)");
      expect(collapsedLinks()).toBe(0);
      // Leaving the link collapses it as usual.
      select(input, 0);
      expect(input.value).toBe("x!あ");
    } finally {
      validate.mockRestore();
    }
  });

  it("parses the final draft once however many links it holds", async () => {
    const validate = vi.spyOn(externalLinks, "validHttpUrl");
    try {
      const draft = `${SOURCE} `.repeat(32);
      const input = view(draft);
      expect(collapsedLinks()).toBe(32);
      select(input, 0, 1);
      select(input, 2);
      const shown = input.value;
      expect(collapsedLinks()).toBe(31);
      fireEvent.compositionStart(input);
      validate.mockClear();
      for (const text of ["!", "!あ", "!あい"]) {
        const display = `${shown.slice(0, 2)}${text}${shown.slice(2)}`;
        compose(input, display, 2 + text.length);
        expect(input.value).toBe(display);
      }
      expect(validate).not.toHaveBeenCalled();
      fireEvent.compositionEnd(input);
      await act(async () => {});
      expect(validate).toHaveBeenCalledTimes(32);
      expect(collapsedLinks()).toBe(31);
    } finally {
      validate.mockRestore();
    }
  });

  it("keeps the revealed link when the composition is cancelled", async () => {
    const renders: string[] = [];
    const input = revealed(SOURCE, renders);
    select(input, 2);
    fireEvent.compositionStart(input);
    compose(input, "[x!](https://a)", 3);
    compose(input, SOURCE, 2);
    expect(canonical()).toBe(SOURCE);
    const rendered = renders.length;
    fireEvent.compositionEnd(input);
    await act(async () => {});
    expect(input.value).toBe(SOURCE);
    expect(collapsedLinks()).toBe(0);
    expect(selectionOf(input)).toEqual([2, 2]);
    expect(new Set(renders.slice(rendered))).toEqual(new Set([SOURCE]));
  });

  it("collapses a revealed link once when the composition ends outside it", async () => {
    const renders: string[] = [];
    const input = revealed(`${SOURCE} z`, renders);
    select(input, 2);
    fireEvent.compositionStart(input);
    compose(input, `${SOURCE}あ z`, SOURCE.length + 1);
    expect(input.value).toBe(`${SOURCE}あ z`);
    fireEvent.compositionEnd(input);
    await waitFor(() => expect(input.value).toBe("xあ z"));
    expect(collapsedLinks()).toBe(1);
    expect(selectionOf(input)).toEqual([2, 2]);
    expect(canonical()).toBe(`${SOURCE}あ z`);
  });
});
