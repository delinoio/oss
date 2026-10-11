// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionTabBar } from "./session-tab-bar";
import { SessionTabKind } from "./session-tabs";
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

it("keeps numbered terminal labels and focus independent of original IDs and locale", async () => {
  const { i18n, SupportedLanguage } = await import("./localization");
  const original = "0192b234-6789-7abc-8def-012345678901";
  const tabs = [
    { kind: SessionTabKind.Conversation } as const,
    { kind: SessionTabKind.Terminal, id: original, number: 7 } as const,
  ];
  const selected = JSON.stringify(["terminal", original]);
  const props = {
    id: "session",
    tabs,
    selected,
    select: vi.fn(),
    close: vi.fn(),
  };
  const view = render(<SessionTabBar {...props} />);
  const tab = screen.getByRole("tab", { name: "Terminal 7" });
  expect(tab.title).toBe("Terminal 7");
  expect(tab.textContent).not.toContain(original);
  tab.focus();
  await i18n.changeLanguage(SupportedLanguage.Korean);
  view.rerender(<SessionTabBar {...props} />);
  expect(screen.getByRole("tab", { name: "터미널 7" })).toBe(tab);
  expect(tab.title).toBe("터미널 7");
  expect(document.activeElement).toBe(tab);
});

it("keeps New browser page as an ordinary reachable button after the numeric tabs", () => {
  const open = vi.fn();
  render(
    <SessionTabBar
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

// Keep original callbacks and selection independent of the enclosing visual item.
it("closes an inactive tab without selecting it and retains full dynamic labels", () => {
  const select = vi.fn(),
    close = vi.fn();
  const path = "a/".repeat(180) + "original.ts";
  render(
    <SessionTabBar
      id="flat"
      tabs={[
        { kind: SessionTabKind.Conversation },
        { kind: SessionTabKind.File, path, repository: "original-repository" },
        {
          kind: SessionTabKind.Page,
          profile: "original",
          id: "page",
          title: "fixture.test",
        },
      ]}
      selected={JSON.stringify(["page", "original", "page"])}
      select={select}
      close={close}
    />,
  );
  const label = screen.getByRole("tab", {
    name: `${path} · original-repository`,
  });
  expect(label.title).toBe(path);
  expect(label.classList.contains("shared-tab-label--truncate")).toBe(true);
  const item = label.closest(".shared-tab-item")!;
  const button = item.querySelector<HTMLButtonElement>(".shared-tab-close")!;
  expect(button.parentElement).toBe(label.parentElement);
  expect(button.textContent).toBe("×");
  fireEvent.click(button);
  expect(select).not.toHaveBeenCalled();
  expect(close).toHaveBeenCalledTimes(1);
  expect(
    screen
      .getByRole("tab", { name: "fixture.test" })
      .getAttribute("aria-selected"),
  ).toBe("true");
});
