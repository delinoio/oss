// SPDX-License-Identifier: Apache-2.0
import type { FormEvent, KeyboardEvent } from "react";

/**
 * Shared plain-Enter contract for desktop form owners.
 *
 * A form owner supplies the submission path with semantic HTML: one `<form>`
 * with a stable id, a single `type="submit"` primary control (it may be portaled
 * into a task footer and associated with the `form` attribute), and every other
 * control marked `type="button"`. The browser's implicit submission then clicks
 * that primary control once per eligible Enter, and it does nothing while the
 * control is disabled. This module never submits, clicks or advances anything;
 * it only removes the Enter presses that must not reach implicit submission.
 *
 * Owner wiring:
 *
 * ```tsx
 * <form id={id} noValidate onKeyDown={guardEnterSubmission} onSubmit={(event) => {
 *   if (!consumeFormSubmit(event)) return;
 *   primary(); // the same callback the primary button used to call from onClick
 * }}>
 * ```
 *
 * `primary` rechecks the exact disabled/blocked/uncertain conditions of the
 * primary control before allocating a request identity, so a blocked Enter never
 * creates a fresh request id or starts an operation.
 */

/** Why an Enter keydown inside a form owner must not implicitly submit it. */
export enum EnterSubmitBlock {
  /** Not an Enter press, or not an event this form owner is responsible for. */
  NotApplicable = "not-applicable",
  /** Shift, Control, Alt or Meta was held; only plain Enter submits. */
  Modified = "modified",
  /** IME composition is active or just ended (`isComposing` or keyCode 229). */
  Composing = "composing",
  /** The key is being held down and auto-repeating. */
  Repeat = "repeat",
  /** A nested control (for example a completion list) already consumed it. */
  Handled = "handled",
  /** The focused control is owned by this form but is not a single-line text input. */
  IneligibleControl = "ineligible-control",
  /** The focused input controls an open popup that owns Enter for its selection. */
  OpenPopup = "open-popup",
}

/** `<input type>` values whose Enter press is an ordinary implicit submission. */
const SINGLE_LINE_INPUT_TYPES = new Set([
  "",
  "text",
  "search",
  "url",
  "tel",
  "email",
  "password",
  "number",
  // Date and time entry fields submit natively on Enter in browsers. The native
  // picker popup is not part of the page, so it consumes its own Enter.
  "date",
  "datetime-local",
  "month",
  "time",
  "week",
]);
/** Input types whose Enter press activates or opens the control itself. */
const NATIVE_ACTIVATION_INPUT_TYPES = new Set([
  "button",
  "submit",
  "reset",
  "image",
  "file",
]);

export interface EnterKeyEvent {
  key: string;
  keyCode: number;
  repeat: boolean;
  shiftKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  metaKey: boolean;
  target: EventTarget | null;
  currentTarget: EventTarget | null;
  isDefaultPrevented(): boolean;
  nativeEvent: { isComposing?: boolean };
}

/**
 * Classifies an Enter keydown bubbling to a form owner.
 *
 * Returns `undefined` when native implicit submission may proceed (an eligible,
 * unmodified, first-press Enter in a single-line input owned by this form) and
 * `EnterSubmitBlock.NotApplicable` when the event is not this owner's to judge:
 * other keys, controls with their own Enter semantics (buttons, selects,
 * textareas, summaries, file inputs, links) and controls owned by another form
 * such as a nested or portaled dialog.
 */
export function classifyEnterSubmission(
  event: EnterKeyEvent,
): EnterSubmitBlock | undefined {
  const form = event.currentTarget;
  const target = event.target;
  if (
    event.key !== "Enter" ||
    !(form instanceof HTMLFormElement) ||
    !(target instanceof HTMLInputElement) ||
    target.form !== form ||
    NATIVE_ACTIVATION_INPUT_TYPES.has(target.type)
  )
    return EnterSubmitBlock.NotApplicable;
  if (event.isDefaultPrevented()) return EnterSubmitBlock.Handled;
  if (event.nativeEvent.isComposing || event.keyCode === 229)
    return EnterSubmitBlock.Composing;
  if (event.repeat) return EnterSubmitBlock.Repeat;
  if (event.shiftKey || event.ctrlKey || event.altKey || event.metaKey)
    return EnterSubmitBlock.Modified;
  if (target.getAttribute("aria-expanded") === "true")
    return EnterSubmitBlock.OpenPopup;
  if (!SINGLE_LINE_INPUT_TYPES.has(target.type))
    return EnterSubmitBlock.IneligibleControl;
  return undefined;
}

/**
 * `onKeyDown` for a form owner. Cancels the keydown default of every Enter
 * press that must not implicitly submit, which also suppresses the keypress
 * browsers derive implicit submission from. Eligible presses are left alone so
 * the browser clicks the form's enabled primary submit control exactly once.
 */
export function guardEnterSubmission(event: KeyboardEvent<HTMLFormElement>) {
  const block = classifyEnterSubmission(event);
  if (block !== undefined && block !== EnterSubmitBlock.NotApplicable)
    event.preventDefault();
}

/**
 * Claims a `submit` event for the form that owns it. React delivers events from
 * portaled descendants through the component tree, so a submit that originated
 * in another form must not run this owner's callback. Returns `true` only for
 * this form's own submit, after cancelling the browser navigation and stopping
 * it from reaching enclosing forms.
 */
export function consumeFormSubmit(event: FormEvent<HTMLFormElement>): boolean {
  if (event.target !== event.currentTarget) return false;
  event.preventDefault();
  event.stopPropagation();
  return true;
}
