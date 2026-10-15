// SPDX-License-Identifier: Apache-2.0
// One test per row of the "Prompt composer Markdown links" state table in the
// desktop contract. Each row asserts the canonical text, displayed text,
// selection mapping, undo-stack effect and binding offsets.
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  SkillService,
  SkillProvenance,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { useSkillCompletion, type SkillTokenBinding } from "./skill-completion";
import { imageEntryHandlers } from "./image-attachments";
import { copy } from "./localization";
import {
  projectMarkdown,
  scanMarkdown,
  selectionRevealsLink,
} from "./prompt-markdown-links";

const native = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}));
vi.mock("@tauri-apps/api/core", () => native);

const machine = newRequestId(),
  agent = newRequestId(),
  inventory = newRequestId(),
  worker = newRequestId();
const entries = ["add-issue", "add-note"].map((name) => ({
  name,
  description: `${name} fixture`,
  provenance: SkillProvenance.USER,
  selection: {
    $typeName: "delidev.v1.SkillSelection" as const,
    inventoryId: inventory,
    workerDeviceId: worker,
    skillId: newRequestId(),
    contentRevision: "a".repeat(64),
  },
}));

const LINK = "[Docs](https://e.com/d)";
const DRAFT = `see ${LINK} ok`;
const DISPLAY = "see Docs ok";

interface Probe {
  canonical: string;
  changes: number;
  bindings: SkillTokenBinding[];
  api: ReturnType<typeof useSkillCompletion>;
  selections: unknown[];
  /** Every text the owner was offered, in order, with its verdict. */
  attempts: { text: string; accepted: boolean }[];
}
let probe: Probe;
let outsideBlocked = false;

function Harness({
  initial,
  limit,
  attach,
}: {
  initial: string;
  limit?: number;
  attach?: (files: File[]) => void;
}) {
  const [value, setValue] = useState(initial);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change: (next) => {
      if (
        limit !== undefined &&
        new TextEncoder().encode(next).byteLength > limit
      ) {
        probe.attempts.push({ text: next, accepted: false });
        return false;
      }
      probe.attempts.push({ text: next, accepted: true });
      probe.changes += 1;
      setValue(next);
    },
    textarea,
    machineId: machine,
    agentId: agent,
    bindingsChanged: (bindings) => {
      probe.bindings = bindings;
    },
  });
  probe.canonical = value;
  probe.api = skills;
  probe.selections = skills.selections;
  const handlers = imageEntryHandlers(
    {
      controller: { add: (files: File[]) => attach?.(files) },
    } as never,
    outsideBlocked,
  );
  return (
    <form {...handlers}>
      {skills.wrap(
        <textarea
          aria-label="Message"
          ref={textarea}
          value={skills.value}
          {...skills.input}
          onChange={(event) =>
            skills.onChange(event.target.value, event.target.selectionStart)
          }
          onSelect={skills.onSelect}
          onKeyDown={skills.onKeyDown}
          onCompositionStart={skills.onCompositionStart}
          onCompositionEnd={skills.onCompositionEnd}
          {...skills.attributes}
        />,
      )}
      {skills.list}
      {skills.warning}
      <button type="button" onClick={() => setValue("")}>
        Reset
      </button>
    </form>
  );
}

function fixture() {
  const read = vi.fn(async () => ({ skills: entries }));
  const transport = createRouterTransport((router) =>
    router.service(SkillService, { listSkills: read }),
  );
  const client = new QueryClient();
  return (
    initial: string,
    props: { limit?: number; attach?: (files: File[]) => void } = {},
  ) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Harness initial={initial} {...props} />
      </QueryClientProvider>
    </TransportProvider>
  );
}

beforeEach(() => {
  probe = {
    canonical: "",
    changes: 0,
    bindings: [],
    api: undefined as never,
    selections: [],
    attempts: [],
  };
  outsideBlocked = false;
  native.invoke.mockReset();
  native.isTauri.mockReturnValue(true);
  Object.defineProperty(window.navigator, "platform", {
    value: "Linux x86_64",
    configurable: true,
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const field = () => screen.getByRole("textbox") as HTMLTextAreaElement;
function mount(initial = DRAFT, props: { limit?: number } = {}) {
  const view = render(fixture()(initial, props));
  const input = field();
  return { view, input };
}
// Browsers report every caret move through `selectionchange`; React's select
// plugin deduplicates by that stream, so the helpers emit it as well.
function reportSelection() {
  fireEvent(document, new Event("selectionchange"));
}
function select(
  input: HTMLTextAreaElement,
  start: number,
  end = start,
  direction: "forward" | "backward" | "none" = "none",
) {
  input.focus();
  input.setSelectionRange(start, end, direction);
  fireEvent.select(input);
  reportSelection();
}
// Mirrors a native edit: the field text and caret change, then React sees it.
function nativeEdit(input: HTMLTextAreaElement, text: string, caret: number) {
  fireEvent.change(input, {
    target: { value: text, selectionStart: caret, selectionEnd: caret },
  });
  reportSelection();
}
function paste(
  input: HTMLTextAreaElement,
  text: string,
  files: File[] = [],
): boolean {
  return fireEvent.paste(input, {
    clipboardData: { files, getData: () => text },
  });
}
const undo = (input: HTMLTextAreaElement) =>
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
const redo = (input: HTMLTextAreaElement) =>
  fireEvent.keyDown(input, { key: "z", ctrlKey: true, shiftKey: true });
const bindingTexts = () =>
  probe.bindings.map((binding): [number, number, string] => [
    binding.start,
    binding.end,
    probe.canonical.slice(binding.start, binding.end),
  ]);

// Builds `$add-issue see [Docs](...) ok $add-note ` with two accepted bindings
// so offsets can be checked on both sides of the collapsed link.
async function boundDraft() {
  const view = render(fixture()(""));
  const input = field();
  nativeEdit(input, "$add-iss", 8);
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Tab" });
  nativeEdit(input, `$add-issue ${DRAFT} $add-no`, 11 + DRAFT.length + 8 + 0);
  await screen.findByRole("option", { name: /add-note/ });
  fireEvent.keyDown(input, { key: "Tab" });
  expect(probe.canonical).toBe(`$add-issue ${DRAFT} $add-note `);
  return { view, input };
}

// --- Rows ------------------------------------------------------------------

it("R01 type: edits map through the link, coalesce into one undo step and keep selection", () => {
  const { input } = mount();
  expect(input.value).toBe(DISPLAY);
  select(input, DISPLAY.length);
  nativeEdit(input, `${DISPLAY}x`, DISPLAY.length + 1);
  nativeEdit(input, `${DISPLAY}xy`, DISPLAY.length + 2);
  expect(probe.canonical).toBe(`${DRAFT}xy`);
  expect(input.value).toBe(`${DISPLAY}xy`);
  expect(input.selectionStart).toBe(DISPLAY.length + 2);
  expect(probe.api.readCanonical().start).toBe(DRAFT.length + 2);
  expect(probe.bindings).toEqual([]);
  undo(input);
  expect(probe.canonical).toBe(DRAFT);
  expect(input.value).toBe(DISPLAY);
  expect(input.selectionStart).toBe(DISPLAY.length);
  redo(input);
  expect(probe.canonical).toBe(`${DRAFT}xy`);
  expect(input.selectionStart).toBe(DISPLAY.length + 2);
});

it("R01 type: typing right after a collapsed link edits outside it and keeps it collapsed", () => {
  const { input } = mount(`${LINK}`);
  expect(input.value).toBe("Docs");
  select(input, 4);
  nativeEdit(input, "Docsx", 5);
  expect(probe.canonical).toBe(`${LINK}x`);
  expect(input.value).toBe("Docsx");
  select(input, 0);
  nativeEdit(input, "yDocsx", 1);
  expect(probe.canonical).toBe(`y${LINK}x`);
  expect(input.value).toBe("yDocsx");
});

it("R02 delimiter: a typed space or newline converts exactly the finished URL as one undo step", () => {
  const { input } = mount("go ");
  nativeEdit(input, "go https://a.test/x", 19);
  expect(probe.canonical).toBe("go https://a.test/x");
  expect(input.value).toBe("go https://a.test/x");
  nativeEdit(input, "go https://a.test/x ", 20);
  const converted = "go [https://a.test/x](https://a.test/x) ";
  expect(probe.canonical).toBe(converted);
  expect(input.value).toBe("go https://a.test/x ");
  expect(probe.api.readCanonical().start).toBe(converted.length);
  expect(input.selectionStart).toBe(20);
  undo(input);
  expect(probe.canonical).toBe("go https://a.test/x");
  expect(input.value).toBe("go https://a.test/x");
  redo(input);
  expect(probe.canonical).toBe(converted);
  // A newline converts the next URL the same way and keeps the newline.
  nativeEdit(input, "go https://a.test/x https://b.test/y", 36);
  nativeEdit(input, "go https://a.test/x https://b.test/y\n", 37);
  expect(probe.canonical).toBe(
    `${converted}[https://b.test/y](https://b.test/y)\n`,
  );
  expect(bindingTexts()).toEqual([]);
});

it("R03 buffer end: an unterminated URL and ineligible text stay plain", () => {
  const { input } = mount("");
  nativeEdit(input, "https://a.test/x", 16);
  expect(probe.canonical).toBe("https://a.test/x");
  nativeEdit(input, "https://a.test/x.", 17);
  expect(probe.canonical).toBe("https://a.test/x.");
  // The terminating space converts the URL and keeps the sentence period.
  nativeEdit(input, "https://a.test/x. ", 18);
  expect(probe.canonical).toBe("[https://a.test/x](https://a.test/x). ");
  nativeEdit(input, "https://a.test/x. www.example.com", 33);
  nativeEdit(input, "https://a.test/x. www.example.com ", 34);
  expect(probe.canonical).toBe(
    "[https://a.test/x](https://a.test/x). www.example.com ",
  );
  nativeEdit(input, "https://a.test/x. www.example.com mailto:a@b.test", 49);
  nativeEdit(input, "https://a.test/x. www.example.com mailto:a@b.test ", 50);
  expect(probe.canonical).toBe(
    "[https://a.test/x](https://a.test/x). www.example.com mailto:a@b.test ",
  );
  undo(input);
  expect(probe.canonical).toBe(
    "[https://a.test/x](https://a.test/x). www.example.com ",
  );
});

it("R04 paste: converts every eligible URL once, moves skill bindings and is one undo step", async () => {
  const { input } = await boundDraft();
  const before = probe.canonical;
  const beforeBindings = bindingTexts();
  expect(beforeBindings.map((row) => row[2])).toEqual([
    "$add-issue",
    "$add-note",
  ]);
  // Paste between the two skills (canonical offset 11, display offset 11).
  select(input, 11);
  expect(
    paste(
      input,
      "Review https://example.com/docs, and https://x.test/a(b) 한글 www.example.com\r\n",
    ),
  ).toBe(false);
  const pasted =
    "Review [https://example.com/docs](https://example.com/docs), and [https://x.test/a(b)](https://x.test/a\\(b\\)) 한글 www.example.com\n";
  expect(probe.canonical).toBe(
    `$add-issue ${pasted}${before.slice("$add-issue ".length)}`,
  );
  expect(input.value).toBe(
    `$add-issue Review https://example.com/docs, and https://x.test/a(b) 한글 www.example.com\n${DISPLAY} $add-note `,
  );
  const shift = pasted.length;
  expect(bindingTexts()).toEqual([
    [0, 10, "$add-issue"],
    [
      beforeBindings[1]![0]! + shift,
      beforeBindings[1]![1]! + shift,
      "$add-note",
    ],
  ]);
  expect(probe.selections).toHaveLength(2);
  expect(probe.api.readCanonical().start).toBe(11 + pasted.length);
  undo(input);
  expect(probe.canonical).toBe(before);
  expect(bindingTexts()).toEqual(beforeBindings);
  redo(input);
  expect(probe.canonical).toContain("Review [https://example.com/docs]");
});

it("R04 paste: existing links, images, code and fences are never converted", () => {
  const { input } = mount("");
  const text =
    "[Docs](https://e.com/d) ![i](https://e.com/i.png) `https://e.com/c`\n```\nhttps://e.com/f\n```\n";
  paste(input, text);
  expect(probe.canonical).toBe(text);
  // Pasting the same converted link again never nests it.
  const once = "x [https://a.test/x](https://a.test/x)";
  paste(input, once);
  expect(probe.canonical).toBe(text + once);
});

it("R04 paste: a conversion over the limit leaves the URL unconverted atomically", () => {
  const { input } = mount("", { limit: 20 });
  paste(input, "https://a.test/x");
  expect(probe.canonical).toBe("https://a.test/x");
  paste(input, " https://b.test/yyyy");
  // Even the plain text exceeds 20 bytes: nothing changes.
  expect(probe.canonical).toBe("https://a.test/x");
  expect(input.value).toBe("https://a.test/x");
  undo(input);
  expect(probe.canonical).toBe("");
});

it("R21 image with links: the whole image stays protected source, never collapsed or converted", () => {
  const image = "![see https://plain.test [x](https://x.test)](image.png)";
  const { input } = mount("");
  paste(input, image);
  expect(probe.canonical).toBe(image);
  expect(input.value).toBe(image);
  paste(input, " and https://b.test/y ");
  expect(probe.canonical).toBe(
    `${image} and [https://b.test/y](https://b.test/y) `,
  );
  expect(scanMarkdown(image).links).toEqual([]);
  // A delimiter typed after a URL inside the description converts nothing.
  const draft = "![see https://plain.test[x](https://x.test)](image.png)";
  cleanup();
  const again = mount(draft);
  select(again.input, 24);
  nativeEdit(again.input, `${draft.slice(0, 24)} ${draft.slice(24)}`, 25);
  expect(probe.canonical).toBe(
    "![see https://plain.test [x](https://x.test)](image.png)",
  );
});

it("R22 deeply nested labels: the scan stays within its budget, only the innermost link collapses", () => {
  const { input } = mount("");
  const nested = `${"[".repeat(3000)}${"a".repeat(30_000)}${"](https://n.test)".repeat(3000)}`;
  paste(input, nested);
  expect(probe.canonical).toBe(nested);
  // Every outer label holds the innermost link, which alone collapses.
  expect(input.value.length).toBeLessThan(nested.length);
  expect(input.value.startsWith("[".repeat(2999))).toBe(true);
  // The scan completed, so a following bare URL still converts.
  const started = performance.now();
  paste(input, " https://b.test/y ");
  expect(performance.now() - started).toBeLessThan(2000);
  expect(probe.canonical).toBe(
    `${nested} [https://b.test/y](https://b.test/y) `,
  );
});

it("R23 escaped image marker: the bang is literal text and the link collapses and opens", async () => {
  native.invoke.mockResolvedValue(undefined);
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockImplementation(
    function (this: HTMLElement) {
      return (
        this.hasAttribute("data-markdown-link")
          ? [{ left: 0, right: 100, top: 0, bottom: 20 }]
          : []
      ) as never;
    },
  );
  const draft = "\\![Docs](https://e.com/d)";
  const { input } = mount(draft);
  expect(probe.canonical).toBe(draft);
  expect(input.value).toBe("\\!Docs");
  fireEvent.mouseDown(input, {
    button: 0,
    ctrlKey: true,
    clientX: 10,
    clientY: 10,
  });
  fireEvent.mouseUp(input, { clientX: 10, clientY: 10 });
  fireEvent.click(input, {
    button: 0,
    ctrlKey: true,
    clientX: 10,
    clientY: 10,
  });
  await act(async () => {});
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", {
    url: "https://e.com/d",
  });
  // An even run of backslashes leaves a real image: nothing collapses.
  cleanup();
  const image = "\\\\![Docs](https://e.com/d)";
  const real = mount(image);
  expect(real.input.value).toBe(image);
  select(real.input, image.length);
  paste(real.input, " https://b.test/y ");
  expect(probe.canonical).toBe(
    `${image} [https://b.test/y](https://b.test/y) `,
  );
});

it("R24 localized boundary: a URL glued to a letter or digit of any script is never converted", () => {
  const { input } = mount("");
  // Pasted text: only the standalone URL converts.
  paste(input, "한https://a.test/x 3https://a.test/y 안녕 https://a.test/z ");
  expect(probe.canonical).toBe(
    "한https://a.test/x 3https://a.test/y 안녕 [https://a.test/z](https://a.test/z) ",
  );
  // A typed delimiter after a glued URL keeps it plain text.
  const base = input.value,
    end = base.length;
  select(input, end);
  nativeEdit(input, `${base}あhttps://a.test/w`, end + 17);
  nativeEdit(input, `${base}あhttps://a.test/w `, end + 18);
  expect(probe.canonical.endsWith("あhttps://a.test/w ")).toBe(true);
  expect(probe.canonical).not.toContain("[https://a.test/w]");
  // No conversion entry exists: one undo removes the typed run and delimiter.
  undo(input);
  expect(
    probe.canonical.endsWith("[https://a.test/z](https://a.test/z) "),
  ).toBe(true);
});

it("R25 deletion across whitespace: a removed space or newline seals the deletion entry like a typed one", () => {
  const { input } = mount("one two");
  select(input, 7);
  // Backspace through "two" and the separating space: one entry that ends
  // with the removed space, then "one" forms the next entry.
  for (const [text, caret] of [
    ["one tw", 6],
    ["one t", 5],
    ["one ", 4],
    ["one", 3],
    ["on", 2],
    ["o", 1],
  ] as const)
    nativeEdit(input, text, caret);
  expect(probe.canonical).toBe("o");
  undo(input);
  expect(probe.canonical).toBe("one");
  undo(input);
  expect(probe.canonical).toBe("one two");
  redo(input);
  expect(probe.canonical).toBe("one");
  redo(input);
  expect(probe.canonical).toBe("o");
});

it("R25 deletion across whitespace: forward Delete, newlines and multi-character removals seal too", () => {
  const { input } = mount("ab\ncd ef");
  // Forward Delete from the caret before "ab": "a", "b", then the newline.
  select(input, 0);
  for (const text of ["b\ncd ef", "\ncd ef", "cd ef", "d ef", " ef"])
    nativeEdit(input, text, 0);
  expect(probe.canonical).toBe(" ef");
  undo(input);
  expect(probe.canonical).toBe("cd ef");
  undo(input);
  expect(probe.canonical).toBe("ab\ncd ef");
  redo(input);
  redo(input);
  // A single removal that contains whitespace seals on its own.
  nativeEdit(input, "ef", 0);
  nativeEdit(input, "f", 0);
  expect(probe.canonical).toBe("f");
  undo(input);
  expect(probe.canonical).toBe("ef");
  undo(input);
  expect(probe.canonical).toBe(" ef");
});

it("R25 deletion across whitespace: removing a link whose source holds a space seals the entry", () => {
  const draft = "x [a b](https://e.com/d) y";
  const { input } = mount(draft);
  expect(input.value).toBe("x a b y");
  // Delete the whole label (one removal of the link source), then more text.
  select(input, 2, 5);
  nativeEdit(input, "x  y", 2);
  expect(probe.canonical).toBe("x  y");
  nativeEdit(input, "x y", 1);
  expect(probe.canonical).toBe("x y");
  undo(input);
  expect(probe.canonical).toBe("x  y");
  undo(input);
  expect(probe.canonical).toBe(draft);
});

it("R05 paste with files: image ownership stays with the form handler", () => {
  const attach = vi.fn();
  render(fixture()("", { attach }));
  const input = field();
  const file = new File(["x"], "a.png", { type: "image/png" });
  expect(paste(input, "https://a.test/x", [file])).toBe(false);
  expect(attach).toHaveBeenCalledWith([file]);
  expect(probe.canonical).toBe("");
  expect(input.value).toBe("");
  undo(input);
  expect(probe.canonical).toBe("");
});

it("R05 paste without text: left to the browser and the form", () => {
  const { input } = mount("");
  expect(paste(input, "")).toBe(true);
  expect(probe.canonical).toBe("");
});

it("R06 delete: backspace through a collapsed link edits its label and reveals the source", () => {
  const { input } = mount(`${LINK} t`);
  expect(input.value).toBe("Docs t");
  // Delete the trailing "t" (outside): stays collapsed.
  nativeEdit(input, "Docs ", 5);
  expect(probe.canonical).toBe(`${LINK} `);
  expect(input.value).toBe("Docs ");
  undo(input);
  expect(probe.canonical).toBe(`${LINK} t`);
  // Delete the last label character at the link end: the label edit reveals.
  select(input, 4);
  nativeEdit(input, "Doc t", 3);
  expect(probe.canonical).toBe("[Doc](https://e.com/d) t");
  expect(input.value).toBe("[Doc](https://e.com/d) t");
  expect(probe.api.readCanonical().start).toBe(4);
  expect(input.selectionStart).toBe(4);
});

it("R06 delete: breaking the syntax leaves ordinary visible source", () => {
  const { input } = mount(DRAFT);
  select(input, 5);
  expect(input.value).toBe(DRAFT);
  const closing = DRAFT.indexOf(")");
  nativeEdit(
    input,
    DRAFT.slice(0, closing) + DRAFT.slice(closing + 1),
    closing,
  );
  expect(probe.canonical).toBe(DRAFT.replace(")", ""));
  expect(input.value).toBe(probe.canonical);
  expect(
    screen
      .getByRole("textbox")
      .parentElement!.querySelectorAll("[data-markdown-link]"),
  ).toHaveLength(0);
  // Restoring the delimiter makes it a valid link again; the caret is at the
  // link end boundary, so it collapses.
  nativeEdit(input, DRAFT, closing + 1);
  expect(input.value).toBe(DISPLAY);
});

it("R07 selection: carets and selections reveal and collapse without a draft edit", async () => {
  const { input } = await boundDraft();
  const canonical = probe.canonical;
  const bindings = bindingTexts();
  const changes = probe.changes;
  const displayed = input.value;
  expect(displayed).toBe(`$add-issue ${DISPLAY} $add-note `);
  // A caret at either edge of the label keeps the link collapsed.
  select(input, 15);
  expect(input.value).toBe(displayed);
  select(input, 19);
  expect(input.value).toBe(displayed);
  // A caret strictly inside reveals the whole source.
  select(input, 17);
  expect(input.value).toBe(canonical);
  expect(input.selectionStart).toBe(18);
  expect(probe.api.readCanonical().start).toBe(
    canonical.indexOf("[Docs]") + 1 + 2,
  );
  // Leaving collapses it again.
  select(input, 0);
  expect(input.value).toBe(displayed);
  // A selection that overlaps reveals it, keeping the direction.
  select(input, 12, 20, "backward");
  expect(input.value).toBe(canonical);
  expect(input.selectionDirection).toBe("backward");
  expect(probe.changes).toBe(changes);
  expect(bindingTexts()).toEqual(bindings);
  expect(probe.canonical).toBe(canonical);
  expect(probe.selections).toHaveLength(2);
  // Select-all reveals every link; moving to the end collapses them.
  select(input, 0, canonical.length);
  expect(input.value).toBe(canonical);
  select(input, canonical.length);
  expect(input.value).toBe(displayed);
  // Revealing never created an undo step: undo reverts the last real edit,
  // the completion that preceded all the selection changes.
  undo(input);
  expect(probe.canonical).toBe(`$add-issue ${DRAFT} $add-no`);
});

it("R07 selection: Korean text and surrogate pairs map by code unit", () => {
  const link = "[한글😀 문서](https://e.com/k)";
  const { input } = mount(`앞 ${link} 뒤`);
  expect(input.value).toBe("앞 한글😀 문서 뒤");
  select(input, 4);
  expect(input.value).toBe(`앞 ${link} 뒤`);
  expect(probe.api.readCanonical().start).toBe(2 + 1 + 2);
  select(input, 0);
  select(input, 6);
  expect(probe.api.readCanonical().start).toBe(2 + 1 + 4);
  expect(input.selectionStart).toBe(7);
  select(input, 0);
  expect(input.value).toBe("앞 한글😀 문서 뒤");
});

it("R07 selection: editing inside a revealed link keeps it valid and revealed", () => {
  const { input } = mount(DRAFT);
  select(input, 6);
  expect(input.value).toBe(DRAFT);
  const caret = DRAFT.indexOf("Docs") + 2;
  select(input, caret);
  nativeEdit(
    input,
    `${DRAFT.slice(0, caret)}!${DRAFT.slice(caret)}`,
    caret + 1,
  );
  expect(probe.canonical).toBe("see [Do!cs](https://e.com/d) ok");
  expect(input.value).toBe(probe.canonical);
  select(input, 0);
  expect(input.value).toBe("see Do!cs ok");
});

it("R08 copy: serializes the canonical Markdown of the selection", () => {
  const { input } = mount(DRAFT);
  select(input, 0, DISPLAY.length);
  expect(input.value).toBe(DRAFT);
  const setData = vi.fn();
  const proceed = fireEvent.copy(input, { clipboardData: { setData } });
  expect(proceed).toBe(false);
  expect(setData).toHaveBeenCalledWith("text/plain", DRAFT);
  expect(probe.canonical).toBe(DRAFT);
  expect(probe.changes).toBe(0);
  // A collapsed selection copies nothing and is left to the browser.
  select(input, 0);
  expect(fireEvent.copy(input, { clipboardData: { setData } })).toBe(true);
});

it("R09 cut: removes the canonical range, serializes it and is one undo step", async () => {
  const { input } = await boundDraft();
  const before = probe.canonical;
  const beforeBindings = bindingTexts();
  const start = before.indexOf("see ");
  const end = before.indexOf(" $add-note");
  select(input, start, 11 + DISPLAY.length);
  const setData = vi.fn();
  expect(fireEvent.cut(input, { clipboardData: { setData } })).toBe(false);
  expect(setData).toHaveBeenCalledWith("text/plain", before.slice(start, end));
  expect(probe.canonical).toBe(before.slice(0, start) + before.slice(end));
  expect(input.value).toBe("$add-issue  $add-note ");
  expect(input.selectionStart).toBe(11);
  expect(bindingTexts()).toEqual([
    [0, 10, "$add-issue"],
    [12, 21, "$add-note"],
  ]);
  undo(input);
  expect(probe.canonical).toBe(before);
  expect(bindingTexts()).toEqual(beforeBindings);
  expect(input.value).toBe(`$add-issue ${DISPLAY} $add-note `);
});

it("R10 R11 undo and redo: step through typing and paste transactions in order", () => {
  const { input } = mount("");
  nativeEdit(input, "a", 1);
  nativeEdit(input, "a ", 2);
  paste(input, "https://a.test/x");
  const converted = "a [https://a.test/x](https://a.test/x)";
  expect(probe.canonical).toBe(converted);
  undo(input);
  expect(probe.canonical).toBe("a ");
  undo(input);
  expect(probe.canonical).toBe("");
  undo(input);
  expect(probe.canonical).toBe("");
  redo(input);
  expect(probe.canonical).toBe("a ");
  redo(input);
  expect(probe.canonical).toBe(converted);
  redo(input);
  expect(probe.canonical).toBe(converted);
  // A new edit clears the redo stack.
  undo(input);
  nativeEdit(input, "a b", 3);
  redo(input);
  expect(probe.canonical).toBe("a b");
});

it("R10 R11 undo and redo: browser history commands and non-Apple y shortcut use the same stack", () => {
  const { input } = mount("");
  nativeEdit(input, "a", 1);
  const event = new InputEvent("beforeinput", {
    inputType: "historyUndo",
    cancelable: true,
    bubbles: true,
  });
  act(() => {
    input.dispatchEvent(event);
  });
  expect(event.defaultPrevented).toBe(true);
  expect(probe.canonical).toBe("");
  fireEvent.keyDown(input, { key: "y", ctrlKey: true });
  expect(probe.canonical).toBe("a");
  // Wrong modifiers do not undo.
  fireEvent.keyDown(input, { key: "z", ctrlKey: true, altKey: true });
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(probe.canonical).toBe("a");
});

it("R10 R11 undo and redo: Apple platforms use the command key", () => {
  Object.defineProperty(window.navigator, "platform", {
    value: "MacIntel",
    configurable: true,
  });
  const { input } = mount("");
  nativeEdit(input, "a", 1);
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(probe.canonical).toBe("a");
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(probe.canonical).toBe("");
  fireEvent.keyDown(input, { key: "z", metaKey: true, shiftKey: true });
  expect(probe.canonical).toBe("a");
});

it("R12 R13 R14 IME: composition freezes display, converts only after it ends and is one undo step", () => {
  const { input } = mount(DRAFT);
  select(input, DISPLAY.length);
  fireEvent.compositionStart(input);
  nativeEdit(input, `${DISPLAY}ㅎ`, DISPLAY.length + 1);
  expect(input.value).toBe(`${DISPLAY}ㅎ`);
  nativeEdit(input, `${DISPLAY}한`, DISPLAY.length + 1);
  // Canonical follows each update; the displayed link stays collapsed.
  expect(probe.canonical).toBe(`${DRAFT}한`);
  expect(input.value).toBe(`${DISPLAY}한`);
  fireEvent.compositionEnd(input);
  expect(input.value).toBe(`${DISPLAY}한`);
  undo(input);
  expect(probe.canonical).toBe(DRAFT);
  expect(input.value).toBe(DISPLAY);
});

it("R12 R13 R14 IME: a delimiter committed during composition converts after composition ends", () => {
  const { input } = mount("go https://a.test/x");
  select(input, 19);
  fireEvent.compositionStart(input);
  nativeEdit(input, "go https://a.test/x ", 20);
  expect(probe.canonical).toBe("go https://a.test/x ");
  expect(input.value).toBe("go https://a.test/x ");
  fireEvent.compositionEnd(input);
  const converted = "go [https://a.test/x](https://a.test/x) ";
  expect(probe.canonical).toBe(converted);
  expect(input.value).toBe("go https://a.test/x ");
  expect(probe.api.readCanonical().start).toBe(converted.length);
  undo(input);
  expect(probe.canonical).toBe("go https://a.test/x");
  // Undo is refused while a composition is active.
  fireEvent.compositionStart(input);
  undo(input);
  fireEvent.compositionEnd(input);
});

it("R12 R13 R14 IME: a final input delivered after composition end is processed the same way", () => {
  const { input } = mount("go https://a.test/x");
  select(input, 19);
  fireEvent.compositionStart(input);
  fireEvent.compositionEnd(input);
  nativeEdit(input, "go https://a.test/x ", 20);
  expect(probe.canonical).toBe("go [https://a.test/x](https://a.test/x) ");
});

it("R12 R13 R14 IME: no conversion while composing even for a complete URL word", () => {
  const { input } = mount("");
  fireEvent.compositionStart(input);
  nativeEdit(input, "https://a.test/x ", 17);
  expect(probe.canonical).toBe("https://a.test/x ");
  fireEvent.compositionEnd(input);
  expect(probe.canonical).toBe("[https://a.test/x](https://a.test/x) ");
});

it("R26 IME end over the limit: the accepted composed text is re-offered when its Markdown expansion is refused", () => {
  const plain = "go https://a.test/x ",
    expanded = "go [https://a.test/x](https://a.test/x) ";
  // The plain draft fits; the expansion (41 bytes) does not.
  const { input } = mount("go https://a.test/x", { limit: 30 });
  select(input, 19);
  fireEvent.compositionStart(input);
  nativeEdit(input, plain, 20);
  expect(probe.canonical).toBe(plain);
  fireEvent.compositionEnd(input);
  expect(probe.canonical).toBe(plain);
  expect(input.value).toBe(plain);
  // The refused expansion is followed by the accepted text, so an owner that
  // shows a limit error on refusal clears it again.
  const verdicts = probe.attempts.map((attempt) => [
    attempt.text,
    attempt.accepted,
  ]);
  expect(verdicts.slice(-2)).toEqual([
    [expanded, false],
    [plain, true],
  ]);
  expect(probe.api.readCanonical().start).toBe(plain.length);
  // The composition stays one sealed entry: one undo removes it, none is added.
  undo(input);
  expect(probe.canonical).toBe("go https://a.test/x");
  redo(input);
  expect(probe.canonical).toBe(plain);
  nativeEdit(input, `${plain}y`, plain.length + 1);
  undo(input);
  expect(probe.canonical).toBe(plain);
});

it("R26 IME end over the limit: a final input after composition end and an unchanged range behave alike", () => {
  const plain = "go https://a.test/x ",
    expanded = "go [https://a.test/x](https://a.test/x) ";
  const { input } = mount("go https://a.test/x", { limit: 30 });
  select(input, 19);
  fireEvent.compositionStart(input);
  fireEvent.compositionEnd(input);
  nativeEdit(input, plain, 20);
  expect(probe.canonical).toBe(plain);
  expect(
    probe.attempts.slice(-2).map((attempt) => [attempt.text, attempt.accepted]),
  ).toEqual([
    [expanded, false],
    [plain, true],
  ]);
  // A composition without any delimiter offers nothing beyond the edit itself.
  cleanup();
  const again = mount("ab", { limit: 30 });
  select(again.input, 2);
  fireEvent.compositionStart(again.input);
  nativeEdit(again.input, "ab한", 3);
  const offered = probe.attempts.length;
  fireEvent.compositionEnd(again.input);
  expect(probe.attempts).toHaveLength(offered);
  expect(probe.canonical).toBe("ab한");
});

it("R15 skill acceptance: keeps the previous layer's binding, separator and caret in canonical offsets", async () => {
  const { input } = await boundDraft();
  const before = probe.canonical;
  // Accept a completion after the collapsed link, as the second skill did.
  expect(bindingTexts()).toEqual([
    [0, 10, "$add-issue"],
    [before.indexOf("$add-note"), before.indexOf("$add-note") + 9, "$add-note"],
  ]);
  expect(input.value).toBe(`$add-issue ${DISPLAY} $add-note `);
  // Caret is one past the token: canonical end + 1, displayed accordingly.
  expect(probe.api.readCanonical().start).toBe(before.length);
  expect(input.selectionStart).toBe(input.value.length);
  undo(input);
  expect(probe.canonical).toBe(`$add-issue ${DRAFT} $add-no`);
  expect(bindingTexts()).toEqual([[0, 10, "$add-issue"]]);
  redo(input);
  expect(probe.canonical).toBe(before);
  expect(probe.selections).toHaveLength(2);
});

it("R15 skill acceptance: a rejected completion changes nothing", async () => {
  const view = render(fixture()("", { limit: 11 }));
  const input = field();
  nativeEdit(input, "$add-iss", 8);
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Tab" });
  // "$add-issue " is 11 bytes, within the limit; a second completion exceeds it.
  expect(probe.canonical).toBe("$add-issue ");
  nativeEdit(input, "$add-issue $add-no", 18);
  expect(probe.canonical).toBe("$add-issue ");
  view.unmount();
});

it("R16 modifier click: only the exact platform modifier opens the parsed URL once", async () => {
  native.invoke.mockResolvedValue(undefined);
  const { input } = await boundDraft();
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockImplementation(
    function (this: HTMLElement) {
      return (
        this.hasAttribute("data-markdown-link")
          ? [{ left: 0, right: 100, top: 0, bottom: 20 }]
          : []
      ) as never;
    },
  );
  const before = probe.canonical;
  const displayed = input.value;
  const changes = probe.changes;
  const click = (init: MouseEventInit) => {
    const down = fireEvent.mouseDown(input, {
      clientX: 10,
      clientY: 10,
      ...init,
    });
    fireEvent.mouseUp(input, { clientX: 10, clientY: 10, ...init });
    const up = fireEvent.click(input, { clientX: 10, clientY: 10, ...init });
    return { down, up };
  };
  // Windows/Linux: Control alone.
  expect(click({ button: 0, ctrlKey: true })).toEqual({
    down: false,
    up: false,
  });
  await act(async () => {});
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", {
    url: "https://e.com/d",
  });
  for (const init of [
    { button: 0 },
    { button: 0, metaKey: true },
    { button: 0, ctrlKey: true, shiftKey: true },
    { button: 0, ctrlKey: true, altKey: true },
    { button: 0, ctrlKey: true, metaKey: true },
    { button: 1, ctrlKey: true },
    { button: 2, ctrlKey: true },
  ]) {
    const result = click(init);
    expect(result.down).toBe(true);
  }
  await act(async () => {});
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(probe.canonical).toBe(before);
  expect(input.value).toBe(displayed);
  expect(probe.changes).toBe(changes);
  expect(bindingTexts()).toHaveLength(2);
  Object.defineProperty(window.navigator, "platform", {
    value: "MacIntel",
    configurable: true,
  });
  expect(click({ button: 0, metaKey: true })).toEqual({
    down: false,
    up: false,
  });
  await act(async () => {});
  expect(native.invoke).toHaveBeenCalledTimes(2);
  expect(click({ button: 0, ctrlKey: true }).down).toBe(true);
  expect(native.invoke).toHaveBeenCalledTimes(2);
});

it("R16 modifier click: a pointer outside every link, a missing opener and native errors leave the draft alone", async () => {
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockImplementation(
    function (this: HTMLElement) {
      return (
        this.hasAttribute("data-markdown-link")
          ? [{ left: 0, right: 100, top: 0, bottom: 20 }]
          : []
      ) as never;
    },
  );
  const { input } = mount(DRAFT);
  // Outside the painted rectangle.
  expect(
    fireEvent.mouseDown(input, { ctrlKey: true, clientX: 500, clientY: 500 }),
  ).toBe(true);
  fireEvent.mouseUp(input);
  expect(native.invoke).not.toHaveBeenCalled();
  // No native opener: ordinary editing continues.
  native.isTauri.mockReturnValue(false);
  expect(
    fireEvent.mouseDown(input, { ctrlKey: true, clientX: 10, clientY: 10 }),
  ).toBe(true);
  fireEvent.mouseUp(input);
  fireEvent.click(input, { ctrlKey: true, clientX: 10, clientY: 10 });
  expect(native.invoke).not.toHaveBeenCalled();
  // A rejected open shows the shared localized failure once, with no retry.
  native.isTauri.mockReturnValue(true);
  native.invoke.mockRejectedValue("browser-unavailable");
  fireEvent.mouseDown(input, { ctrlKey: true, clientX: 10, clientY: 10 });
  fireEvent.mouseUp(input);
  fireEvent.click(input, { ctrlKey: true, clientX: 10, clientY: 10 });
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(copy("external-link.notInstalled"));
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(probe.canonical).toBe(DRAFT);
  expect(input.value).toBe(DISPLAY);
  fireEvent.click(
    screen.getByRole("button", { name: copy("external-link.dismiss") }),
  );
  expect(screen.queryByRole("alert")).toBeNull();
});

it("R17 blur and focus: change nothing about text, selection, history or bindings", () => {
  const { input } = mount(DRAFT);
  nativeEdit(input, `${DISPLAY}x`, DISPLAY.length + 1);
  select(input, 3);
  fireEvent.blur(input);
  fireEvent.focus(input);
  expect(probe.canonical).toBe(`${DRAFT}x`);
  expect(input.value).toBe(`${DISPLAY}x`);
  expect(input.selectionStart).toBe(3);
  expect(probe.api.readCanonical().start).toBe(3);
  undo(input);
  expect(probe.canonical).toBe(DRAFT);
});

it("R18 history navigation: replacement drops bindings and history and selects in canonical offsets", async () => {
  const { input } = await boundDraft();
  act(() => {
    probe.api.replaceUnbound(`x ${LINK} y`, 2);
  });
  expect(probe.canonical).toBe(`x ${LINK} y`);
  expect(input.value).toBe("x Docs y");
  expect(probe.bindings).toEqual([]);
  expect(input.selectionStart).toBe(2);
  undo(input);
  expect(probe.canonical).toBe(`x ${LINK} y`);
  act(() => {
    probe.api.select(2 + 1, 2 + 1 + 2);
  });
  expect(input.value).toBe(`x ${LINK} y`);
  expect(probe.api.readCanonical()).toEqual({
    text: `x ${LINK} y`,
    start: 3,
    end: 5,
  });
});

it("R19 limit rejection: nothing changes and the field returns to the last text", () => {
  const { input } = mount(DRAFT, { limit: DRAFT.length });
  select(input, DISPLAY.length);
  nativeEdit(input, `${DISPLAY}x`, DISPLAY.length + 1);
  expect(probe.canonical).toBe(DRAFT);
  expect(input.value).toBe(DISPLAY);
  expect(probe.api.readCanonical().start).toBe(DRAFT.length);
  undo(input);
  expect(probe.canonical).toBe(DRAFT);
  expect(probe.changes).toBe(0);
});

it("R20 owner replacement: submit or restoration resets history and moves the caret to the end", () => {
  const { input } = mount(DRAFT);
  nativeEdit(input, `${DISPLAY}x`, DISPLAY.length + 1);
  fireEvent.click(screen.getByRole("button", { name: "Reset" }));
  expect(probe.canonical).toBe("");
  undo(input);
  expect(probe.canonical).toBe("");
  expect(input.value).toBe("");
});

it("keeps the painted mirror aligned with the displayed text and canonical skill ranges", async () => {
  const { view, input } = await boundDraft();
  const overlay = view.container.querySelector(".skill-text-overlay > div")!;
  // The mirror holds exactly the displayed text plus its trailing guard.
  expect(overlay.textContent).toBe(`${input.value}\u200b`);
  const labels = [...view.container.querySelectorAll("[data-markdown-link]")];
  expect(labels.map((node) => node.textContent)).toEqual(["Docs"]);
  expect(labels[0]!.className).toBe("markdown-link-label");
  const skills = [...view.container.querySelectorAll(".skill-token-selected")];
  expect(skills.map((node) => node.textContent)).toEqual([
    "$add-issue",
    "$add-note",
  ]);
  // Skill spans keep their canonical start offsets as identity.
  expect(
    skills.map((node) => (node as HTMLElement).dataset.skillStart),
  ).toEqual(["0", String(probe.canonical.indexOf("$add-note"))]);
  // Revealed source stays one ordinary span with no link styling.
  select(input, 17);
  const source = view.container.querySelector("[data-markdown-link]")!;
  expect(source.className).toBe("markdown-link-source");
  expect(source.textContent).toBe(LINK);
});

it("ignores a caret-only change event and a stray undo with no history", () => {
  const { input } = mount(DRAFT);
  fireEvent.change(input, { target: { value: DISPLAY } });
  expect(probe.changes).toBe(0);
  undo(input);
  redo(input);
  expect(probe.canonical).toBe(DRAFT);
  expect(probe.changes).toBe(0);
});

it("restored or replaced drafts are never converted", () => {
  const { input } = mount("go https://a.test/x ");
  expect(probe.canonical).toBe("go https://a.test/x ");
  act(() => {
    probe.api.replaceUnbound("see https://b.test/y ", 21);
  });
  expect(probe.canonical).toBe("see https://b.test/y ");
  expect(input.value).toBe("see https://b.test/y ");
});

it("paints an unavailable skill token after a collapsed link at its displayed position", async () => {
  const { view, input } = await boundDraft();
  const end = input.value.length;
  select(input, end);
  nativeEdit(input, `${input.value}$zzz `, end + 5);
  await vi.waitFor(() =>
    expect(view.container.querySelector(".skill-token-unavailable")).not.toBe(
      null,
    ),
  );
  const span = view.container.querySelector(
    ".skill-token-unavailable",
  ) as HTMLElement;
  expect(span.textContent).toBe("$zzz");
  expect(span.dataset.skillStart).toBe(String(probe.canonical.indexOf("$zzz")));
  const overlay = view.container.querySelector(".skill-text-overlay > div")!;
  expect(overlay.textContent).toBe(`${input.value}​`);
  expect(input.value.indexOf("$zzz")).toBe(
    probe.canonical.indexOf("$zzz") - (LINK.length - "Docs".length),
  );
});

// --- Line endings (R27) ----------------------------------------------------

// Exact offset that a textarea offset stands for: a CRLF pair is one shown
// unit, a lone CR is one shown unit and an offset on a line ending is the
// start of that ending.
function exactOffset(text: string, shown: number): number {
  let index = 0;
  for (let unit = 0; unit < shown && index < text.length; unit++)
    index += text[index] === "\r" && text[index + 1] === "\n" ? 2 : 1;
  return index;
}
const normalized = (text: string) => text.replace(/\r\n?/gu, "\n");

it("R27 line endings: the field equals the normalized draft and every selection reads back exactly", () => {
  for (const draft of [
    "a\r\nb\r\nc",
    "a\rb\rc",
    "a\r\nb\rc\nd\r\n",
    "\r\n\r\n",
    "x\r",
    "\r",
  ]) {
    const { view, input } = mount(draft);
    expect(input.value).toBe(normalized(draft));
    const length = input.value.length;
    for (let start = 0; start <= length; start++)
      for (let end = start; end <= length; end++) {
        select(input, start, end);
        const at = probe.api.readCanonical();
        expect(at.text).toBe(draft);
        expect([at.start, at.end]).toEqual([
          exactOffset(draft, start),
          exactOffset(draft, end),
        ]);
        expect(input.value).toBe(normalized(draft));
      }
    view.unmount();
  }
});

it("R27 line endings: paste and cut edit the exact range and undo restores the exact text", () => {
  const draft = "head\r\n[Docs](https://e.com/d)\r\ntail\rend";
  const { input } = mount(draft);
  expect(input.value).toBe("head\nDocs\ntail\nend");
  // Paste at the start of the line after the CRLF that follows the link.
  select(input, 10);
  expect(probe.api.readCanonical().start).toBe(draft.indexOf("tail"));
  paste(input, "P");
  expect(probe.canonical).toBe("head\r\n[Docs](https://e.com/d)\r\nPtail\rend");
  expect(input.value).toBe("head\nDocs\nPtail\nend");
  expect(input.selectionStart).toBe(11);
  undo(input);
  expect(probe.canonical).toBe(draft);
  expect(input.selectionStart).toBe(10);
  // Cut the shown line break after the link: the whole CRLF leaves.
  select(input, 9, 10);
  const setData = vi.fn();
  expect(fireEvent.cut(input, { clipboardData: { setData } })).toBe(false);
  expect(setData).toHaveBeenCalledWith("text/plain", "\r\n");
  expect(probe.canonical).toBe("head\r\n[Docs](https://e.com/d)tail\rend");
  expect(input.value).toBe("head\nDocstail\nend");
  expect(input.selectionStart).toBe(9);
  undo(input);
  expect(probe.canonical).toBe(draft);
  // Cut across a lone CR and the text after it.
  select(input, 13, 16);
  expect(probe.api.readCanonical()).toMatchObject({
    start: draft.indexOf("\rend") - 1,
    end: draft.indexOf("\rend") + 2,
  });
  expect(fireEvent.cut(input, { clipboardData: { setData } })).toBe(false);
  expect(probe.canonical).toBe("head\r\n[Docs](https://e.com/d)\r\ntai" + "nd");
  redo(input);
  undo(input);
  expect(probe.canonical).toBe(draft);
  // Paste over the shown line break replaces the whole CRLF; the URL is glued
  // to the preceding word, so it stays plain text.
  select(input, 4, 5);
  paste(input, "https://a.test/x");
  expect(probe.canonical).toBe(
    "headhttps://a.test/x[Docs](https://e.com/d)\r\ntail\rend",
  );
  expect(input.value).toBe("head" + "https://a.test/x" + "Docs\ntail\nend");
});

it("R27 line endings: copy serializes the exact selected text", () => {
  const { input } = mount("a\r\nb\rc");
  select(input, 1, 4);
  const setData = vi.fn();
  expect(fireEvent.copy(input, { clipboardData: { setData } })).toBe(false);
  expect(setData).toHaveBeenCalledWith("text/plain", "\r\nb\r");
});

it("R27 line endings: typing and deleting next to a lone CR never fuse it with an LF", () => {
  // Typing a new line break right after a lone CR must not form a CRLF pair.
  const { input, view } = mount("a\rb");
  select(input, 2);
  nativeEdit(input, "a\n\nb", 3);
  expect(normalized(probe.canonical)).toBe("a\n\nb");
  expect(input.value).toBe("a\n\nb");
  expect(probe.canonical).toHaveLength(4);
  expect(probe.api.readCanonical().start).toBe(3);
  undo(input);
  expect(probe.canonical).toBe("a\rb");
  expect(input.value).toBe("a\nb");
  view.unmount();
  // Deleting the text between a lone CR and an LF keeps both line breaks.
  const second = mount("a\rX\nb");
  select(second.input, 3);
  nativeEdit(second.input, "a\n\nb", 2);
  expect(normalized(probe.canonical)).toBe("a\n\nb");
  expect(second.input.value).toBe("a\n\nb");
  expect(probe.canonical).toHaveLength(4);
  second.view.unmount();
  // An existing CRLF pair is never rewritten by an unrelated edit.
  const third = mount("a\r\nb");
  select(third.input, 3);
  nativeEdit(third.input, "a\nbc", 4);
  expect(probe.canonical).toBe("a\r\nbc");
  nativeEdit(third.input, "a\n\nbc", 2);
  expect(normalized(probe.canonical)).toBe("a\n\nbc");
  expect(third.input.value).toBe("a\n\nbc");
  expect(probe.canonical).toContain("\r\n");
});

it("R27 line endings: IME composition around CRLF text keeps the field and selection exact", () => {
  const draft = "a\r\n[Docs](https://e.com/d)\r\nb";
  for (const caret of [2, 6, 7]) {
    const { input, view } = mount(draft);
    select(input, caret);
    const start = probe.api.readCanonical().start;
    fireEvent.compositionStart(input);
    for (const chunk of ["ㅎ", "하", "한"]) {
      const base = input.value;
      const at = input.selectionStart;
      const from = chunk === "ㅎ" ? at : at - 1;
      nativeEdit(input, base.slice(0, from) + chunk + base.slice(at), from + 1);
    }
    fireEvent.compositionEnd(input);
    expect(probe.canonical).toBe(
      draft.slice(0, start) + "한" + draft.slice(start),
    );
    expect(input.value).toBe(
      projectMarkdown(
        probe.canonical,
        scanMarkdown(probe.canonical).links,
        (link) => !selectionRevealsLink(link, probe.api.readCanonical()),
      ).text,
    );
    expect(probe.api.readCanonical().start).toBe(start + 1);
    undo(input);
    expect(probe.canonical).toBe(draft);
    view.unmount();
  }
});

it("R27 line endings: owner replacement and recall select exact offsets in CRLF text", () => {
  const { input } = mount("");
  act(() => {
    probe.api.replaceUnbound("x\r\ny\r\nz", 6);
  });
  expect(input.value).toBe("x\ny\nz");
  expect(input.selectionStart).toBe(4);
  expect(probe.api.readCanonical().start).toBe(6);
  act(() => {
    probe.api.select(3, 6);
  });
  expect([input.selectionStart, input.selectionEnd]).toEqual([2, 4]);
  expect(probe.api.readCanonical()).toMatchObject({ start: 3, end: 6 });
});

// --- Multiline reference definitions (R28) ----------------------------------

it("R28 multiline definition: a URL on a continuation line stays source text however it is entered", () => {
  const URL = "https://example.test";
  for (const ending of ["\n", "\r\n", "\r"]) {
    const head = `[docs]:${ending}  `;
    // Pasted after the label: the destination of the definition.
    const { input, view } = mount(head);
    select(input, input.value.length);
    paste(input, URL);
    expect(probe.canonical).toBe(`${head}${URL}`);
    // A typed delimiter after it converts nothing either.
    nativeEdit(input, `${input.value} `, input.value.length + 1);
    expect(probe.canonical).toBe(`${head}${URL} `);
    nativeEdit(input, `${input.value}\n`, input.value.length + 1);
    expect(normalized(probe.canonical)).toBe(normalized(`${head}${URL} \n`));
    expect(probe.canonical).not.toContain("](");
    view.unmount();
    // A title on the following lines is protected too.
    const titled = mount(`[docs]: /u${ending}  "see `);
    select(titled.input, titled.input.value.length);
    paste(titled.input, `${URL}" `);
    expect(probe.canonical).toBe(`[docs]: /u${ending}  "see ${URL}" `);
    titled.view.unmount();
  }
  // Without a definition around it the same paste converts.
  const plain = mount("[docs]:\n\n");
  select(plain.input, plain.input.value.length);
  paste(plain.input, URL);
  expect(probe.canonical).toBe(`[docs]:\n\n[${URL}](${URL})`);
});

// Seeded random walk over the whole state machine. After every operation the
// field must show exactly the projection of the canonical text for the
// canonical selection, selection maps must agree, and undoing every edit must
// return to the initial draft with redo returning to the final one.
function walker(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}
const WALK_PASTES = [
  "[docs]:\n  https://d.test\n  'T https://t.test'\n",
  "[docs]:\n  ",
  "https://a.test/p?q=[1]",
  "text 한글 😀",
  "[Docs](https://e.com/d)",
  "x [y](https://z.test/a(b)) w",
  "`https://c.test` ",
  "\n",
  "www.example.com mailto:a@b.test",
  "https://a.test/x. ",
  "![i](https://e.com/i.png)",
  "```\nhttps://f.test\n",
  "한https://a.test/g 3https://a.test/h ",
  "\\![d](https://e.com/d)",
  "![a [b](https://b.test)](i.png) https://c.test/k ",
];
const WALK_CHARS = ["a", "한", " ", "\n", "[", "]", "(", ")", "`", "!", "\\"];
for (let seed = 1; seed <= 60; seed++) {
  it(`holds the display, selection and history invariants on a random walk (seed ${seed})`, () => {
    const next = walker(seed);
    const roll = next();
    // Drafts that retain CRLF and lone CR endings walk the same machine.
    const initial =
      roll < 0.35 ? DRAFT : roll < 0.6 ? `a\r\n${LINK}\rb\r\n` : "";
    const { input } = mount(initial);
    const pick = <T,>(items: readonly T[]) =>
      items[Math.floor(next() * items.length)]!;
    const check = () => {
      const canonical = probe.canonical;
      const at = probe.api.readCanonical();
      expect(at.text).toBe(canonical);
      const links = scanMarkdown(canonical).links;
      const projection = projectMarkdown(
        canonical,
        links,
        (link) => !selectionRevealsLink(link, at),
      );
      expect(input.value).toBe(projection.text);
      expect(input.selectionStart).toBe(projection.toDisplay(at.start));
      expect(input.selectionEnd).toBe(projection.toDisplay(at.end));
      expect(projection.toSource(input.selectionStart)).toBe(at.start);
      expect(projection.toSource(input.selectionEnd)).toBe(at.end);
    };
    check();
    let edits = 0;
    for (let step = 0; step < 30; step++) {
      const start = input.selectionStart,
        end = input.selectionEnd;
      const operation = pick([
        "type",
        "delete",
        "paste",
        "select",
        "select",
        "compose",
      ]);
      const before = probe.canonical;
      if (operation === "type") {
        const char = pick(WALK_CHARS);
        const text =
          input.value.slice(0, start) + char + input.value.slice(end);
        nativeEdit(input, text, start + char.length);
      } else if (operation === "delete") {
        const from = start === end ? Math.max(0, start - 1) : start;
        const text = input.value.slice(0, from) + input.value.slice(end);
        nativeEdit(input, text, from);
      } else if (operation === "paste") {
        paste(input, pick(WALK_PASTES));
      } else if (operation === "compose") {
        // A composition is one edit; its text commits when it ends.
        fireEvent.compositionStart(input);
        for (let update = 0; update < 1 + Math.floor(next() * 3); update++) {
          const at = input.selectionStart,
            to = input.selectionEnd,
            chunk = pick(["ㅎ", "한", " ", "https://a.test/x", "\n"]);
          nativeEdit(
            input,
            input.value.slice(0, at) + chunk + input.value.slice(to),
            at + chunk.length,
          );
        }
        fireEvent.compositionEnd(input);
      } else {
        const a = Math.floor(next() * (input.value.length + 1)),
          b = next() < 0.5 ? a : Math.floor(next() * (input.value.length + 1));
        select(input, Math.min(a, b), Math.max(a, b));
      }
      if (probe.canonical !== before) edits += 1;
      check();
    }
    const final = probe.canonical;
    // Every accepted edit is undoable; stepping back reaches the initial draft.
    for (let step = 0; step < edits + 2; step++) undo(input);
    expect(probe.canonical).toBe(initial);
    check();
    for (let step = 0; step < edits + 2; step++) redo(input);
    expect(probe.canonical).toBe(final);
    check();
  });
}
