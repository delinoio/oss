// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { codexMessage, NativeCodexMessage } from "./native-codex-message";
import { i18n } from "./localization";
afterEach(async () => { cleanup(); await i18n.changeLanguage("en"); });
const original = { delivery_present: true, delivery: "async", questions_present: true, questions: [{ title: "First", options: null }, { title: "Second", options: [] }, { title: "Third <script>opaque</script>", options: ["one", "two", ""] }] };
it("retains original nullable, empty and ordered embedded questions without a response form", () => {
  render(<NativeCodexMessage value={original} />);
  expect(codexMessage(original)?.questions?.map(question => question.options)).toEqual([null, [], ["one", "two", ""]]);
  expect(screen.getByText("Third <script>opaque</script>")).toBeTruthy();
  expect(screen.getByText("No choices were supplied.")).toBeTruthy();
  expect(screen.getByText("The native choices list is empty.")).toBeTruthy();
  expect(screen.getAllByRole("button").map(button => button.textContent)).toEqual(["one", "two", "Empty suggestion"]);
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(document.querySelector("form")).toBeNull();
});
it.each(["en", "ko"])("requires explicit selection and preserves an existing draft in %s", async language => {
  await i18n.changeLanguage(language);
  const select = vi.fn();
  const view = render(<NativeCodexMessage value={original} select={select} draftBlocked />);
  fireEvent.click(screen.getByRole("button", { name: "one" }));
  expect(select).not.toHaveBeenCalled();
  view.rerender(<NativeCodexMessage value={original} select={select} draftBlocked={false} />);
  expect(select).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "one" }));
  expect(select).toHaveBeenCalledExactlyOnceWith("one");
});
it("adds only unsent text and does not overwrite composer edits or send a prompt", () => {
  const send = vi.fn();
  function Draft() {
    const [draft, setDraft] = useState("original user draft");
    return <><textarea aria-label="Composer" value={draft} onChange={event => setDraft(event.target.value)} /><NativeCodexMessage value={original} draftBlocked={draft.length > 0} select={value => { if (draft.length) return false; setDraft(value); return true; }} /><button onClick={() => send(draft)}>Explicit send</button></>;
  }
  render(<Draft />);
  fireEvent.click(screen.getByRole("button", { name: "one" }));
  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("original user draft");
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "one" }));
  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("one");
  fireEvent.click(screen.getByRole("button", { name: "two" }));
  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("one");
  expect(send).not.toHaveBeenCalled();
});
it.each([
  { ...original, delivery: "sync" }, { ...original, delivery_present: false }, { ...original, questions_present: false },
  { ...original, questions: [{ title: "original", options: [null] }] }, { ...original, questions: [{ title: "original", options: {} }] },
  { ...original, questions: [{ title: "original", options: [], unknown: true }] }, { ...original, questions: [{ title: "x".repeat(65537), options: null }] },
  { ...original, unknown: true },
])("rejects malformed observations without displaying a partial suggestion", value => {
  expect(codexMessage(value)).toBeUndefined();
  render(<NativeCodexMessage value={value} select={vi.fn()} draftBlocked={false} />);
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.getByRole("status").textContent).toBe("This native message observation is unavailable.");
});
