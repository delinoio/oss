// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useEntranceActivation } from "./entrance-motion";
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { ScrollPicker } from "./scroll-picker";

const query = () => ({
  loaded: true,
  nextPageToken: "next",
  append: vi.fn(),
  retry: vi.fn(),
  reload: vi.fn(),
});
const options = [
  { id: "a", label: "Equal name" },
  { id: "disabled", label: "Unavailable choice", disabled: true },
  { id: "b", label: "Equal name" },
];

it("preserves exact identities, disabled options and keyboard selection", () => {
  const change = vi.fn();
  render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={change}
      query={query()}
      active
    />,
  );
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  fireEvent.keyDown(trigger, { key: "End" });
  fireEvent.keyDown(trigger, { key: "Enter" });
  expect(change).toHaveBeenCalledExactlyOnceWith("b");
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  expect(
    (
      screen.getByRole("option", {
        name: "Unavailable choice",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getAllByRole("option", { name: "Equal name" })).toHaveLength(2);
  fireEvent.keyDown(trigger, { key: "Escape" });
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
});

it("keeps off-page selected labels and keyboard continuation without changing selection", () => {
  const state = query(),
    change = vi.fn();
  render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="off-page"
      selectedLabel="Retained runner"
      change={change}
      query={state}
      active
    />,
  );
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  expect(trigger.textContent).toBe("Retained runner");
  expect(trigger.dataset.value).toBe("off-page");
  fireEvent.click(trigger);
  const continuation = screen.getByRole("button", { name: "Load more Runner" });
  continuation.focus();
  fireEvent.keyDown(continuation, { key: "Tab" });
  expect(screen.getByRole("listbox")).toBeTruthy();
  fireEvent.click(continuation);
  expect(state.append).toHaveBeenCalledTimes(1);
  expect(change).not.toHaveBeenCalled();
  fireEvent.keyDown(continuation, { key: "Escape" });
  expect(document.activeElement).toBe(trigger);
});

it("closes inactive pickers and retains native required-field validation", () => {
  const view = (active: boolean) => (
    <form>
      <ScrollPicker
        options={options}
        label="Runner"
        value=""
        change={() => {}}
        query={query()}
        active={active}
        required
      />
    </form>
  );
  const result = render(view(true)),
    trigger = screen.getByRole("combobox", { name: "Runner" });
  expect(result.container.querySelector("form")!.checkValidity()).toBe(false);
  fireEvent.click(trigger);
  result.rerender(view(false));
  expect(screen.queryByRole("listbox")).toBeNull();
  expect((trigger as HTMLButtonElement).disabled).toBe(true);
});

it("uses one mounted manual top-layer listbox and repositions on resize/scroll without selection or page replacement", () => {
  const state = query(),
    change = vi.fn();
  render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={change}
      query={state}
      active
    />,
  );
  const trigger = screen.getByRole("combobox");
  fireEvent.click(trigger);
  const popup = screen.getByRole("listbox");
  expect(popup.getAttribute("popover")).toBe("manual");
  fireEvent.keyDown(trigger, { key: "End" });
  const highlighted = trigger.getAttribute("aria-activedescendant");
  fireEvent.resize(window);
  fireEvent.scroll(document);
  expect(screen.getByRole("listbox")).toBe(popup);
  expect(trigger.getAttribute("aria-activedescendant")).toBe(highlighted);
  expect(change).not.toHaveBeenCalled();
  expect(state.append).not.toHaveBeenCalled();
  expect(state.reload).not.toHaveBeenCalled();
  fireEvent.keyDown(trigger, { key: "Enter", isComposing: true });
  fireEvent.keyDown(trigger, {
    key: "Enter",
    keyCode: 229,
    isComposing: false,
  });
  expect(change).not.toHaveBeenCalled();
  expect(screen.getByRole("listbox")).toBe(popup);
  fireEvent.keyDown(trigger, { key: "Enter" });
  expect(change).toHaveBeenCalledExactlyOnceWith("b");
});

it("disposes positioning listeners and blocks stale selection on owning lifetime loss", () => {
  const change = vi.fn(),
    state = query();
  const view = render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={change}
      query={state}
      active
    />,
  );
  fireEvent.click(screen.getByRole("combobox"));
  view.unmount();
  fireEvent.resize(window);
  fireEvent.scroll(document);
  expect(change).not.toHaveBeenCalled();
  expect(screen.queryByRole("listbox")).toBeNull();
});

it("blocks queued selection when the original owner becomes inert before rendering", () => {
  const change = vi.fn();
  const view = render(
    <section>
      <ScrollPicker
        options={options}
        label="Runner"
        value="a"
        change={change}
        query={query()}
        active
      />
    </section>,
  );
  fireEvent.click(screen.getByRole("combobox"));
  const option = screen.getAllByRole("option", { name: "Equal name" })[1]!;
  view.container.querySelector("section")!.setAttribute("inert", "");
  fireEvent.click(option);
  expect(change).not.toHaveBeenCalled();
});

it("settles an enclosing page entrance before opening the picker popup", () => {
  function Page() {
    const entrance = useEntranceActivation("page:1");
    return (
      <section data-testid="page" {...entrance}>
        <ScrollPicker
          options={options}
          label="Runner"
          value="a"
          change={vi.fn()}
          query={query()}
          active
        />
      </section>
    );
  }
  render(<Page />);
  expect(screen.getByTestId("page").getAttribute("data-entrance")).toBe("y4");
  fireEvent.click(screen.getByRole("combobox", { name: "Runner" }));
  expect(screen.getByRole("listbox")).toBeTruthy();
  expect(screen.getByTestId("page").hasAttribute("data-entrance")).toBe(false);
});

it("moves the highlight with the keyboard without changing the accepted value", () => {
  const change = vi.fn();
  render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={change}
      query={query()}
      active
    />,
  );
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  const [first, , last] = screen.getAllByRole("option");
  expect(first.getAttribute("aria-selected")).toBe("true");
  expect(first.getAttribute("data-highlighted")).toBe("true");
  fireEvent.keyDown(trigger, { key: "End" });
  // The cue hooks move with the highlight; aria-selected keeps the accepted value.
  expect(first.getAttribute("data-highlighted")).toBe("false");
  expect(last.getAttribute("data-highlighted")).toBe("true");
  expect(first.getAttribute("aria-selected")).toBe("true");
  expect(last.getAttribute("aria-selected")).toBe("false");
  expect(change).not.toHaveBeenCalled();
});

it("includes an optional action in keyboard traversal after the choices", () => {
  const change = vi.fn(),
    invoke = vi.fn();
  render(
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={change}
      query={query()}
      active
      action={{ label: "New", name: "New runner", invoke }}
    />,
  );
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  const action = screen.getByRole("option", { name: "New runner" });
  // The action follows the choices and the continuation control in the popup.
  expect(screen.getByRole("listbox").lastElementChild).toBe(action);
  expect(action.textContent).toBe("New");
  expect(action.hasAttribute("data-picker-id")).toBe(false);
  // Disabled choices are skipped: a -> b -> action.
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  expect(trigger.getAttribute("aria-activedescendant")).toBe(action.id);
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  expect(trigger.getAttribute("aria-activedescendant")).toBe(action.id);
  fireEvent.keyDown(trigger, { key: "ArrowUp" });
  expect(trigger.getAttribute("aria-activedescendant")).not.toBe(action.id);
  fireEvent.keyDown(trigger, { key: "End" });
  fireEvent.keyDown(trigger, { key: " " });
  expect(invoke).toHaveBeenCalledTimes(1);
  expect(change).not.toHaveBeenCalled();
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  // Escape dismisses an open popup without invoking the action.
  fireEvent.keyDown(trigger, { key: "End" });
  fireEvent.keyDown(trigger, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(invoke).toHaveBeenCalledTimes(1);
});

it("keeps the action reachable when no choice is enabled and ignores it while inactive", () => {
  const invoke = vi.fn();
  const view = (active: boolean) => (
    <ScrollPicker
      options={[{ id: "off", label: "Off", disabled: true }]}
      label="Runner"
      value=""
      change={() => {}}
      query={query()}
      active={active}
      action={{ label: "New", name: "New runner", invoke }}
    />
  );
  const result = render(view(true)),
    trigger = screen.getByRole("combobox", { name: "Runner" });
  fireEvent.click(trigger);
  fireEvent.keyDown(trigger, { key: "Enter" });
  expect(invoke).toHaveBeenCalledTimes(1);
  fireEvent.click(trigger);
  const action = screen.getByRole("option", { name: "New runner" });
  result.rerender(view(false));
  fireEvent.click(action);
  expect(invoke).toHaveBeenCalledTimes(1);
});

it("reserves scroll clearance for the pinned action only when one is present", () => {
  const css = readFileSync(join(__dirname, "scroll-picker.css"), "utf8");
  expect(css).toMatch(
    /\.scroll-picker-popup\[data-has-action\]\s*\{[^}]*scroll-padding-bottom:\s*41px/,
  );
  const view = (withAction: boolean) => (
    <ScrollPicker
      options={options}
      label="Runner"
      value="a"
      change={() => {}}
      query={query()}
      active
      action={
        withAction
          ? { label: "New", name: "New runner", invoke() {} }
          : undefined
      }
    />
  );
  const result = render(view(true));
  fireEvent.click(screen.getByRole("combobox", { name: "Runner" }));
  expect(screen.getByRole("listbox").hasAttribute("data-has-action")).toBe(
    true,
  );
  result.rerender(view(false));
  expect(screen.getByRole("listbox").hasAttribute("data-has-action")).toBe(
    false,
  );
});

it("cancels the native Enter default so an enclosing form owner never submits from the picker", () => {
  const change = vi.fn(),
    submitted = vi.fn();
  render(
    <form
      noValidate
      onKeyDown={guardEnterSubmission}
      onSubmit={(event) => {
        if (consumeFormSubmit(event)) submitted();
      }}
    >
      <ScrollPicker
        options={options}
        label="Runner"
        value="a"
        change={change}
        query={query()}
        active
      />
      <button type="submit">Save</button>
    </form>,
  );
  const trigger = screen.getByRole("combobox", { name: "Runner" });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  fireEvent.keyDown(trigger, { key: "End" });
  // A canceled keydown (false) never reaches the browser's button activation.
  expect(fireEvent.keyDown(trigger, { key: "Enter" })).toBe(false);
  expect(change).toHaveBeenCalledExactlyOnceWith("b");
  expect(submitted).not.toHaveBeenCalled();
});
