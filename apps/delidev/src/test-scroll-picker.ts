// SPDX-License-Identifier: Apache-2.0
import { fireEvent, waitFor } from "@testing-library/react";

/** Select an exact domain identity through the accessible popup. */
export async function chooseScrollOption(control: HTMLElement, id: string) {
  fireEvent.click(control);
  await waitFor(() => {
  const popup = document.getElementById(control.getAttribute("aria-controls") ?? "");
  const option = [...popup?.querySelectorAll<HTMLElement>("[data-picker-id]") ?? []].find(node => node.dataset.pickerId === id);
  if (!option) throw new Error("The exact picker identity is not loaded.");
  fireEvent.click(option);
  });
  await waitFor(() => { if (control.dataset.value !== id) throw new Error("The selected identity has not been accepted."); });
}
export async function waitScrollChoices(control: HTMLElement) {
  fireEvent.click(control);
  await waitFor(() => { const popup = document.getElementById(control.getAttribute("aria-controls") ?? ""); if ((popup?.querySelectorAll("[data-picker-id]").length ?? 0) < 2) throw new Error("Choices have not loaded."); });
  fireEvent.keyDown(control, { key: "Escape" });
}
export function scrollChoiceValue(control: HTMLElement) { return control.dataset.value; }
