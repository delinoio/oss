// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState, type ReactNode } from "react";
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { pressEnter } from "./test-form-enter";
import { afterEach, expect, it, vi } from "vitest";
import {
  ReasoningEffortField,
  claudeEffortSuggestions,
  codexEffortSuggestions,
} from "./reasoning-effort-field";

afterEach(() => {
  delete (HTMLElement.prototype as { showPopover?: unknown }).showPopover;
  delete (HTMLElement.prototype as { hidePopover?: unknown }).hidePopover;
});
function stubPopover() {
  const show = vi.fn(),
    hide = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "showPopover", {
    configurable: true,
    value: show,
  });
  Object.defineProperty(HTMLElement.prototype, "hidePopover", {
    configurable: true,
    value: hide,
  });
  return { show, hide };
}
// Everything outside the popup, with typed values removed, is the surrounding
// layout. Opening, filtering and dismissing must leave it byte-for-byte equal.
function surrounding(container: HTMLElement) {
  const clone = container.cloneNode(true) as HTMLElement;
  clone.querySelectorAll(".reasoning-effort-popup").forEach((node) => {
    node.remove();
  });
  return clone.innerHTML
    .replace(/ value="[^"]*"/g, "")
    .replace(/ aria-(expanded|controls|activedescendant)="[^"]*"/g, "")
    .replace(/ aria-label="(Show|Hide) /g, ' aria-label="Toggle ');
}

function mount(
  initial?: string,
  suggestions: readonly string[] = codexEffortSuggestions,
) {
  const changed = vi.fn();
  const submitted = vi.fn();
  function Editor() {
    const [value, setValue] = useState(initial);
    return (
      <form
        onSubmit={(event) => {
          event.preventDefault();
          submitted();
        }}
      >
        <ReasoningEffortField
          label="Reasoning effort"
          value={value}
          suggestions={suggestions}
          change={(next) => {
            changed(next);
            setValue(next);
          }}
        />
        <button>Save</button>
      </form>
    );
  }
  render(<Editor />);
  return {
    input: screen.getByRole("combobox", {
      name: "Reasoning effort",
    }) as HTMLInputElement,
    changed,
    submitted,
  };
}
const options = () =>
  within(screen.getByRole("listbox"))
    .getAllByRole("option")
    .map((row) => row.textContent);

it.each([
  [
    codexEffortSuggestions,
    [
      "none",
      "minimal",
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
      "ultra",
      "persistent",
    ],
  ],
  [claudeEffortSuggestions, ["low", "medium", "high", "xhigh", "max"]],
  [[], []],
])(
  "shows harness hints and native default without writing an unspecified value (%#)",
  (suggestions, expected) => {
    const { input, changed } = mount(undefined, suggestions);
    fireEvent.focus(input);
    expect(options()).toEqual(["Use native default", ...expected]);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toBe(
      screen.getByText(/Choose a suggestion/).id,
    );
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input.value).toBe("");
    expect(changed).not.toHaveBeenCalled();
  },
);

it("filters case-insensitive prefixes while preserving exact direct input and clearing explicitly", () => {
  const { input, changed } = mount("future-effort");
  fireEvent.focus(input);
  expect(options()).toEqual(["Use native default"]);
  expect(screen.getByRole("status").textContent).toContain(
    "No matching suggestions",
  );
  expect(changed).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: " Hi " } });
  expect(options()).toEqual(["Use native default", "high"]);
  expect(input.value).toBe(" Hi ");
  expect(changed).toHaveBeenLastCalledWith(" Hi ");
  fireEvent.click(screen.getByRole("option", { name: "high" }));
  expect(input.value).toBe("high");
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Show Reasoning effort suggestions" }),
  );
  fireEvent.click(screen.getByRole("option", { name: "Use native default" }));
  expect(input.value).toBe("");
  expect(changed).toHaveBeenLastCalledWith("");
});

it("selects by keyboard, scrolls the active item and closes without selecting or submitting", () => {
  const { input, changed, submitted } = mount();
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "ArrowUp" });
  const active = screen.getByRole("option", { name: "persistent" });
  expect(input.getAttribute("aria-activedescendant")).toBe(active.id);
  expect(active.getAttribute("aria-selected")).toBe("true");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(changed).toHaveBeenLastCalledWith("persistent");
  fireEvent.change(input, { target: { value: "raw-value" } });
  const count = changed.mock.calls.length;
  fireEvent.keyDown(input, { key: "Enter" });
  expect(changed).toHaveBeenCalledTimes(count);
  expect(submitted).not.toHaveBeenCalled();
  expect(input.value).toBe("raw-value");
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "Tab" });
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(input);
  fireEvent.blur(input, {
    relatedTarget: screen.getByRole("button", { name: "Save" }),
  });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(changed).toHaveBeenCalledTimes(count);
});

it("does not consume IME composition keys or commit their active suggestion", () => {
  const { input, changed } = mount();
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  const descendant = input.getAttribute("aria-activedescendant");
  const enter = new KeyboardEvent("keydown", {
    key: "Enter",
    isComposing: true,
    bubbles: true,
    cancelable: true,
  });
  fireEvent(input, enter);
  expect(enter.defaultPrevented).toBe(false);
  fireEvent.keyDown(input, { key: "ArrowDown", isComposing: true });
  expect(input.getAttribute("aria-activedescendant")).toBe(descendant);
  fireEvent.keyDown(input, { key: "Enter", keyCode: 229 });
  expect(changed).not.toHaveBeenCalled();
  expect(screen.getByRole("listbox")).toBeTruthy();
});

it("updates candidates and drops the active item without rewriting retained input", () => {
  const changed = vi.fn();
  const view = render(
    <ReasoningEffortField
      label="Reasoning effort"
      value="hi"
      suggestions={codexEffortSuggestions}
      change={changed}
    />,
  );
  const input = screen.getByRole("combobox");
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(input.getAttribute("aria-activedescendant")).toBeTruthy();
  view.rerender(
    <ReasoningEffortField
      label="Reasoning effort"
      value="hi"
      suggestions={[]}
      change={changed}
    />,
  );
  expect(options()).toEqual(["Use native default"]);
  expect(input.getAttribute("aria-activedescendant")).toBeNull();
  fireEvent.keyDown(input, { key: "Enter" });
  expect((input as HTMLInputElement).value).toBe("hi");
  expect(changed).not.toHaveBeenCalled();
});

it("keeps disabled values and applies inherited fieldset locks to custom rows", () => {
  const changed = vi.fn();
  const view = render(
    <fieldset>
      <ReasoningEffortField
        label="Subagent effort"
        value="old-effort"
        suggestions={codexEffortSuggestions}
        change={changed}
      />
    </fieldset>,
  );
  const input = screen.getByRole("combobox") as HTMLInputElement;
  fireEvent.focus(input);
  expect(screen.getByRole("listbox")).toBeTruthy();
  view.rerender(
    <fieldset disabled>
      <ReasoningEffortField
        label="Subagent effort"
        value="old-effort"
        suggestions={codexEffortSuggestions}
        change={changed}
      />
    </fieldset>,
  );
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(input.matches(":disabled")).toBe(true);
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(changed).not.toHaveBeenCalled();
  view.rerender(
    <ReasoningEffortField
      label="Subagent effort"
      value="old-effort"
      disabled
      suggestions={codexEffortSuggestions}
      change={changed}
    />,
  );
  expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(
    true,
  );
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe(
    "old-effort",
  );
});

it("uses independent accessible identities for primary and subagent inputs", () => {
  render(
    <>
      <ReasoningEffortField
        label="Reasoning effort"
        value=""
        change={() => {}}
      />
      <ReasoningEffortField
        label="Subagent effort"
        value=""
        change={() => {}}
      />
    </>,
  );
  const primary = screen.getByRole("combobox", { name: "Reasoning effort" });
  const child = screen.getByRole("combobox", { name: "Subagent effort" });
  expect(primary.id).not.toBe(child.id);
  fireEvent.focus(primary);
  const first = primary.getAttribute("aria-controls");
  fireEvent.blur(primary, { relatedTarget: child });
  fireEvent.focus(child);
  expect(child.getAttribute("aria-controls")).not.toBe(first);
  expect(
    screen.getByRole("listbox", { name: "Subagent effort suggestions" }).id,
  ).toBe(child.getAttribute("aria-controls"));
});

it("lets the open suggestion list consume Enter before an enclosing form owner and submits afterwards", () => {
  const submitted = vi.fn();
  function Owner() {
    const [value, setValue] = useState<string | undefined>("");
    return (
      <form
        noValidate
        onKeyDown={guardEnterSubmission}
        onSubmit={(event) => {
          if (consumeFormSubmit(event)) submitted();
        }}
      >
        <ReasoningEffortField
          label="Reasoning effort"
          value={value}
          suggestions={codexEffortSuggestions}
          change={setValue}
        />
        <button type="submit">Save</button>
      </form>
    );
  }
  render(<Owner />);
  const input = screen.getByRole("combobox", { name: "Reasoning effort" });
  fireEvent.focus(input);
  expect(input.getAttribute("aria-expanded")).toBe("true");
  // The open list consumes this Enter, so the form owner never sees a submit.
  expect(pressEnter(input)).toBe(0);
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(submitted).not.toHaveBeenCalled();
  // With the list closed, the next plain Enter is an ordinary submission.
  expect(pressEnter(input)).toBe(1);
  expect(submitted).toHaveBeenCalledTimes(1);
});

it("overlays the suggestions and empty status in a top-layer popup that reserves no layout", () => {
  const { show, hide } = stubPopover();
  const changed = vi.fn();
  function Host({ value }: { value: string }) {
    return (
      <form>
        <p>Before</p>
        <ReasoningEffortField
          label="Reasoning effort"
          value={value}
          suggestions={codexEffortSuggestions}
          change={changed}
        />
        <button>Following control</button>
      </form>
    );
  }
  const { container, rerender } = render(<Host value="" />);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  const before = surrounding(container);
  expect(container.querySelector(".reasoning-effort-popup")).toBeNull();
  fireEvent.focus(input);
  const popup = container.querySelector<HTMLElement>(
    ".reasoning-effort-popup",
  )!;
  expect(popup.getAttribute("popover")).toBe("manual");
  expect(popup.classList.contains("anchored-popup")).toBe(true);
  // The popup stays in the field subtree and owns both the listbox and status.
  expect(popup.closest(".reasoning-effort-field")).not.toBeNull();
  expect(popup.contains(screen.getByRole("listbox"))).toBe(true);
  expect(input.getAttribute("aria-controls")).toBe(
    screen.getByRole("listbox").id,
  );
  expect(show).toHaveBeenCalledOnce();
  expect(surrounding(container)).toBe(before);
  // Filtering to no candidates keeps the status inside the popup.
  rerender(<Host value="no-such-effort" />);
  expect(popup.contains(screen.getByRole("status"))).toBe(true);
  expect(surrounding(container)).toBe(before);
  fireEvent.keyDown(input, { key: "Escape" });
  expect(container.querySelector(".reasoning-effort-popup")).toBeNull();
  expect(hide).toHaveBeenCalled();
  expect(surrounding(container)).toBe(before);
  expect(changed).not.toHaveBeenCalled();
});

it("keeps the typed draft and suggestion selection unchanged by opening, filtering and every dismissal path", () => {
  stubPopover();
  const { input, changed, submitted } = mount("x-draft");
  const dismissals: Array<() => void> = [
    () => fireEvent.keyDown(input, { key: "Escape" }),
    () => fireEvent.keyDown(input, { key: "Tab" }),
    () =>
      fireEvent.blur(input, {
        relatedTarget: screen.getByRole("button", { name: "Save" }),
      }),
    () => fireEvent.keyDown(input, { key: "Enter" }),
    () =>
      fireEvent.click(
        screen.getByRole("button", {
          name: "Hide Reasoning effort suggestions",
        }),
      ),
  ];
  for (const dismiss of dismissals) {
    fireEvent.focus(input);
    fireEvent.click(input);
    expect(screen.getByRole("listbox")).toBeTruthy();
    dismiss();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input.value).toBe("x-draft");
  }
  expect(changed).not.toHaveBeenCalled();
  expect(submitted).not.toHaveBeenCalled();
  // Selecting consumes Enter before the owning form submits.
  fireEvent.focus(input);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(fireEvent.keyDown(input, { key: "Enter" })).toBe(false);
  expect(changed).toHaveBeenCalledTimes(1);
  expect(submitted).not.toHaveBeenCalled();
});

it("keeps pointer presses inside the popup from taking focus away from the input", () => {
  stubPopover();
  const { input, changed } = mount();
  act(() => input.focus());
  const popup = screen.getByRole("listbox").parentElement!;
  // A scrollbar press targets the popup itself rather than an option.
  expect(fireEvent.mouseDown(popup)).toBe(false);
  expect(fireEvent.mouseDown(screen.getAllByRole("option")[0]!)).toBe(false);
  expect(screen.getByRole("listbox")).toBeTruthy();
  expect(document.activeElement).toBe(input);
  expect(changed).not.toHaveBeenCalled();
});

it("hides the popup and releases document listeners on unmount while open", () => {
  const { hide } = stubPopover();
  const added = vi.spyOn(document, "addEventListener"),
    removed = vi.spyOn(document, "removeEventListener");
  const view = render(
    <ReasoningEffortField
      label="Reasoning effort"
      value=""
      suggestions={codexEffortSuggestions}
      change={() => {}}
    />,
  );
  const count = (spy: typeof added) =>
    spy.mock.calls.filter(([type]) => type === "scroll").length;
  act(() => screen.getByRole("combobox").focus());
  expect(count(added)).toBe(1);
  view.unmount();
  expect(count(removed)).toBe(1);
  expect(hide).toHaveBeenCalled();
  added.mockRestore();
  removed.mockRestore();
});

const hosts: Array<[string, (children: ReactNode) => ReactNode]> = [
  [
    "a page host",
    (children) => (
      <main className="settings-content">
        <div className="settings-task-body">{children}</div>
      </main>
    ),
  ],
  [
    "a Settings task dialog host",
    (children) => (
      <dialog open className="settings-task-dialog">
        <div className="settings-task-body">{children}</div>
      </dialog>
    ),
  ],
];
it.each(hosts)(
  "presents and dismisses the popup for primary and subagent fields inside %s",
  (_name, wrap) => {
    const { show } = stubPopover();
    const changed = vi.fn();
    const { container } = render(
      wrap(
        <fieldset>
          <ReasoningEffortField
            label="Reasoning effort"
            value=""
            suggestions={codexEffortSuggestions}
            change={changed}
          />
          <ReasoningEffortField
            label="Subagent effort"
            value="seed"
            suggestions={codexEffortSuggestions}
            change={changed}
          />
        </fieldset>,
      ),
    );
    for (const name of ["Reasoning effort", "Subagent effort"]) {
      const input = screen.getByRole("combobox", { name });
      act(() => input.focus());
      expect(
        container.querySelectorAll(".reasoning-effort-popup"),
      ).toHaveLength(1);
      expect(
        screen.getByRole("listbox", { name: `${name} suggestions` }),
      ).toBeTruthy();
      fireEvent.keyDown(input, { key: "Escape" });
      expect(container.querySelector(".reasoning-effort-popup")).toBeNull();
    }
    expect(show).toHaveBeenCalledTimes(2);
    expect(changed).not.toHaveBeenCalled();
  },
);

it("closes the popup when its Settings task dialog closes while open", async () => {
  stubPopover();
  const { container } = render(
    <dialog open className="settings-task-dialog">
      <div className="settings-task-body">
        <ReasoningEffortField
          label="Reasoning effort"
          value=""
          suggestions={codexEffortSuggestions}
          change={() => {}}
        />
      </div>
    </dialog>,
  );
  act(() => screen.getByRole("combobox").focus());
  expect(screen.getByRole("listbox")).toBeTruthy();
  container.querySelector("dialog")!.removeAttribute("open");
  await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
});
