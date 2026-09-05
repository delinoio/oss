// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef, useState } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { Button, Dialog, Sheet, ShellLayout, resolveShellLayout } from "./ui-foundation";

afterEach(cleanup);

describe("DevHud UI foundation", () => {
  it.each([
    [1440, ShellLayout.Sidebar],
    [1024, ShellLayout.Sidebar],
    [1023, ShellLayout.Rail],
    [701, ShellLayout.Rail],
    [700, ShellLayout.Mobile],
    [390, ShellLayout.Mobile],
    [320, ShellLayout.Mobile],
  ])("resolves %ipx to the contracted layout", (width, layout) => {
    expect(resolveShellLayout(width)).toBe(layout);
  });

  it("contains dialog focus, closes with Escape, and restores the opener", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      const opener = createRef<HTMLButtonElement>();
      const first = createRef<HTMLButtonElement>();
      return <><Button ref={opener} onClick={() => setOpen(true)}>Open</Button><Dialog open={open} title="Commands" initialFocusRef={first} returnFocusRef={opener} onClose={() => setOpen(false)}><Button ref={first}>First</Button><Button>Last</Button></Dialog></>;
    }
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open" });
    opener.focus();
    fireEvent.click(opener);
    const dialog = screen.getByRole("dialog", { name: "Commands" });
    const first = screen.getByRole("button", { name: "First" });
    const last = screen.getByRole("button", { name: "Last" });
    await waitFor(() => expect(document.activeElement).toBe(first));
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Commands" })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it("closes a sheet through its named back control", async () => {
    function Harness() {
      const [open, setOpen] = useState(true);
      return <Sheet open={open} title="More" backLabel="Back" onClose={() => setOpen(false)}><Button>Destination</Button></Sheet>;
    }
    render(<Harness />);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Destination" })));
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  });
});
