// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import {
  bindingAria,
  bindingKeys,
  bindingMatches,
  dispatchShortcut,
  globalShortcutBindings,
  sessionShortcutBindings,
  ShortcutExecution,
  ShortcutId,
  ShortcutInput,
  ShortcutPlatform,
  ShortcutScope,
  ShortcutStore,
  type ShortcutDefinition,
} from "./shortcuts";
import { Surface } from "./surface";
import {
  effectiveShortcutDefinitions,
  ShortcutOverrideState,
} from "./shortcut-preferences";

const platform = ShortcutPlatform.Other;
function definition(
  extra: Partial<ShortcutDefinition> = {},
): ShortcutDefinition {
  return {
    id: ShortcutId.NewSession,
    scope: ShortcutScope.Global,
    label: "shortcuts.newSession",
    bindings: [{ key: "k", primary: true }],
    run: vi.fn(),
    ...extra,
  };
}
function dispatch(
  items: ShortcutDefinition[],
  options: KeyboardEventInit = {},
  target: HTMLElement = document.body,
) {
  const event = new KeyboardEvent("keydown", {
    bubbles: true,
    cancelable: true,
    key: "k",
    ctrlKey: true,
    ...options,
  });
  const handle = () => dispatchShortcut(event, items, Surface.Search, platform);
  target.addEventListener("keydown", handle, { once: true });
  target.dispatchEvent(event);
  return event;
}
it("resolves exact local primary modifiers and logical question marks", () => {
  expect(
    bindingMatches(
      new KeyboardEvent("keydown", { key: "K", metaKey: true }),
      { key: "k", primary: true },
      ShortcutPlatform.Mac,
    ),
  ).toBe(true);
  expect(
    bindingMatches(
      new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
      { key: "k", primary: true },
      ShortcutPlatform.Mac,
    ),
  ).toBe(false);
  for (const options of [
    { shiftKey: true },
    { altKey: true },
    { metaKey: true },
  ])
    expect(
      bindingMatches(
        new KeyboardEvent("keydown", { key: "k", ctrlKey: true, ...options }),
        { key: "k", primary: true },
        platform,
      ),
    ).toBe(false);
  for (const shiftKey of [false, true])
    expect(
      bindingMatches(
        new KeyboardEvent("keydown", { key: "?", shiftKey }),
        { key: "?" },
        platform,
      ),
    ).toBe(true);
  expect(
    bindingKeys({ key: "n", primary: true, shift: true }, ShortcutPlatform.Mac),
  ).toEqual(["⌘", "⇧", "N"]);
  expect(bindingAria({ key: "Enter", primary: true }, platform)).toBe(
    "Control+Enter",
  );
  expect(
    bindingKeys(globalShortcutBindings[ShortcutId.Help][0], platform),
  ).toEqual(["?"]);
  expect(
    bindingAria(globalShortcutBindings[ShortcutId.Help][0], platform),
  ).toBe("Shift+/");
});
it.each([{ isComposing: true }, { keyCode: 229 }, { repeat: true }])(
  "ignores composition and repeated events %j",
  (options) => {
    const item = definition();
    dispatch([item], options);
    expect(item.run).not.toHaveBeenCalled();
  },
);
it("ignores handled events and AltGraph without preventing native input", () => {
  const item = definition();
  const event = new KeyboardEvent("keydown", {
    key: "k",
    ctrlKey: true,
    cancelable: true,
  });
  event.preventDefault();
  expect(dispatchShortcut(event, [item], Surface.Search, platform)).toBe(false);
  const altGraph = new KeyboardEvent("keydown", { key: "k", ctrlKey: true });
  vi.spyOn(altGraph, "getModifierState").mockImplementation(
    (key) => key === "AltGraph",
  );
  expect(dispatchShortcut(altGraph, [item], Surface.Search, platform)).toBe(
    false,
  );
  expect(item.run).not.toHaveBeenCalled();
});
it("gives target then screen precedence and never falls through a disabled match", () => {
  const input = document.createElement("input");
  document.body.append(input);
  const global = definition({ input: ShortcutInput.Allow });
  const screen = definition({
    id: ShortcutId.SearchFocus,
    scope: Surface.Search,
    input: ShortcutInput.Allow,
  });
  const local = definition({
    id: ShortcutId.SearchSubmit,
    scope: Surface.Search,
    target: { current: input },
    input: ShortcutInput.Target,
  });
  dispatch([global, screen, local], {}, input);
  expect(local.run).toHaveBeenCalledTimes(1);
  expect(screen.run).not.toHaveBeenCalled();
  expect(global.run).not.toHaveBeenCalled();
  local.enabled = false;
  dispatch([global, screen, local], {}, input);
  expect(local.run).toHaveBeenCalledTimes(1);
  expect(screen.run).not.toHaveBeenCalled();
  input.remove();
  dispatch([global, screen]);
  expect(screen.run).toHaveBeenCalledTimes(1);
  expect(global.run).not.toHaveBeenCalled();
});
it("blocks conflicts and logs only stable action metadata", () => {
  const log = vi.spyOn(console, "warn").mockImplementation(() => {});
  const first = definition(),
    second = definition({ id: ShortcutId.Help });
  dispatch([first, second]);
  expect(first.run).not.toHaveBeenCalled();
  expect(second.run).not.toHaveBeenCalled();
  expect(log).toHaveBeenCalledWith({
    event: "delidev_shortcut_conflict",
    scope: ShortcutScope.Global,
    actionIds: [ShortcutId.NewSession, ShortcutId.Help],
  });
});
it.each(["input", "textarea", "select", "editable", "textbox", "combobox"])(
  "preserves question mark text in %s",
  (kind) => {
    const element = document.createElement(
      ["input", "textarea", "select"].includes(kind) ? kind : "div",
    );
    if (kind === "editable") element.setAttribute("contenteditable", "true");
    if (kind === "textbox" || kind === "combobox")
      element.setAttribute("role", kind);
    document.body.append(element);
    const help = definition({ bindings: [{ key: "?" }] });
    expect(
      dispatch([help], { key: "?", ctrlKey: false }, element).defaultPrevented,
    ).toBe(false);
    expect(help.run).not.toHaveBeenCalled();
    element.remove();
  },
);
it("preserves native behavior and excludes inactive, hidden and passthrough targets", () => {
  const input = document.createElement("textarea");
  document.body.append(input);
  const native = definition({
    bindings: [{ key: "Enter" }],
    execution: ShortcutExecution.Native,
    target: { current: input },
    input: ShortcutInput.Target,
  });
  expect(
    dispatch([native], { key: "Enter", ctrlKey: false }, input)
      .defaultPrevented,
  ).toBe(false);
  expect(native.run).not.toHaveBeenCalled();
  const item = definition({ input: ShortcutInput.Allow });
  input.dataset.shortcuts = "passthrough";
  dispatch([item], {}, input);
  delete input.dataset.shortcuts;
  input.hidden = true;
  dispatch([item], {}, input);
  input.remove();
  dispatch([item], {});
  expect(item.run).toHaveBeenCalledTimes(1);
  dispatch([
    definition({ active: false }),
    definition({ scope: Surface.Sessions }),
  ]);
});
it("blocks modals and compact drawers but permits a wide sidebar region", () => {
  const dialog = document.createElement("dialog");
  dialog.setAttribute("open", "");
  document.body.append(dialog);
  const item = definition();
  dispatch([item]);
  expect(item.run).not.toHaveBeenCalled();
  dialog.setAttribute("role", "region");
  dispatch([item]);
  expect(item.run).toHaveBeenCalledTimes(1);
  dialog.removeAttribute("role");
  dialog.hidden = true;
  dispatch([item]);
  expect(item.run).toHaveBeenCalledTimes(2);
  dialog.remove();
});
it("scopes snapshots and removes original registration owners independently", () => {
  const store = new ShortcutStore(),
    first = Symbol(),
    second = Symbol();
  store.register(first, [definition()]);
  store.register(second, [
    definition({ id: ShortcutId.SearchFocus, scope: Surface.Search }),
    definition({ active: false }),
  ]);
  expect(store.getSnapshot().map((item) => item.id)).toEqual([
    ShortcutId.NewSession,
  ]);
  store.setSurface(Surface.Search);
  expect(store.getSnapshot()).toHaveLength(2);
  store.remove(first);
  expect(store.getSnapshot().map((item) => item.id)).toEqual([
    ShortcutId.SearchFocus,
  ]);
  store.remove(second);
  expect(store.getSnapshot()).toEqual([]);
});

it("admits only the numeric tab definitions from terminal input and consumes once", () => {
  const input = document.createElement("textarea");
  input.dataset.shortcuts = "passthrough";
  document.body.append(input);
  const tab = definition({
    id: ShortcutId.SessionTab1,
    input: ShortcutInput.Allow,
    terminal: true,
    bindings: [{ key: "1", primary: true }],
  });
  const other = definition({ input: ShortcutInput.Allow });
  expect(dispatch([tab, other], { key: "1" }, input).defaultPrevented).toBe(
    true,
  );
  expect(tab.run).toHaveBeenCalledTimes(1);
  dispatch([tab, other], { key: "1", repeat: true }, input);
  dispatch([tab, other], { key: "1", isComposing: true }, input);
  dispatch([tab, other], { key: "1", shiftKey: true }, input);
  dispatch([tab, other], {}, input);
  expect(tab.run).toHaveBeenCalledTimes(1);
  expect(other.run).not.toHaveBeenCalled();
  input.remove();
});

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "keeps fixed send and native newline with every primary override on %s",
  (platform) => {
    const input = document.createElement("textarea");
    document.body.append(input);
    input.focus();
    const run = vi.fn(),
      definitions: ShortcutDefinition[] = [
        {
          id: ShortcutId.SessionSend,
          scope: Surface.Sessions,
          label: "shortcuts.queueMessage",
          bindings: [{ key: "Enter" }, { key: "Enter", primary: true }],
          target: { current: input },
          input: ShortcutInput.Target,
          run,
        },
        {
          id: ShortcutId.SessionNewline,
          scope: Surface.Sessions,
          label: "shortcuts.newline",
          bindings: [{ key: "Enter", shift: true }],
          target: { current: input },
          input: ShortcutInput.Target,
          execution: ShortcutExecution.Native,
        },
      ];
    const primary =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    for (const override of [
      undefined,
      {
        state: ShortcutOverrideState.Binding,
        chord: { key: "j", shift: true },
      } as const,
      { state: ShortcutOverrideState.Disabled } as const,
    ]) {
      const actions = effectiveShortcutDefinitions(
        definitions,
        override ? { [ShortcutId.SessionSend]: override } : {},
      );
      const press = (options: KeyboardEventInit = {}) => {
        const event = new KeyboardEvent("keydown", {
          key: "Enter",
          bubbles: true,
          cancelable: true,
          ...options,
        });
        input.addEventListener(
          "keydown",
          () => dispatchShortcut(event, actions, Surface.Sessions, platform),
          { once: true },
        );
        input.dispatchEvent(event);
        return event;
      };
      const before = run.mock.calls.length;
      expect(press().defaultPrevented).toBe(true);
      expect(run).toHaveBeenCalledTimes(before + 1);
      expect(press({ shiftKey: true }).defaultPrevented).toBe(false);
      expect(run).toHaveBeenCalledTimes(before + 1);
      for (const flags of [
        { isComposing: true },
        { keyCode: 229 },
        { repeat: true },
      ])
        press(flags);
      const handled = new KeyboardEvent("keydown", {
        key: "Enter",
        cancelable: true,
      });
      handled.preventDefault();
      expect(
        dispatchShortcut(handled, actions, Surface.Sessions, platform),
      ).toBe(false);
      expect(run).toHaveBeenCalledTimes(before + 1);
      if (override?.state !== ShortcutOverrideState.Disabled) {
        press({
          ...primary,
          ...(override ? { key: "j", shiftKey: true } : {}),
        });
        expect(run).toHaveBeenCalledTimes(before + 2);
      }
      actions[0]!.enabled = false;
      expect(press().defaultPrevented).toBe(true);
    }
    input.remove();
  },
);

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "dispatches physical primary chords once across input layouts on %s",
  (localPlatform) => {
    const modifiers =
      localPlatform === ShortcutPlatform.Mac
        ? { metaKey: true }
        : { ctrlKey: true };
    const input = document.createElement("textarea");
    document.body.append(input);
    for (const [key, translated, code, shiftKey] of [
      ["k", "ㅏ", "KeyK", false],
      ["b", "ㅠ", "KeyB", false],
      ["i", "ㅑ", "KeyI", false],
      ["n", "ㅜ", "KeyN", true],
      ["1", "!", "Digit1", true],
    ] as const) {
      const item = definition({
        input: ShortcutInput.Allow,
        bindings: [{ key, primary: true, shift: shiftKey }],
      });
      for (const logicalKey of [key, translated]) {
        const event = new KeyboardEvent("keydown", {
          key: logicalKey,
          code,
          shiftKey,
          ...modifiers,
          bubbles: true,
          cancelable: true,
        });
        input.addEventListener(
          "keydown",
          () => dispatchShortcut(event, [item], Surface.Search, localPlatform),
          { once: true },
        );
        input.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(true);
        expect(
          dispatchShortcut(event, [item], Surface.Search, localPlatform),
        ).toBe(false);
      }
      expect(item.run).toHaveBeenCalledTimes(2);
    }
    input.remove();
  },
);
it("prefers physical alphanumeric codes and preserves named, logical Help and Numpad keys", () => {
  const matches = (
    key: string,
    code: string,
    binding: string,
    primary = true,
  ) =>
    bindingMatches(
      new KeyboardEvent("keydown", { key, code, ctrlKey: primary }),
      { key: binding, primary },
      platform,
    );
  expect(matches("k", "KeyJ", "j")).toBe(true);
  expect(matches("k", "KeyJ", "k")).toBe(false);
  for (const code of ["", "Unidentified", "Numpad1"])
    expect(matches("1", code, "1")).toBe(true);
  expect(matches("Enter", "KeyJ", "Enter")).toBe(true);
  expect(matches("Escape", "KeyJ", "Escape")).toBe(true);
  expect(matches("?", "KeyJ", "?", false)).toBe(true);
  expect(matches("ㅏ", "KeyK", "k", false)).toBe(false);
});
it.each([
  { isComposing: true },
  { keyCode: 229 },
  { repeat: true },
  { shiftKey: true },
  { altKey: true },
  { metaKey: true },
])("retains rejection fences for translated physical chords %j", (flags) => {
  const item = definition({ input: ShortcutInput.Allow });
  const event = dispatch([item], { key: "ㅏ", code: "KeyK", ...flags });
  expect(item.run).not.toHaveBeenCalled();
  expect(event.defaultPrevented).toBe(false);
});

it("runs the close-tab chord from terminal input once and consumes it when disabled", () => {
  const input = document.createElement("textarea");
  input.dataset.shortcuts = "passthrough";
  document.body.append(input);
  const close = definition({
    id: ShortcutId.SessionCloseTab,
    input: ShortcutInput.Allow,
    terminal: true,
    bindings: [{ key: "w", primary: true }],
  });
  expect(dispatch([close], { key: "w" }, input).defaultPrevented).toBe(true);
  expect(close.run).toHaveBeenCalledTimes(1);
  dispatch([close], { key: "w", repeat: true }, input);
  dispatch([close], { key: "w", shiftKey: true }, input);
  dispatch([close], { key: "w", altKey: true }, input);
  expect(close.run).toHaveBeenCalledTimes(1);
  const disabled = definition({
    id: ShortcutId.SessionCloseTab,
    input: ShortcutInput.Allow,
    terminal: true,
    enabled: false,
    bindings: [{ key: "w", primary: true }],
  });
  expect(dispatch([disabled], { key: "w" }, input).defaultPrevented).toBe(true);
  expect(disabled.run).not.toHaveBeenCalled();
  input.remove();
});

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "opens Settings once from ordinary inputs with the exact local comma chord on %s",
  (localPlatform) => {
    const input = document.createElement("input");
    document.body.append(input);
    const run = vi.fn();
    const action = definition({
      id: ShortcutId.OpenSettings,
      label: "shortcuts.openSettings",
      bindings: globalShortcutBindings[ShortcutId.OpenSettings],
      input: ShortcutInput.Allow,
      run,
    });
    const primary = {
      metaKey: localPlatform === ShortcutPlatform.Mac,
      ctrlKey: localPlatform === ShortcutPlatform.Other,
    };
    const send = (options: KeyboardEventInit = {}, handled = false) => {
      const event = new KeyboardEvent("keydown", {
        key: ",",
        bubbles: true,
        cancelable: true,
        ...primary,
        ...options,
      });
      if (handled) event.preventDefault();
      input.addEventListener(
        "keydown",
        () =>
          dispatchShortcut(event, [action], Surface.Sessions, localPlatform),
        { once: true },
      );
      input.dispatchEvent(event);
      return event;
    };
    expect(send().defaultPrevented).toBe(true);
    expect(run).toHaveBeenCalledTimes(1);
    for (const options of [
      { ctrlKey: false, metaKey: false },
      { ctrlKey: !primary.ctrlKey, metaKey: !primary.metaKey },
      { shiftKey: true },
      { altKey: true },
      { repeat: true },
      { isComposing: true },
      { keyCode: 229 },
    ])
      send(options);
    send({}, true);
    expect(run).toHaveBeenCalledTimes(1);
    expect(bindingAria(action.bindings[0]!, localPlatform)).toBe(
      localPlatform === ShortcutPlatform.Mac ? "Meta+," : "Control+,",
    );
    input.remove();
  },
);

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "opens terminals once from ordinary input with logical grave on %s and retains dispatch fences",
  (platform) => {
    const input = document.createElement("textarea");
    input.value = "Retained draft";
    document.body.append(input);
    const run = vi.fn();
    const item: ShortcutDefinition = {
      id: ShortcutId.OpenTerminals,
      scope: Surface.Sessions,
      label: "shortcuts.openTerminals",
      bindings: sessionShortcutBindings[ShortcutId.OpenTerminals],
      input: ShortcutInput.Allow,
      run,
    };
    const invoke = (
      options: KeyboardEventInit = {},
      definition = item,
      handled = false,
    ) => {
      const event = new KeyboardEvent("keydown", {
        key: "`",
        code: "Backquote",
        bubbles: true,
        cancelable: true,
        metaKey: platform === ShortcutPlatform.Mac,
        ctrlKey: platform === ShortcutPlatform.Other,
        ...options,
      });
      if (handled) event.preventDefault();
      input.addEventListener(
        "keydown",
        () => dispatchShortcut(event, [definition], Surface.Sessions, platform),
        { once: true },
      );
      input.dispatchEvent(event);
      return event;
    };
    try {
      expect(invoke().defaultPrevented).toBe(true);
      expect(run).toHaveBeenCalledOnce();
      expect(input.value).toBe("Retained draft");
      for (const options of [
        { metaKey: false, ctrlKey: false },
        {
          metaKey: platform !== ShortcutPlatform.Mac,
          ctrlKey: platform === ShortcutPlatform.Mac,
        },
        { shiftKey: true },
        { altKey: true },
        { repeat: true },
        { isComposing: true },
        { keyCode: 229 },
        { key: "~", code: "Backquote" },
      ])
        invoke(options);
      invoke({}, item, true);
      invoke({}, { ...item, active: false });
      expect(invoke({}, { ...item, enabled: false }).defaultPrevented).toBe(
        true,
      );
      input.setAttribute("data-shortcuts", "passthrough");
      invoke();
      input.removeAttribute("data-shortcuts");
      input.hidden = true;
      invoke();
      input.hidden = false;
      input.setAttribute("inert", "");
      invoke();
      input.removeAttribute("inert");
      const modal = document.createElement("dialog");
      modal.open = true;
      document.body.append(modal);
      invoke();
      modal.remove();
      expect(run).toHaveBeenCalledOnce();
      expect(input.value).toBe("Retained draft");
    } finally {
      input.remove();
    }
  },
);

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "admits fixed terminal creation only from its original visible input and consumes disabled matches (%s)",
  (localPlatform) => {
    const host = document.createElement("div"),
      input = document.createElement("textarea"),
      outside = document.createElement("button");
    host.dataset.shortcuts = "passthrough";
    host.append(input);
    document.body.append(host, outside);
    const run = vi.fn(),
      broader = vi.fn();
    const action = definition({
      id: ShortcutId.TerminalCreate,
      scope: Surface.Sessions,
      label: "shortcuts.createTerminal",
      bindings: [{ key: "t", primary: true }],
      target: { current: host },
      input: ShortcutInput.Target,
      terminal: true,
      run,
    });
    const chord =
      localPlatform === ShortcutPlatform.Mac
        ? { metaKey: true }
        : { ctrlKey: true };
    const send = (
      target: HTMLElement,
      options: KeyboardEventInit = {},
      active = action,
    ) => {
      const event = new KeyboardEvent("keydown", {
        key: "t",
        code: "KeyT",
        bubbles: true,
        cancelable: true,
        ...chord,
        ...options,
      });
      target.addEventListener(
        "keydown",
        () =>
          dispatchShortcut(
            event,
            [
              active,
              definition({
                id: ShortcutId.OpenTool,
                scope: Surface.Sessions,
                bindings: [{ key: "t", primary: true }],
                run: broader,
              }),
            ],
            Surface.Sessions,
            localPlatform,
          ),
        { once: true },
      );
      target.dispatchEvent(event);
      return event;
    };
    expect(send(input).defaultPrevented).toBe(true);
    expect(run).toHaveBeenCalledOnce();
    expect(
      send(input, {}, { ...action, enabled: false }).defaultPrevented,
    ).toBe(true);
    expect(run).toHaveBeenCalledOnce();
    expect(broader).not.toHaveBeenCalled();
    for (const options of [
      { shiftKey: true },
      { altKey: true },
      { ctrlKey: true, metaKey: true },
      { repeat: true },
      { isComposing: true },
      { keyCode: 229 },
    ]) {
      send(input, options);
      expect(run).toHaveBeenCalledOnce();
    }
    send(outside);
    expect(run).toHaveBeenCalledOnce();
    for (const attribute of ["hidden", "inert"]) {
      host.setAttribute(attribute, "");
      expect(send(input).defaultPrevented).toBe(false);
      host.removeAttribute(attribute);
    }
    expect(send(input, {}, { ...action, active: false }).defaultPrevented).toBe(
      false,
    );
    const dialog = document.createElement("dialog");
    dialog.open = true;
    document.body.append(dialog);
    expect(send(input).defaultPrevented).toBe(false);
    expect(run).toHaveBeenCalledOnce();
    dialog.remove();
    host.remove();
    outside.remove();
  },
);
