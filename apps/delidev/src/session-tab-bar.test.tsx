// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionTabBar } from "./session-tab-bar";
import { i18n } from "./localization";
import { SessionTabsStore, SessionTabKind } from "./session-tabs";
it("roves focus and selection without an absent-position alias and leaves pinned Conversation noncloseable", () => {
  const close = vi.fn();
  function View() {
    const [selected, select] = useState("conversation");
    return (
      <SessionTabBar
        id="original"
        tabs={[
          { kind: SessionTabKind.Conversation },
          { kind: SessionTabKind.Files },
          { kind: SessionTabKind.Browser },
        ]}
        selected={selected}
        select={select}
        close={close}
        terminalNumber={() => 1}
      />
    );
  }
  render(<View />);
  const tabs = screen.getAllByRole("tab");
  expect(tabs.map((tab) => tab.tabIndex)).toEqual([0, -1, -1]);
  expect(screen.getAllByRole("button")).toHaveLength(2);
  fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
  expect(document.activeElement).toBe(tabs[1]);
  expect(tabs[1].getAttribute("aria-selected")).toBe("true");
  fireEvent.keyDown(tabs[1], { key: "End" });
  expect(document.activeElement).toBe(tabs[2]);
  fireEvent.keyDown(tabs[2], { key: "Home" });
  expect(document.activeElement).toBe(tabs[0]);
  expect(tabs[0].getAttribute("aria-controls")).toBe("session-pane-original");
  fireEvent.click(screen.getAllByRole("button")[0]);
  expect(close).toHaveBeenCalledWith("files");
  expect(tabs[0].getAttribute("aria-keyshortcuts")).toMatch(/1$/);
});

it("uses the shared friendly number for names, close controls and tooltips without changing focus", async () => {
  const store = new SessionTabsStore();
  store.open("session", {
    kind: SessionTabKind.Terminal,
    id: "internal-original-id",
  });
  store.terminalPresentation("session").number("encountered-before-open");
  store.open("session", {
    kind: SessionTabKind.Terminal,
    id: "third-original-id",
  });
  const state = store.snapshot("session");
  const view = render(
    <SessionTabBar
      id="session"
      {...state}
      select={() => {}}
      close={() => {}}
      terminalNumber={(id) => store.terminalPresentation("session").number(id)}
    />,
  );
  const terminal = screen.getByRole("tab", { name: "Terminal 3" });
  terminal.focus();
  expect(terminal.title).toBe("Terminal 3");
  expect(screen.getByRole("button", { name: /Close Terminal 3/ })).toBeTruthy();
  expect(view.container.textContent).not.toContain("original-id");
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  // Session owns locale subscriptions; re-render the same descriptors only.
  view.rerender(
    <SessionTabBar
      id="session"
      {...state}
      select={() => {}}
      close={() => {}}
      terminalNumber={(id) => store.terminalPresentation("session").number(id)}
    />,
  );
  expect(screen.getByRole("tab", { name: "터미널 3" })).toBe(terminal);
  expect(document.activeElement).toBe(terminal);
});
