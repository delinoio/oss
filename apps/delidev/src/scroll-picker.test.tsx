// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ScrollPicker } from "./scroll-picker";

const query = () => ({ loaded: true, nextPageToken: "next", append: vi.fn(), retry: vi.fn(), reload: vi.fn() });
const options = [{ id: "a", label: "Equal name" }, { id: "disabled", label: "Unavailable choice", disabled: true }, { id: "b", label: "Equal name" }];

it("preserves exact identities, disabled options and keyboard selection", () => {
  const change = vi.fn();
  render(<ScrollPicker options={options} label="Runner" value="a" change={change} query={query()} active />);
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  trigger.focus(); fireEvent.keyDown(trigger, { key: "ArrowDown" }); fireEvent.keyDown(trigger, { key: "End" }); fireEvent.keyDown(trigger, { key: "Enter" });
  expect(change).toHaveBeenCalledExactlyOnceWith("b");
  expect(screen.queryByRole("listbox")).toBeNull(); expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  expect((screen.getByRole("option", { name: "Unavailable choice" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getAllByRole("option", { name: "Equal name" })).toHaveLength(2);
  fireEvent.keyDown(trigger, { key: "Escape" }); expect(trigger.getAttribute("aria-expanded")).toBe("false");
});

it("keeps off-page selected labels and keyboard continuation without changing selection", () => {
  const state = query(), change = vi.fn();
  render(<ScrollPicker options={options} label="Runner" value="off-page" selectedLabel="Retained runner" change={change} query={state} active />);
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  expect(trigger.textContent).toBe("Retained runner"); expect(trigger.dataset.value).toBe("off-page");
  fireEvent.click(trigger);
  const continuation = screen.getByRole("button", { name: "Load more Runner" });
  continuation.focus(); fireEvent.keyDown(continuation, { key: "Tab" });
  expect(screen.getByRole("listbox")).toBeTruthy();
  fireEvent.click(continuation); expect(state.append).toHaveBeenCalledTimes(1); expect(change).not.toHaveBeenCalled();
  fireEvent.keyDown(continuation, { key: "Escape" }); expect(document.activeElement).toBe(trigger);
});

it("closes inactive pickers and retains native required-field validation", () => {
  const view = (active: boolean) => <form><ScrollPicker options={options} label="Runner" value="" change={() => {}} query={query()} active={active} required /></form>;
  const result = render(view(true)), trigger = screen.getByRole("combobox", { name: "Runner" });
  expect(result.container.querySelector("form")!.checkValidity()).toBe(false);
  fireEvent.click(trigger); result.rerender(view(false));
  expect(screen.queryByRole("listbox")).toBeNull(); expect((trigger as HTMLButtonElement).disabled).toBe(true);
});
