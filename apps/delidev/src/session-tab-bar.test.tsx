// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionTabBar } from "./session-tab-bar";
import { SessionTabKind, SessionTabsProvider } from "./session-tabs";
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
          { kind: SessionTabKind.Diagnostics },
        ]}
        selected={selected}
        select={select}
        close={close}
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

it("labels each direct terminal tab with a friendly number and no internal ID", () => {
  const ids = [
    "6f1b1f2e-8f8e-4b8c-9a55-0d6a3d1c9a01",
    "0b7a2c4d-2b0e-4d7c-8d5e-7a4f4c3e9b02",
  ];
  render(
    <SessionTabsProvider>
      <SessionTabBar
        id="original"
        tabs={[
          { kind: SessionTabKind.Conversation },
          { kind: SessionTabKind.Terminal, id: ids[0]! },
          { kind: SessionTabKind.Terminal, id: ids[1]! },
        ]}
        selected={SessionTabKind.Conversation}
        select={() => {}}
        close={() => {}}
      />
    </SessionTabsProvider>,
  );
  expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    "Conversation",
    "Terminal 1",
    "Terminal 2",
  ]);
  expect(screen.queryByRole("tab", { name: "Terminals" })).toBeNull();
  const attributes = [
    ...document.querySelectorAll("[aria-label], [title]"),
  ].map(
    (node) => `${node.getAttribute("aria-label")}${node.getAttribute("title")}`,
  );
  for (const id of ids)
    for (const value of attributes) expect(value).not.toContain(id.slice(0, 8));
  expect(
    screen.getByRole("button", { name: "Close Terminal 2 tab" }),
  ).toBeTruthy();
});

it("renders the New browser page button after the tab items, outside the tablist and numeric positions", () => {
  const newPage = vi.fn();
  render(
    <SessionTabBar
      id="original"
      tabs={[
        { kind: SessionTabKind.Conversation },
        {
          kind: SessionTabKind.Page,
          profile: "profile",
          id: "page",
          title: "example.test",
          label: "https://example.test/",
        },
      ]}
      selected={SessionTabKind.Conversation}
      select={() => {}}
      close={() => {}}
      newPage={newPage}
    />,
  );
  const add = screen.getByRole("button", { name: "New browser page" });
  expect(screen.getAllByRole("tab")).toHaveLength(2);
  expect(screen.getByRole("tablist").contains(add)).toBe(false);
  expect(add.getAttribute("role")).toBeNull();
  expect(add.hasAttribute("aria-keyshortcuts")).toBe(false);
  expect(
    screen.getByRole("tablist").compareDocumentPosition(add) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  fireEvent.click(add);
  expect(newPage).toHaveBeenCalledWith(add);
  // No Browser singleton tab exists; the page is a peer of Conversation.
  expect(screen.queryByRole("tab", { name: "Browser" })).toBeNull();
  expect(
    screen.getByRole("tab", { name: "https://example.test/" }),
  ).toBeTruthy();
});

it("renders each tab as a flat item with a label button and a separate sibling close button", () => {
  const select = vi.fn(),
    close = vi.fn();
  const longPath = `${"nested/".repeat(20)}very-long-file-name.ts`;
  render(
    <SessionTabBar
      id="original"
      tabs={[
        { kind: SessionTabKind.Conversation },
        { kind: SessionTabKind.Files },
        {
          kind: SessionTabKind.File,
          repository: "repository",
          path: longPath,
        },
      ]}
      selected={SessionTabKind.Conversation}
      select={select}
      close={close}
    />,
  );
  const strip = screen.getByRole("tablist");
  expect(strip.classList.contains("tab-strip")).toBe(true);
  const tabs = screen.getAllByRole("tab");
  const items = tabs.map((tab) => tab.parentElement!);
  for (const item of items) {
    expect(item.classList.contains("tab-item")).toBe(true);
    expect(item.getAttribute("role")).toBe("presentation");
  }
  expect(items.map((item) => item.classList.contains("is-selected"))).toEqual([
    true,
    false,
    false,
  ]);
  for (const tab of tabs) expect(tab.classList.contains("tab-label")).toBe(true);
  // Conversation stays first and noncloseable; the others own a sibling close.
  expect(items[0].querySelectorAll(".tab-close")).toHaveLength(0);
  for (const item of items.slice(1)) {
    const buttons = item.querySelectorAll("button");
    expect(buttons).toHaveLength(2);
    expect(buttons[1].classList.contains("tab-close")).toBe(true);
    expect(buttons[0].contains(buttons[1])).toBe(false);
    expect(buttons[1].textContent).toBe("×");
  }
  // Dynamic labels truncate visually while the complete title and accessible
  // name remain available.
  expect(items[2].classList.contains("tab-item-dynamic")).toBe(true);
  expect(tabs[2].getAttribute("title")).toBe(longPath);
  expect(tabs[2].getAttribute("aria-label")).toContain(longPath);
  expect(tabs[2].textContent).toBe(longPath);
});

it("closing an inactive tab never selects it", () => {
  const select = vi.fn(),
    close = vi.fn();
  render(
    <SessionTabBar
      id="original"
      tabs={[
        { kind: SessionTabKind.Conversation },
        { kind: SessionTabKind.Files },
        { kind: SessionTabKind.Diagnostics },
      ]}
      selected={SessionTabKind.Diagnostics}
      select={select}
      close={close}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /Close Files/ }));
  expect(close).toHaveBeenCalledTimes(1);
  expect(close).toHaveBeenCalledWith("files");
  expect(select).not.toHaveBeenCalled();
});
