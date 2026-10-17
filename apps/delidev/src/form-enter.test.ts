// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup } from "@testing-library/react";
import {
  EnterSubmitBlock,
  classifyEnterSubmission,
  consumeFormSubmit,
  guardEnterSubmission,
  type EnterKeyEvent,
} from "./form-enter";

afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

function fixture(control = '<input id="field" type="text">') {
  document.body.innerHTML = `<form id="owner">${control}</form>
    <form id="other"><input id="foreign" type="text"></form>`;
  return {
    form: document.getElementById("owner") as HTMLFormElement,
    other: document.getElementById("other") as HTMLFormElement,
    field: document.getElementById("field") as HTMLElement,
    foreign: document.getElementById("foreign") as HTMLInputElement,
  };
}
function key(
  target: EventTarget,
  form: EventTarget,
  init: Partial<EnterKeyEvent> & {
    composing?: boolean;
    handled?: boolean;
  } = {},
) {
  const prevent = vi.fn();
  const event = {
    key: "Enter",
    keyCode: 13,
    repeat: false,
    shiftKey: false,
    ctrlKey: false,
    altKey: false,
    metaKey: false,
    target,
    currentTarget: form,
    isDefaultPrevented: () => init.handled ?? false,
    nativeEvent: { isComposing: init.composing ?? false },
    preventDefault: prevent,
    ...init,
  };
  return { event: event as typeof event & EnterKeyEvent, prevent };
}

it("lets an unmodified first Enter in a single-line text input submit natively", () => {
  for (const type of [
    "",
    "text",
    "search",
    "url",
    "tel",
    "email",
    "password",
    "number",
  ]) {
    const { form, field } = fixture(
      `<input id="field" ${type ? `type="${type}"` : ""}>`,
    );
    const { event, prevent } = key(field, form);
    expect(classifyEnterSubmission(event), type).toBeUndefined();
    guardEnterSubmission(event as never);
    expect(prevent, type).not.toHaveBeenCalled();
  }
});

it.each([
  ["composition confirmation", { composing: true }, EnterSubmitBlock.Composing],
  ["the legacy IME key code", { keyCode: 229 }, EnterSubmitBlock.Composing],
  ["a held key", { repeat: true }, EnterSubmitBlock.Repeat],
  ["Shift", { shiftKey: true }, EnterSubmitBlock.Modified],
  ["Control", { ctrlKey: true }, EnterSubmitBlock.Modified],
  ["Alt", { altKey: true }, EnterSubmitBlock.Modified],
  ["Meta", { metaKey: true }, EnterSubmitBlock.Modified],
  ["an already handled event", { handled: true }, EnterSubmitBlock.Handled],
])("blocks implicit submission for %s", (_name, init, expected) => {
  const { form, field } = fixture();
  const { event, prevent } = key(field, form, init);
  expect(classifyEnterSubmission(event)).toBe(expected);
  guardEnterSubmission(event as never);
  expect(prevent).toHaveBeenCalledTimes(1);
});

it("blocks Enter in an input that controls an open popup", () => {
  const { form, field } = fixture(
    '<input id="field" role="combobox" aria-expanded="true">',
  );
  const { event, prevent } = key(field, form);
  expect(classifyEnterSubmission(event)).toBe(EnterSubmitBlock.OpenPopup);
  guardEnterSubmission(event as never);
  expect(prevent).toHaveBeenCalledTimes(1);
});

it("blocks Enter on checkboxes, radios and other non-text inputs owned by the form", () => {
  for (const type of ["checkbox", "radio", "range", "color", "date", "time"]) {
    const { form, field } = fixture(`<input id="field" type="${type}">`);
    const { event, prevent } = key(field, form);
    expect(classifyEnterSubmission(event), type).toBe(
      EnterSubmitBlock.IneligibleControl,
    );
    guardEnterSubmission(event as never);
    expect(prevent, type).toHaveBeenCalledTimes(1);
  }
});

it("leaves controls with their own Enter semantics untouched", () => {
  for (const markup of [
    '<button id="field" type="button">Go</button>',
    '<button id="field" type="submit">Go</button>',
    '<input id="field" type="button">',
    '<input id="field" type="file">',
    '<select id="field"><option>a</option></select>',
    '<textarea id="field"></textarea>',
    '<details><summary id="field">More</summary></details>',
    '<a id="field" href="#x">link</a>',
  ]) {
    const { form, field } = fixture(markup);
    const { event, prevent } = key(field, form);
    expect(classifyEnterSubmission(event), markup).toBe(
      EnterSubmitBlock.NotApplicable,
    );
    guardEnterSubmission(event as never);
    expect(prevent, markup).not.toHaveBeenCalled();
  }
});

it("ignores other keys and inputs owned by another form", () => {
  const { form, field, foreign } = fixture();
  const space = key(field, form, { key: " " });
  expect(classifyEnterSubmission(space.event)).toBe(
    EnterSubmitBlock.NotApplicable,
  );
  // A portaled child's keydown reaches this owner through React, not the DOM.
  const portaled = key(foreign, form, { repeat: true });
  expect(classifyEnterSubmission(portaled.event)).toBe(
    EnterSubmitBlock.NotApplicable,
  );
  guardEnterSubmission(portaled.event as never);
  expect(portaled.prevent).not.toHaveBeenCalled();
});

it("claims only the form's own submit events", () => {
  const { form, field } = fixture();
  const own = {
    target: form,
    currentTarget: form,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
  };
  expect(consumeFormSubmit(own as never)).toBe(true);
  expect(own.preventDefault).toHaveBeenCalledTimes(1);
  expect(own.stopPropagation).toHaveBeenCalledTimes(1);
  const nested = {
    target: field,
    currentTarget: form,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
  };
  expect(consumeFormSubmit(nested as never)).toBe(false);
  expect(nested.preventDefault).not.toHaveBeenCalled();
  expect(nested.stopPropagation).not.toHaveBeenCalled();
});
