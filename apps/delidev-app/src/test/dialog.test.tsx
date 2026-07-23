import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { Dialog } from "../components/Dialog";

function DialogHarness({ onClose }: { onClose: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Review deletion
      </button>
      {open ? (
        <Dialog
          descriptionId="dialog-description"
          onClose={() => {
            setOpen(false);
            onClose();
          }}
          titleId="dialog-title"
        >
          <h2 id="dialog-title">Delete account?</h2>
          <p id="dialog-description">This action is permanent.</p>
          <button type="button">Keep account</button>
        </Dialog>
      ) : null}
    </>
  );
}

describe("dialog", () => {
  it("focuses its first action, closes on Escape, and restores focus", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<DialogHarness onClose={onClose} />);
    const trigger = screen.getByRole("button", { name: "Review deletion" });
    await user.click(trigger);
    expect(screen.getByRole("button", { name: "Keep account" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledOnce();
    expect(trigger).toHaveFocus();
  });
});
