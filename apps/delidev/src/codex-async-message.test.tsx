// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { codexAsyncMessage } from "@delinoio/delidev-api-client";
import { CodexAsyncContent, selectAsyncDraft, type AsyncDraftOwner } from "./codex-async-message";
const metadata = { version: 1, delivery_present: true, delivery: "async", questions_present: true, questions: [{ title: "null", options: null }, { title: "empty", options: [] }, { title: "ordered", options: ["second", "first"] }] };
it("decodes exact nullable question order and rejects unsafe/open metadata", () => {
 expect(codexAsyncMessage(metadata)?.questions).toEqual(metadata.questions);
 for (const value of [{ ...metadata, delivery: "sync" }, { ...metadata, extra: true }, { ...metadata, questions: [{ title: "q" }] }, { ...metadata, questions: [{ title: "q", options: [null] }] }, { ...metadata, questions: [{ title: "x".repeat(4097), options: null }] }]) expect(codexAsyncMessage(value)).toBeUndefined();
});
it("shows inert choices and only invokes explicit draft selection", () => {
 const select = vi.fn(); render(<CodexAsyncContent value={metadata} select={select} />);
 expect(screen.getByText("Asynchronous message")).toBeTruthy();
 const buttons = screen.getAllByRole("button"); expect(buttons.map(button => button.textContent)).toEqual(["second", "first"]); expect(select).not.toHaveBeenCalled();
 fireEvent.click(buttons[1]); expect(select).toHaveBeenCalledExactlyOnceWith("first");
});
it("keeps read-only retained questions inert", () => {
 render(<CodexAsyncContent value={metadata} />); expect(screen.getAllByRole("button").every(button => (button as HTMLButtonElement).disabled)).toBe(true);
});
it("changes only an empty unsent draft owned by the captured generation", () => {
 const owner: AsyncDraftOwner = { session: "original", transport: {}, connection: 1, generation: 3, draft: "", allowed: true }; const save = vi.fn();
 expect(selectAsyncDraft(owner, owner, "choice", save)).toBe(true); expect(save).toHaveBeenCalledExactlyOnceWith("choice"); save.mockClear();
 for (const current of [{ ...owner, draft: "human edit" }, { ...owner, session: "replacement" }, { ...owner, transport: {} }, { ...owner, generation: 4 }, { ...owner, connection: 2 }, { ...owner, allowed: false }]) expect(selectAsyncDraft(owner, current, "choice", save)).toBe(false);
 const existing = { ...owner, draft: "existing" }; expect(selectAsyncDraft(existing, existing, "choice", save)).toBe(false); expect(save).not.toHaveBeenCalled();
 expect(selectAsyncDraft(owner, owner, "choice", () => false)).toBe(false);
});
