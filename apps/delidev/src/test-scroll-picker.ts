// SPDX-License-Identifier: Apache-2.0
import { fireEvent, waitFor } from "@testing-library/react";

/** The inline list is its own always-visible choice container. */
function inline(control: HTMLElement) {
  return control.getAttribute("role") === "listbox";
}
function unavailable(control: HTMLElement) {
  return inline(control)
    ? control.getAttribute("aria-disabled") === "true"
    : (control as HTMLButtonElement).disabled;
}

/** Select an exact domain identity through the accessible popup or inline list. */
export async function chooseScrollOption(control: HTMLElement, id: string) {
  await waitFor(() => {
    if (unavailable(control)) throw new Error("Picker is disabled.");
  });
  if (!inline(control)) fireEvent.click(control);
  await waitFor(() => {
    const popup = inline(control)
      ? control
      : document.getElementById(control.getAttribute("aria-controls") ?? "");
    const option = [
      ...(popup?.querySelectorAll<HTMLElement>("[data-picker-id]") ?? []),
    ].find((node) => node.dataset.pickerId === id);
    if (!option) throw new Error("The exact picker identity is not loaded.");
    fireEvent.click(option);
  });
  await waitFor(() => {
    if (control.dataset.value !== id)
      throw new Error("The selected identity has not been accepted.");
  });
}
export async function waitScrollChoices(control: HTMLElement) {
  await waitFor(() => {
    if (unavailable(control)) throw new Error("Picker is disabled.");
  });
  if (!inline(control)) fireEvent.click(control);
  await waitFor(() => {
    const popup = inline(control)
      ? control
      : document.getElementById(control.getAttribute("aria-controls") ?? "");
    if ((popup?.querySelectorAll("[data-picker-id]").length ?? 0) < 2)
      throw new Error("Choices have not loaded.");
  });
  if (!inline(control)) fireEvent.keyDown(control, { key: "Escape" });
}
export function scrollChoiceValue(control: HTMLElement) {
  return control.dataset.value;
}
