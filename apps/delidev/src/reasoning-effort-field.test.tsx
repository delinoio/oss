// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { ReasoningEffortField, claudeEffortSuggestions, codexEffortSuggestions } from "./reasoning-effort-field";

function mount(initial?: string, suggestions: readonly string[] = codexEffortSuggestions) {
  const changed = vi.fn();
  const submitted = vi.fn();
  function Editor() {
    const [value, setValue] = useState(initial);
    return <form onSubmit={event => { event.preventDefault(); submitted(); }}><ReasoningEffortField label="Reasoning effort" value={value} suggestions={suggestions} change={next => { changed(next); setValue(next); }} /><button>Save</button></form>;
  }
  render(<Editor />);
  return { input: screen.getByRole("combobox", { name: "Reasoning effort" }) as HTMLInputElement, changed, submitted };
}
const options = () => within(screen.getByRole("listbox")).getAllByRole("option").map(row => row.textContent);

it.each([
  [codexEffortSuggestions, ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"]],
  [claudeEffortSuggestions, ["low", "medium", "high", "xhigh", "max"]],
  [[], []],
])("shows harness hints and native default without writing an unspecified value (%#)", (suggestions, expected) => {
  const { input, changed } = mount(undefined, suggestions);
  fireEvent.focus(input);
  expect(options()).toEqual(["Use native default", ...expected]);
  expect(input.getAttribute("aria-expanded")).toBe("true");
  expect(input.getAttribute("aria-describedby")).toBe(screen.getByText(/Choose a suggestion/).id);
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(input.value).toBe(""); expect(changed).not.toHaveBeenCalled();
});

it("filters case-insensitive prefixes while preserving exact direct input and clearing explicitly", () => {
  const { input, changed } = mount("future-effort");
  fireEvent.focus(input);
  expect(options()).toEqual(["Use native default"]);
  expect(screen.getByRole("status").textContent).toContain("No matching suggestions");
  expect(changed).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: " Hi " } });
  expect(options()).toEqual(["Use native default", "high"]);
  expect(input.value).toBe(" Hi "); expect(changed).toHaveBeenLastCalledWith(" Hi ");
  fireEvent.click(screen.getByRole("option", { name: "high" }));
  expect(input.value).toBe("high"); expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Show Reasoning effort suggestions" }));
  fireEvent.click(screen.getByRole("option", { name: "Use native default" }));
  expect(input.value).toBe(""); expect(changed).toHaveBeenLastCalledWith("");
});

it("selects by keyboard, scrolls the active item and closes without selecting or submitting", () => {
  const { input, changed, submitted } = mount();
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "ArrowUp" });
  const active = screen.getByRole("option", { name: "persistent" });
  expect(input.getAttribute("aria-activedescendant")).toBe(active.id);
  expect(active.getAttribute("aria-selected")).toBe("true");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(changed).toHaveBeenLastCalledWith("persistent");
  fireEvent.change(input, { target: { value: "raw-value" } });
  const count = changed.mock.calls.length;
  fireEvent.keyDown(input, { key: "Enter" });
  expect(changed).toHaveBeenCalledTimes(count); expect(submitted).not.toHaveBeenCalled();
  expect(input.value).toBe("raw-value"); expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "Tab" });
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(input); fireEvent.blur(input, { relatedTarget: screen.getByRole("button", { name: "Save" }) });
  expect(screen.queryByRole("listbox")).toBeNull(); expect(changed).toHaveBeenCalledTimes(count);
});

it("does not consume IME composition keys or commit their active suggestion", () => {
  const { input, changed } = mount();
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "ArrowDown" });
  const descendant = input.getAttribute("aria-activedescendant");
  const enter = new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true });
  fireEvent(input, enter);
  expect(enter.defaultPrevented).toBe(false);
  fireEvent.keyDown(input, { key: "ArrowDown", isComposing: true });
  expect(input.getAttribute("aria-activedescendant")).toBe(descendant);
  fireEvent.keyDown(input, { key: "Enter", keyCode: 229 });
  expect(changed).not.toHaveBeenCalled(); expect(screen.getByRole("listbox")).toBeTruthy();
});

it("updates candidates and drops the active item without rewriting retained input", () => {
  const changed = vi.fn();
  const view = render(<ReasoningEffortField label="Reasoning effort" value="hi" suggestions={codexEffortSuggestions} change={changed} />);
  const input = screen.getByRole("combobox");
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(input.getAttribute("aria-activedescendant")).toBeTruthy();
  view.rerender(<ReasoningEffortField label="Reasoning effort" value="hi" suggestions={[]} change={changed} />);
  expect(options()).toEqual(["Use native default"]);
  expect(input.getAttribute("aria-activedescendant")).toBeNull();
  fireEvent.keyDown(input, { key: "Enter" });
  expect((input as HTMLInputElement).value).toBe("hi"); expect(changed).not.toHaveBeenCalled();
});

it("keeps disabled values and applies inherited fieldset locks to custom rows", () => {
  const changed = vi.fn();
  const view = render(<fieldset><ReasoningEffortField label="Subagent effort" value="old-effort" suggestions={codexEffortSuggestions} change={changed} /></fieldset>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  fireEvent.focus(input); expect(screen.getByRole("listbox")).toBeTruthy();
  view.rerender(<fieldset disabled><ReasoningEffortField label="Subagent effort" value="old-effort" suggestions={codexEffortSuggestions} change={changed} /></fieldset>);
  expect(screen.queryByRole("listbox")).toBeNull(); expect(input.matches(":disabled")).toBe(true);
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(screen.queryByRole("listbox")).toBeNull(); expect(changed).not.toHaveBeenCalled();
  view.rerender(<ReasoningEffortField label="Subagent effort" value="old-effort" disabled suggestions={codexEffortSuggestions} change={changed} />);
  expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("old-effort");
});

it("uses independent accessible identities for primary and subagent inputs", () => {
  render(<><ReasoningEffortField label="Reasoning effort" value="" change={() => {}} /><ReasoningEffortField label="Subagent effort" value="" change={() => {}} /></>);
  const primary = screen.getByRole("combobox", { name: "Reasoning effort" });
  const child = screen.getByRole("combobox", { name: "Subagent effort" });
  expect(primary.id).not.toBe(child.id);
  fireEvent.focus(primary); const first = primary.getAttribute("aria-controls");
  fireEvent.blur(primary, { relatedTarget: child }); fireEvent.focus(child);
  expect(child.getAttribute("aria-controls")).not.toBe(first);
  expect(screen.getByRole("listbox", { name: "Subagent effort suggestions" }).id).toBe(child.getAttribute("aria-controls"));
});
