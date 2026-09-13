// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppShell, Button, Dialog, Sheet, ShellLayout, StatePanel, resolveShellLayout } from "./ui-foundation";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

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

  it("renders StatePanel titles at the requested nested heading level", () => {
    render(<StatePanel eyebrow="Empty" title="No pull requests" summary="Nothing matched" headingLevel={4} />);

    expect(screen.getByRole("heading", { name: "No pull requests", level: 4 }).className).toContain("state-panel-title");
  });

  it("tracks the rendered bottom-navigation height for fixed UI clearance", async () => {
    let height = 88;
    let resize: ResizeObserverCallback | undefined;
    const disconnect = vi.fn();
    class TestResizeObserver {
      constructor(callback: ResizeObserverCallback) { resize = callback; }
      observe() {}
      unobserve() {}
      disconnect() { disconnect(); }
    }
    vi.stubGlobal("ResizeObserver", TestResizeObserver);
    const originalRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function measuredRect(this: HTMLElement) {
      if (this.classList.contains("app-shell-bottom-bar")) {
        return { x: 0, y: 0, width: 390, height, top: 0, right: 390, bottom: height, left: 0, toJSON: () => ({}) };
      }
      return originalRect.call(this);
    });

    const { unmount } = render(<AppShell layout={ShellLayout.Mobile} skipLabel="Skip" bottomBar={<nav aria-label="Primary">Navigation</nav>}>Content</AppShell>);
    const shell = document.querySelector<HTMLElement>(".app-shell");
    await waitFor(() => expect(shell?.style.getPropertyValue("--mobile-bottom-navigation-height")).toBe("88px"));

    height = 124;
    act(() => resize?.([], {} as ResizeObserver));
    await waitFor(() => expect(shell?.style.getPropertyValue("--mobile-bottom-navigation-height")).toBe("124px"));

    unmount();
    expect(disconnect).toHaveBeenCalledOnce();
    expect(shell?.style.getPropertyValue("--mobile-bottom-navigation-height")).toBe("");
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
      const [open, setOpen] = useState(false);
      const opener = createRef<HTMLButtonElement>();
      const returnFocusRef = createRef<HTMLButtonElement>();
      return <><Button ref={opener} onClick={() => setOpen(true)}>Open</Button><Sheet open={open} title="More" backLabel="Back" returnFocusRef={returnFocusRef} onClose={() => setOpen(false)}><Button>Destination</Button></Sheet></>;
    }
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open" });
    opener.focus();
    fireEvent.click(opener);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Destination" })));
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it("includes disclosure summaries in sheet focus containment", async () => {
    render(<Sheet open title="More" backLabel="Back" onClose={() => {}}><details><summary>Inspect</summary><p>Details</p></details><Button>Later</Button></Sheet>);
    const summary = screen.getByText("Inspect");
    const back = screen.getByRole("button", { name: "Back" });

    await waitFor(() => expect(document.activeElement).toBe(summary));
    fireEvent.keyDown(summary, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(back);
    fireEvent.keyDown(back, { key: "Tab" });
    expect(document.activeElement).toBe(summary);
  });

  it("does not override focus transferred within the sheet while autofocus is pending", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><Button onClick={() => setOpen(true)}>Open</Button><Sheet open={open} title="More" backLabel="Back" onClose={() => setOpen(false)}><Button>Destination</Button><Button>Keep focus</Button></Sheet></>;
    }
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open" });
    opener.focus();
    fireEvent.click(opener);
    const retainedFocus = screen.getByRole("button", { name: "Keep focus" });
    retainedFocus.focus();

    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    expect(document.activeElement).toBe(retainedFocus);
  });

  it("skips fieldset-disabled controls when focusing a sheet", async () => {
    render(<Sheet open title="Read-only" backLabel="Back" onClose={() => undefined}><fieldset disabled><input aria-label="Disabled input" /></fieldset></Sheet>);

    expect(screen.getByLabelText("Disabled input").matches(":disabled")).toBe(true);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Back" })));
  });
});
