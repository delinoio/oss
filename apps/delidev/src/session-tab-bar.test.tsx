import { captureSharedTabLayout } from "./test-shared-tabs-layout";
// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionTabBar } from "./session-tab-bar";
import { i18n } from "./localization";
import { Comparison } from "./session-diff-model";
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
          {
            kind: SessionTabKind.Page,
            profile: "original",
            id: "page",
            title: "fixture.test",
          },
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

it("keeps New browser page as an ordinary reachable button after the numeric tabs", () => {
  const open = vi.fn();
  render(
    <SessionTabBar
      terminalNumber={() => 1}
      id="plus"
      tabs={[{ kind: SessionTabKind.Conversation }]}
      selected="conversation"
      select={() => {}}
      close={() => {}}
      newPage={open}
    />,
  );
  const plus = screen.getByRole("button", { name: "New browser page" });
  expect(screen.getAllByRole("tab")).toHaveLength(1);
  expect(plus.getAttribute("aria-keyshortcuts")).toBeNull();
  fireEvent.click(plus);
  expect(open).toHaveBeenCalledTimes(1);
});

it("keeps complete names, sibling close targets and direct pages for every workspace kind", async () => {
  const select = vi.fn(),
    close = vi.fn();
  const long = "A long dynamic label / " + "retained-name-".repeat(35);
  render(
    <section className="session-workspace">
      <SessionTabBar
        id="geometry"
        terminalNumber={() => 17}
        tabs={[
          { kind: SessionTabKind.Conversation },
          { kind: SessionTabKind.Files },
          { kind: SessionTabKind.Diff },
          { kind: SessionTabKind.Diagnostics },
          {
            kind: SessionTabKind.File,
            repository: "original-repository",
            path: long,
          },
          {
            kind: SessionTabKind.Comparison,
            repository: "original-repository",
            path: long,
            comparison: Comparison.Staged,
          },
          { kind: SessionTabKind.Terminal, id: "original-terminal" },
          {
            kind: SessionTabKind.Page,
            profile: "original-profile",
            id: "original-page",
            title: long,
            label: "https://fixture.test/" + long,
          },
          {
            kind: SessionTabKind.PendingSidechat,
            requestId: "pending-original",
            name: long,
          },
          { kind: SessionTabKind.Sidechat, id: "original-child", name: long },
        ]}
        selected={JSON.stringify([
          SessionTabKind.Page,
          "original-profile",
          "original-page",
        ])}
        select={select}
        close={close}
        newPage={() => {}}
      />
    </section>,
  );
  const tabs = screen.getAllByRole("tab");
  expect(tabs).toHaveLength(10);
  expect(tabs[0].parentElement!.querySelector(".shared-tab-close")).toBeNull();
  for (const tab of tabs.slice(1)) {
    const sibling = tab.nextElementSibling!;
    expect(sibling.classList.contains("shared-tab-close")).toBe(true);
    expect(sibling.parentElement).toBe(tab.parentElement);
    expect(tab.classList.contains("shared-tab-label--truncate")).toBe(true);
  }
  expect(tabs[4].getAttribute("aria-label")).toContain(
    long + " · original-repository",
  );
  expect(tabs[7].title).toBe("https://fixture.test/" + long);
  expect(tabs[6].title).toBe("Terminal 17");
  fireEvent.keyDown(tabs[7], { key: "ArrowRight", isComposing: true });
  expect(select).not.toHaveBeenCalled();
  fireEvent.click(tabs[4].nextElementSibling!);
  expect(select).not.toHaveBeenCalled();
  expect(close).toHaveBeenCalledTimes(1);
  await captureSharedTabLayout("workspace");
});
