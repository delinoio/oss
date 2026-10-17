// SPDX-License-Identifier: Apache-2.0
import { fireEvent } from "@testing-library/react";

export interface EnterPress {
  /** Dispatch IME composition state on the keydown (`isComposing`). */
  composing?: boolean;
  /** Dispatch the legacy IME placeholder key code 229. */
  keyCode?: number;
  repeat?: boolean;
  shiftKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
  metaKey?: boolean;
}

/**
 * Presses Enter on a focused control the way a browser does. jsdom dispatches
 * the keydown but has no implicit form submission, so this follows the HTML
 * rule for a keydown that nothing cancelled: the owning form's default button
 * (its first submit control in tree order, including `form=` associated
 * controls outside the element) receives a click unless it is disabled. Forms
 * without a submit control do not submit, which is stricter than a browser's
 * single-field fallback and therefore never hides a missing primary control.
 * Returns the number of times the default button was clicked (0 or 1).
 */
export function pressEnter(control: HTMLElement, press: EnterPress = {}) {
  control.focus();
  const proceeded = fireEvent.keyDown(control, {
    key: "Enter",
    code: "Enter",
    keyCode: press.keyCode ?? 13,
    isComposing: press.composing ?? false,
    repeat: press.repeat ?? false,
    shiftKey: press.shiftKey ?? false,
    ctrlKey: press.ctrlKey ?? false,
    altKey: press.altKey ?? false,
    metaKey: press.metaKey ?? false,
  });
  const form = (control as HTMLInputElement).form;
  if (
    !proceeded ||
    !(control instanceof HTMLInputElement) ||
    !form ||
    ["button", "submit", "reset", "image", "file"].includes(control.type)
  )
    return 0;
  const submit = [...form.elements].find(
    (element): element is HTMLButtonElement | HTMLInputElement =>
      (element instanceof HTMLButtonElement ||
        element instanceof HTMLInputElement) &&
      element.type === "submit",
  );
  if (!submit || submit.disabled) return 0;
  fireEvent.click(submit);
  return 1;
}
