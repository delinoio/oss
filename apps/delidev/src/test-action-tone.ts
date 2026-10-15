// SPDX-License-Identifier: Apache-2.0
import { expect } from "vitest";
import { SettingsActionTone } from "./settings-action";

/** Asserts the semantic tone marker without reading icons or visible text. */
export function expectTone(
  button: HTMLElement,
  tone: SettingsActionTone | null,
) {
  expect(button.tagName).toBe("BUTTON");
  expect(button.getAttribute("data-action-tone")).toBe(tone);
}
export const danger = SettingsActionTone.Danger;
export const neutral = SettingsActionTone.Default;
