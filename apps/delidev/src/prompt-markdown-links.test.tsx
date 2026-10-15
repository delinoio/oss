// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
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

function Composer({ initial = "" }: { initial?: string }) {
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
    machineId: "",
    agentId: "",
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
      <output data-testid="canonical">{value}</output>
    </>
  );
}
function view(initial?: string) {
  const transport = createRouterTransport(() => {});
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Composer initial={initial} />
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
