import { describe, expect, it } from "vitest";

import { ShortcutKey, ShortcutModifier } from "../persistence/contracts";
import { shortcutFromKeyboardInput } from "./SettingsPanel";

describe("structured shortcut capture", () => {
  it("captures only supported keys with structured modifiers", () => {
    expect(
      shortcutFromKeyboardInput({
        code: "KeyK",
        ctrlKey: true,
        altKey: false,
        shiftKey: true,
        metaKey: false,
      }),
    ).toEqual({
      kind: "candidate",
      shortcut: {
        modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift],
        key: ShortcutKey.K,
      },
    });
  });

  it("types cancellation, malformed inputs, and modifier-only presses", () => {
    expect(
      shortcutFromKeyboardInput({
        code: "Escape",
        ctrlKey: false,
        altKey: false,
        shiftKey: false,
        metaKey: false,
      }),
    ).toEqual({ kind: "cancelled" });
    expect(
      shortcutFromKeyboardInput({
        code: "KeyK",
        ctrlKey: false,
        altKey: false,
        shiftKey: false,
        metaKey: false,
      }),
    ).toEqual({ kind: "invalid" });
    expect(
      shortcutFromKeyboardInput({
        code: "ControlLeft",
        ctrlKey: true,
        altKey: false,
        shiftKey: false,
        metaKey: false,
      }),
    ).toEqual({ kind: "ignored" });
  });
});
