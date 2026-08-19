import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { Dialog } from "./Dialog";

HTMLDialogElement.prototype.showModal = vi.fn();

it("focuses deterministically and closes on Escape", () => {
  const close = vi.fn();
  render(
    <Dialog title="Permanent deletion" onClose={close}>
      <textarea data-autofocus aria-label="Reason" />
    </Dialog>,
  );
  expect(document.activeElement).toBe(screen.getByLabelText("Reason"));
  fireEvent(
    screen.getByRole("dialog", { hidden: true }),
    new Event("cancel", { bubbles: true, cancelable: true }),
  );
  expect(close).toHaveBeenCalledOnce();
});
