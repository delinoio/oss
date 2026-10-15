// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import {
  captureShortcut,
  effectiveShortcutDefinitions,
  parseShortcutOverrides,
  readOnlyShortcutSuppression,
  customizationBindings,
  shortcutCatalog,
  editableShortcutCatalog,
  readOnlyShortcutCatalog,
  fixedNativeShortcutCatalog,
  shortcutConflicts,
  ShortcutOverrideState,
  ShortcutTargetContext,
  validShortcutChord,
} from "./shortcut-preferences";
import {
  globalShortcutBindings,
  sessionShortcutBindings,
  ShortcutId,
  ShortcutInput,
  ShortcutPlatform,
  ShortcutScope,
} from "./shortcuts";
import { Surface } from "./surface";
import chordFixtures from "./shortcut-chord-fixtures.json";
const chord = {
  state: ShortcutOverrideState.Binding,
  chord: { key: "j", shift: true },
} as const;
it("replaces only customizable primary chords while preserving creation Enter", () => {
  const original = [
    {
      id: ShortcutId.NewSessionSend,
      scope: Surface.NewSession,
      label: "shortcuts.createSession" as const,
      bindings: [{ key: "Enter" }, { key: "Enter", primary: true }],
    },
  ];
  expect(
    effectiveShortcutDefinitions(original, {
      [ShortcutId.NewSessionSend]: chord,
    })[0]!.bindings,
  ).toEqual([{ key: "Enter" }, { key: "j", shift: true, primary: true }]);
  expect(
    effectiveShortcutDefinitions(original, {
      [ShortcutId.NewSessionSend]: { state: ShortcutOverrideState.Disabled },
    })[0]!.bindings,
  ).toEqual([{ key: "Enter" }]);
  expect(
    effectiveShortcutDefinitions(
      [
        {
          id: ShortcutId.Help,
          scope: ShortcutScope.Global,
          label: "shortcuts.help",
          bindings: [{ key: "?" }],
        },
      ],
      { [ShortcutId.Help]: { state: ShortcutOverrideState.Disabled } },
    )[0]!.bindings,
  ).toEqual([]);
});
it("validates inactive catalog scopes without rejecting exclusive screen reuse", () => {
  expect(
    shortcutConflicts({
      [ShortcutId.Help]: chord,
      [ShortcutId.NewSession]: chord,
    }),
  ).toEqual([[ShortcutId.Help, ShortcutId.NewSession]]);
  expect(
    shortcutConflicts({
      [ShortcutId.SessionFocus]: chord,
      [ShortcutId.SearchFocus]: chord,
    }),
  ).toEqual([]);
  expect(() =>
    parseShortcutOverrides({
      [ShortcutId.Help]: { state: "binding", chord: { key: "n", shift: false } },
    }),
  ).toThrow("Conflicting");
});
it("rejects fixed product and editing reservations and closed-schema violations", () => {
  for (const key of ["k", "w", "1", "9", "c", "v", "Escape", "é", "Tab"])
    expect(validShortcutChord({ key, shift: false })).toBe(false);
  expect(() =>
    parseShortcutOverrides({ unknown: { state: "disabled" } }),
  ).toThrow();
  expect(() =>
    parseShortcutOverrides({ help: { state: "disabled", extra: true } }),
  ).toThrow();
  expect(validShortcutChord({ key: "j", shift: true })).toBe(true);
  expect(validShortcutChord({ key: "v", shift: true })).toBe(false);
  expect(validShortcutChord({ key: "z", shift: true })).toBe(false);
});
it("capture requires the renderer primary and rejects unsafe input without matching actions", () => {
  expect(
    captureShortcut(
      new KeyboardEvent("keydown", { key: "J", metaKey: true, shiftKey: true }),
      ShortcutPlatform.Mac,
    ),
  ).toEqual({ key: "j", shift: true });
  for (const input of [
    { key: "j" },
    { key: "k", ctrlKey: true },
    { key: "j", ctrlKey: true, repeat: true },
    { key: "j", ctrlKey: true, isComposing: true },
    { key: "j", ctrlKey: true, altKey: true },
    { key: "j", ctrlKey: true, metaKey: true },
  ])
    expect(
      captureShortcut(
        new KeyboardEvent("keydown", input),
        ShortcutPlatform.Other,
      ),
    ).toBeUndefined();
});

it("rejects fixed native menu chords throughout the complete editable map", () => {
  for (const action of editableShortcutCatalog)
    for (const key of ["q", "h", "m", "w"]) {
      expect(() =>
        parseShortcutOverrides({
          [action.id]: {
            state: ShortcutOverrideState.Binding,
            chord: { key, shift: false },
          },
        }),
      ).toThrow();
      expect(validShortcutChord({ key, shift: true })).toBe(true);
    }
});

it("keeps native menu bindings in a typed read-only catalog separate from seven editable IDs", () => {
  expect(editableShortcutCatalog).toHaveLength(7);
  expect(fixedNativeShortcutCatalog.map((action) => action.key)).toEqual([
    "w",
    "q",
    "h",
    "m",
  ]);
  for (const native of fixedNativeShortcutCatalog) {
    expect(
      editableShortcutCatalog.some(
        (action) => String(action.id) === String(native.id),
      ),
    ).toBe(false);
    for (const action of editableShortcutCatalog)
      expect(() =>
        parseShortcutOverrides({
          [action.id]: {
            state: ShortcutOverrideState.Binding,
            chord: { key: native.key, shift: false },
          },
        }),
      ).toThrow();
  }
});

it("permits the retired New Window T chord for all seven overrides and capture platforms", () => {
  for (const action of editableShortcutCatalog) {
    const overrides = {
      [action.id]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "t", shift: false },
      },
    };
    expect(parseShortcutOverrides(overrides)).toEqual(overrides);
  }
  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other]) {
    const modifier =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    expect(
      captureShortcut(
        new KeyboardEvent("keydown", { key: "t", ...modifier }),
        platform,
      ),
    ).toEqual({ key: "t", shift: false });
  }
});

it("owns unshifted N as the New session default and not as a native reservation", () => {
  expect(globalShortcutBindings[ShortcutId.NewSession]).toEqual([
    { key: "n", primary: true },
  ]);
  expect(
    editableShortcutCatalog.find((action) => action.id === ShortcutId.NewSession)
      ?.defaults,
  ).toEqual([{ key: "n", primary: true }]);
  expect(
    fixedNativeShortcutCatalog.map((action) => String(action.id)),
  ).not.toContain("native-new-window");
  expect(customizationBindings(ShortcutId.NewSession, {})).toEqual([
    { key: "n", primary: true },
  ]);
  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other]) {
    const modifier =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    expect(
      captureShortcut(
        new KeyboardEvent("keydown", { key: "n", ...modifier }),
        platform,
      ),
    ).toEqual({ key: "n", shift: false });
  }
});

it("keeps saved New session overrides meaningful around the unshifted N default", () => {
  const shifted = {
    [ShortcutId.NewSession]: {
      state: ShortcutOverrideState.Binding,
      chord: { key: "n", shift: true },
    },
  } as const;
  expect(parseShortcutOverrides(shifted)).toEqual(shifted);
  expect(customizationBindings(ShortcutId.NewSession, shifted)).toEqual([
    { key: "n", shift: true, primary: true },
  ]);
  const disabled = {
    [ShortcutId.NewSession]: { state: ShortcutOverrideState.Disabled },
  } as const;
  expect(parseShortcutOverrides(disabled)).toEqual(disabled);
  expect(customizationBindings(ShortcutId.NewSession, disabled)).toEqual([]);
});

// The same fixture table drives the Rust validator, so both runtimes accept
// and reject identical chords and override maps.
it("matches the shared chord and override fixture table", () => {
  for (const { key, shift, valid } of chordFixtures.chords) {
    expect(validShortcutChord({ key, shift }), `${key}/${shift}`).toBe(valid);
    for (const action of editableShortcutCatalog) {
      if (action.id === ShortcutId.Help) continue;
      const overrides = {
        [action.id]: {
          state: ShortcutOverrideState.Binding,
          chord: { key, shift },
        },
      };
      if (valid)
        expect(parseShortcutOverrides(overrides), `${action.id}`).toEqual(
          overrides,
        );
      else expect(() => parseShortcutOverrides(overrides)).toThrow();
    }
  }
  for (const { name, overrides, valid } of chordFixtures.overrides) {
    if (valid) expect(parseShortcutOverrides(overrides), name).toEqual(overrides);
    else expect(() => parseShortcutOverrides(overrides), name).toThrow();
  }
});

it("retains fixed Session Enter with default, custom and disabled primary overrides", () => {
  const definitions = [
    {
      id: ShortcutId.SessionSend,
      scope: Surface.Sessions,
      label: "shortcuts.queueMessage" as const,
      bindings: [{ key: "Enter" }, { key: "Enter", primary: true }],
    },
  ];
  expect(effectiveShortcutDefinitions(definitions, {})[0]!.bindings).toEqual([
    { key: "Enter" },
    { key: "Enter", primary: true },
  ]);
  expect(
    effectiveShortcutDefinitions(definitions, {
      [ShortcutId.SessionSend]: chord,
    })[0]!.bindings,
  ).toEqual([{ key: "Enter" }, { key: "j", shift: true, primary: true }]);
  expect(
    effectiveShortcutDefinitions(definitions, {
      [ShortcutId.SessionSend]: { state: ShortcutOverrideState.Disabled },
    })[0]!.bindings,
  ).toEqual([{ key: "Enter" }]);
  expect(
    editableShortcutCatalog.find(
      (action) => action.id === ShortcutId.SessionSend,
    )?.fixed,
  ).toEqual([{ key: "Enter" }]);
  expect(
    readOnlyShortcutCatalog.find(
      (action) => action.id === ShortcutId.SessionNewline,
    )?.defaults,
  ).toEqual([{ key: "Enter", shift: true }]);
  expect(editableShortcutCatalog).toHaveLength(7);
});

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])(
  "captures physical chords without preference migration on %s",
  (platform) => {
    const modifiers =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    for (const [key, code, canonical, shiftKey] of [
      ["t", "KeyT", "t", false],
      ["ㅅ", "KeyT", "t", false],
      ["k", "KeyJ", "j", false],
      ["!", "Digit1", "1", true],
      ["1", "Numpad1", "1", true],
      ["j", "Unidentified", "j", false],
      ["j", "", "j", false],
      ["Enter", "Enter", "Enter", false],
    ] as const) {
      const captured = captureShortcut(
        new KeyboardEvent("keydown", { key, code, shiftKey, ...modifiers }),
        platform,
      );
      expect(captured).toEqual({ key: canonical, shift: shiftKey });
      const persisted = {
        [ShortcutId.SessionFocus]: {
          state: ShortcutOverrideState.Binding,
          chord: captured,
        },
      };
      expect(parseShortcutOverrides(persisted)).toEqual(persisted);
    }
    for (const flags of [
      { isComposing: true },
      { keyCode: 229 },
      { repeat: true },
      { altKey: true },
      { ctrlKey: true, metaKey: true },
    ]) {
      expect(
        captureShortcut(
          new KeyboardEvent("keydown", {
            key: "ㅅ",
            code: "KeyT",
            ...modifiers,
            ...flags,
          }),
          platform,
        ),
      ).toBeUndefined();
    }
    const altGraph = new KeyboardEvent("keydown", {
      key: "ㅅ",
      code: "KeyT",
      ...modifiers,
    });
    vi.spyOn(altGraph, "getModifierState").mockImplementation(
      (key) => key === "AltGraph",
    );
    expect(captureShortcut(altGraph, platform)).toBeUndefined();
    expect(
      captureShortcut(
        new KeyboardEvent("keydown", { key: "ㅏ", code: "KeyK", ...modifiers }),
        platform,
      ),
    ).toBeUndefined();
  },
);

it("keeps Open Settings fixed and rejects comma capture without changing editable IDs", () => {
  const action = readOnlyShortcutCatalog.find(
    (entry) => entry.id === ShortcutId.OpenSettings,
  )!;
  expect(action.defaults).toBe(globalShortcutBindings[ShortcutId.OpenSettings]);
  expect(action.input).toBe(ShortcutInput.Allow);
  expect(action.scopes).toEqual([ShortcutScope.Global]);
  expect(editableShortcutCatalog.map((entry) => entry.id)).toEqual([
    ShortcutId.Help,
    ShortcutId.NewSession,
    ShortcutId.SessionFocus,
    ShortcutId.SessionSend,
    ShortcutId.NewSessionFocus,
    ShortcutId.NewSessionSend,
    ShortcutId.SearchFocus,
  ]);
  expect(() =>
    parseShortcutOverrides({
      [ShortcutId.OpenSettings]: { state: "disabled" },
    }),
  ).toThrow();
  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other])
    expect(
      captureShortcut(
        new KeyboardEvent("keydown", {
          key: ",",
          metaKey: platform === ShortcutPlatform.Mac,
          ctrlKey: platform === ShortcutPlatform.Other,
        }),
        platform,
      ),
    ).toBeUndefined();
});

it("keeps Open terminals fixed in the read-only Session catalog without expanding editable preferences", () => {
  const action = readOnlyShortcutCatalog.find(
    (item) => item.id === ShortcutId.OpenTerminals,
  );
  expect(action).toMatchObject({
    label: "shortcuts.openTerminals",
    scopes: [Surface.Sessions],
    input: ShortcutInput.Allow,
  });
  expect(action?.defaults).toBe(
    sessionShortcutBindings[ShortcutId.OpenTerminals],
  );
  expect(editableShortcutCatalog).toHaveLength(7);
  expect(() =>
    parseShortcutOverrides({
      [ShortcutId.OpenTerminals]: { state: ShortcutOverrideState.Disabled },
    }),
  ).toThrow();
  expect(validShortcutChord({ key: "`", shift: false })).toBe(false);
});

const unshiftedT = {
  state: ShortcutOverrideState.Binding,
  chord: { key: "t", shift: false },
} as const;
const toolDefinition = {
  id: ShortcutId.OpenTool,
  scope: Surface.Sessions,
  label: "session.openTool" as const,
  bindings: sessionShortcutBindings[ShortcutId.OpenTool],
  enabled: true,
};
it("keeps OpenTool read-only and outside the seven editable action IDs", () => {
  expect(editableShortcutCatalog).toHaveLength(7);
  expect(
    editableShortcutCatalog.some((action) => action.id === ShortcutId.OpenTool),
  ).toBe(false);
  expect(
    readOnlyShortcutCatalog.find((action) => action.id === ShortcutId.OpenTool)
      ?.defaults,
  ).toEqual([{ key: "t", primary: true }]);
  expect(() =>
    parseShortcutOverrides({ [ShortcutId.OpenTool]: unshiftedT }),
  ).toThrow("Invalid shortcut action");
});
it.each(editableShortcutCatalog.map((action) => action.id))(
  "round-trips an unshifted primary+T override for %s and suppresses only the default OpenTool",
  (id) => {
    const overrides = { [id]: unshiftedT };
    const before = JSON.stringify(overrides);
    // The existing preference stays valid and unchanged byte for byte.
    expect(parseShortcutOverrides(overrides)).toEqual(overrides);
    expect(JSON.stringify(overrides)).toBe(before);
    expect(shortcutConflicts(overrides)).toEqual([]);
    expect(readOnlyShortcutSuppression(ShortcutId.OpenTool, overrides)).toBe(
      "shortcuts.toolSuppressed",
    );
    expect(customizationBindings(ShortcutId.OpenTool, overrides)).toEqual([]);
    const [effective] = effectiveShortcutDefinitions(
      [toolDefinition],
      overrides,
    );
    expect(effective).toMatchObject({
      bindings: [],
      enabled: false,
      unavailableReason: "shortcuts.toolSuppressed",
    });
    // Other read-only defaults and the closed catalog are unaffected.
    expect(
      readOnlyShortcutSuppression(ShortcutId.OpenTerminals, overrides),
    ).toBeUndefined();
    expect(shortcutCatalog.length).toBe(
      editableShortcutCatalog.length + readOnlyShortcutCatalog.length,
    );
  },
);
it("restores OpenTool immediately and ignores disabled, Shift+T and other-key overrides", () => {
  for (const overrides of [
    {},
    { [ShortcutId.SessionFocus]: { state: ShortcutOverrideState.Disabled } },
    {
      [ShortcutId.SessionFocus]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "t", shift: true },
      },
    },
    { [ShortcutId.SessionFocus]: chord },
    {
      [ShortcutId.SessionFocus]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "T", shift: false },
      },
    },
  ] as const) {
    expect(
      readOnlyShortcutSuppression(
        ShortcutId.OpenTool,
        overrides as Parameters<typeof readOnlyShortcutSuppression>[1],
      ),
    ).toBeUndefined();
  }
  const suppressed = { [ShortcutId.Help]: unshiftedT };
  expect(
    effectiveShortcutDefinitions([toolDefinition], suppressed)[0],
  ).toMatchObject({
    bindings: [],
  });
  expect(effectiveShortcutDefinitions([toolDefinition], {})[0]).toEqual(
    toolDefinition,
  );
});

const unshiftedE = {
  state: ShortcutOverrideState.Binding,
  chord: { key: "e", shift: false },
} as const;
const filesDefinition = {
  id: ShortcutId.OpenFiles,
  scope: Surface.Sessions,
  label: "shortcuts.openFiles" as const,
  bindings: sessionShortcutBindings[ShortcutId.OpenFiles],
  enabled: true,
};
it("keeps OpenFiles read-only and outside the seven editable action IDs", () => {
  expect(editableShortcutCatalog).toHaveLength(7);
  expect(
    editableShortcutCatalog.some(
      (action) => action.id === ShortcutId.OpenFiles,
    ),
  ).toBe(false);
  expect(
    readOnlyShortcutCatalog.find((action) => action.id === ShortcutId.OpenFiles)
      ?.defaults,
  ).toEqual([{ key: "e", primary: true }]);
  expect(() =>
    parseShortcutOverrides({ [ShortcutId.OpenFiles]: unshiftedE }),
  ).toThrow("Invalid shortcut action");
});
it.each(editableShortcutCatalog.map((action) => action.id))(
  "round-trips an unshifted primary+E override for %s and suppresses only the default OpenFiles",
  (id) => {
    const overrides = { [id]: unshiftedE };
    const before = JSON.stringify(overrides);
    // The existing preference stays valid and unchanged byte for byte.
    expect(parseShortcutOverrides(overrides)).toEqual(overrides);
    expect(JSON.stringify(overrides)).toBe(before);
    expect(shortcutConflicts(overrides)).toEqual([]);
    expect(readOnlyShortcutSuppression(ShortcutId.OpenFiles, overrides)).toBe(
      "shortcuts.filesSuppressed",
    );
    expect(customizationBindings(ShortcutId.OpenFiles, overrides)).toEqual([]);
    expect(
      effectiveShortcutDefinitions([filesDefinition], overrides)[0],
    ).toMatchObject({
      bindings: [],
      enabled: false,
      unavailableReason: "shortcuts.filesSuppressed",
    });
    // The T policy stays independent of an E override.
    expect(
      readOnlyShortcutSuppression(ShortcutId.OpenTool, overrides),
    ).toBeUndefined();
    expect(
      readOnlyShortcutSuppression(ShortcutId.OpenTerminals, overrides),
    ).toBeUndefined();
  },
);
it("restores OpenFiles immediately and ignores disabled, Shift+E and other-key overrides", () => {
  for (const overrides of [
    {},
    { [ShortcutId.SessionFocus]: { state: ShortcutOverrideState.Disabled } },
    {
      [ShortcutId.SessionFocus]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "e", shift: true },
      },
    },
    { [ShortcutId.SessionFocus]: chord },
    { [ShortcutId.SessionFocus]: unshiftedT },
  ] as const) {
    expect(
      readOnlyShortcutSuppression(
        ShortcutId.OpenFiles,
        overrides as Parameters<typeof readOnlyShortcutSuppression>[1],
      ),
    ).toBeUndefined();
  }
  expect(
    effectiveShortcutDefinitions([filesDefinition], {
      [ShortcutId.Help]: unshiftedE,
    })[0],
  ).toMatchObject({ bindings: [] });
  expect(effectiveShortcutDefinitions([filesDefinition], {})[0]).toEqual(
    filesDefinition,
  );
});

it("registers TerminalCreate as a read-only terminal-target Session action that no override suppresses", () => {
  const action = readOnlyShortcutCatalog.find(
    (item) => item.id === ShortcutId.TerminalCreate,
  );
  expect(action).toMatchObject({
    target: ShortcutTargetContext.Terminal,
    scopes: [Surface.Sessions],
    priority: 2,
    input: ShortcutInput.Target,
  });
  expect(action?.defaults).toBe(
    sessionShortcutBindings[ShortcutId.TerminalCreate],
  );
  expect(
    editableShortcutCatalog.some(
      (item) => item.id === ShortcutId.TerminalCreate,
    ),
  ).toBe(false);
  expect(() =>
    parseShortcutOverrides({
      [ShortcutId.TerminalCreate]: { state: ShortcutOverrideState.Disabled },
    }),
  ).toThrow("Invalid shortcut action");
  // Every existing custom T override stays valid, unchanged and conflict free,
  // and none of them participates in suppressing the terminal-bound default.
  for (const { id } of editableShortcutCatalog) {
    const overrides = { [id]: unshiftedT };
    expect(parseShortcutOverrides(overrides)).toEqual(overrides);
    expect(shortcutConflicts(overrides)).toEqual([]);
    expect(
      readOnlyShortcutSuppression(ShortcutId.TerminalCreate, overrides),
    ).toBeUndefined();
    expect(customizationBindings(ShortcutId.TerminalCreate, overrides)).toEqual(
      [{ key: "t", primary: true }],
    );
  }
});

it("holds every policy-listed read-only default with a pending reason until the initial read is ready", () => {
  for (const id of [ShortcutId.OpenFiles, ShortcutId.OpenTool]) {
    expect(readOnlyShortcutSuppression(id, {}, false)).toBe(
      "shortcuts.preferencesPending",
    );
    expect(customizationBindings(id, {}, false)).toEqual([]);
  }
  expect(
    effectiveShortcutDefinitions([filesDefinition], {}, false)[0],
  ).toMatchObject({
    bindings: [],
    enabled: false,
    unavailableReason: "shortcuts.preferencesPending",
  });
  // Ready (the default) keeps the established policy and untouched defaults.
  expect(readOnlyShortcutSuppression(ShortcutId.OpenFiles, {}, true)).toBe(
    undefined,
  );
  expect(effectiveShortcutDefinitions([filesDefinition], {}, true)[0]).toEqual(
    filesDefinition,
  );
  // Editable defaults and other read-only IDs are never gated.
  expect(
    readOnlyShortcutSuppression(ShortcutId.OpenTerminals, {}, false),
  ).toBeUndefined();
  expect(customizationBindings(ShortcutId.NewSession, {}, false)).toEqual(
    customizationBindings(ShortcutId.NewSession, {}, true),
  );
});

it("holds the read-only Reload page default back for an R override, a pending read or an unverified map", () => {
  const override = {
    [ShortcutId.SessionFocus]: {
      state: ShortcutOverrideState.Binding,
      chord: { key: "r", shift: false },
    },
  } as const;
  expect(
    readOnlyShortcutSuppression(ShortcutId.ReloadPage, {}),
  ).toBeUndefined();
  expect(readOnlyShortcutSuppression(ShortcutId.ReloadPage, override)).toBe(
    "shortcuts.reloadSuppressed",
  );
  expect(readOnlyShortcutSuppression(ShortcutId.ReloadPage, {}, false)).toBe(
    "shortcuts.preferencesPending",
  );
  expect(
    readOnlyShortcutSuppression(ShortcutId.ReloadPage, {}, true, false),
  ).toBe("shortcuts.reloadUnverified");
  // Other read-only defaults keep their original semantics when unverified.
  expect(
    readOnlyShortcutSuppression(ShortcutId.OpenTool, {}, true, false),
  ).toBeUndefined();
  // Shift variants and disabled overrides never claim the unshifted chord.
  expect(
    readOnlyShortcutSuppression(ShortcutId.ReloadPage, {
      [ShortcutId.SessionFocus]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "r", shift: true },
      },
      [ShortcutId.NewSession]: { state: ShortcutOverrideState.Disabled },
    }),
  ).toBeUndefined();
  expect(customizationBindings(ShortcutId.ReloadPage, {})).toEqual(
    globalShortcutBindings[ShortcutId.ReloadPage],
  );
  expect(customizationBindings(ShortcutId.ReloadPage, override)).toEqual([]);
  expect(customizationBindings(ShortcutId.ReloadPage, {}, true, false)).toEqual(
    [],
  );
  // The saved document accepts an R override and the reload default never conflicts with it.
  expect(parseShortcutOverrides(override)).toEqual(override);
  expect(shortcutConflicts(override)).toEqual([]);
  expect(
    editableShortcutCatalog.some(
      (action) => action.id === ShortcutId.ReloadPage,
    ),
  ).toBe(false);
  expect(
    readOnlyShortcutCatalog.some(
      (action) => action.id === ShortcutId.ReloadPage,
    ),
  ).toBe(true);
});
