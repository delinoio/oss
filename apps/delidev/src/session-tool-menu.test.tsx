// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
it("opens at the first enabled entry and owns arrows, Escape and outside dismissal without activating tools", () => {
  const selected = vi.fn();
  const view = render(<><SessionToolMenu active><button role="menuitem" disabled>Terminals</button><button role="menuitem" onClick={selected}>Files</button><button role="menuitem">Diagnostics</button></SessionToolMenu><button>Destination</button></>);
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  const entries = screen.getAllByRole("menuitem");
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "ArrowDown" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Home" }); expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "End" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Escape" }); expect(document.activeElement).toBe(trigger); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(selected).not.toHaveBeenCalled();
  fireEvent.click(trigger); fireEvent.click(screen.getByRole("menuitem", { name: "Files" })); expect(selected).toHaveBeenCalledOnce(); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger); const destination = screen.getByRole("button", { name: "Destination" }); destination.focus(); fireEvent.pointerDown(destination); expect(document.activeElement).toBe(destination); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger); view.rerender(<SessionToolMenu active={false}><button role="menuitem">Files</button></SessionToolMenu>); expect(screen.queryByRole("menuitem")).toBeNull();
});


it("hands a visible opener to dialog entries while preserving ordinary tool destination focus", () => {
  const opened = vi.fn();
  render(<><SessionToolMenu active>{openDialog => <><button role="menuitem" onClick={() => screen.getByRole("button", { name: "Tool destination" }).focus()}>Files</button><button role="menuitem" disabled>Unavailable</button><button role="menuitem" onClick={() => { openDialog(); opened(document.activeElement); }}>Open Sidechat</button></>}</SessionToolMenu><button>Tool destination</button></>);
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("menuitem", { name: "Files" }), { key: "End" });
  const sidechat = screen.getByRole("menuitem", { name: "Open Sidechat" });
  expect(document.activeElement).toBe(sidechat);
  fireEvent.click(sidechat);
  expect(opened).toHaveBeenCalledTimes(1); expect(opened).toHaveBeenCalledWith(trigger);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("menuitem", { name: "Files" }));
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Tool destination" }));
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("menuitem", { name: "Files" }), { key: "Tab" });
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(opened).toHaveBeenCalledTimes(1);
});
