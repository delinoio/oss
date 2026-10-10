// SPDX-License-Identifier: Apache-2.0
import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Search } from "./views";
import { SidebarOutletProvider } from "./sidebar-context";
const shortcut = vi.hoisted(() => ({ focus: undefined as undefined | (() => void) }));
vi.mock("./shortcut-provider", () => ({ useShortcuts: (definitions: { run?: () => void }[]) => { shortcut.focus = definitions.find(value => value.run)?.run; return { aria: () => "" }; } }));
vi.mock("./configuration-fields", () => ({ ResourceChoice: () => null }));
vi.mock("./scroll-pagination-query", () => ({ useConnectPaginationReader: () => undefined, usePaginationChain: () => ({ loaded: false, rows: [], refresh: () => undefined }), usePaginationRefresh: () => undefined }));
vi.mock("./scroll-continuation", () => ({ useScrollRoot: () => null, ScrollContinuation: () => null }));
vi.mock("./scroll-payload-window", () => ({ ScrollPayloadWindow: () => null }));
afterEach(() => vi.unstubAllGlobals());
function fixture(open = vi.fn(() => true)) {
  vi.stubGlobal("requestAnimationFrame", vi.fn(() => 1)); vi.stubGlobal("cancelAnimationFrame", vi.fn());
  const view = (visible = false, ready = false, active = true, allowed = true) => <SidebarOutletProvider target={null} closeDrawer={() => undefined} drawerOpen={false} paneVisible={visible} paneReady={ready} paneFocusAllowed={allowed} openDrawer={open}><Search active={active} open={() => undefined} /></SidebarOutletProvider>;
  const mounted = render(view()), input = screen.getByLabelText("Search conversations") as HTMLInputElement;
  const focus = vi.spyOn(input, "focus");
  return { view, mounted, focus, open };
}
it("focuses one current Search destination only after opening settlement", () => {
  const f = fixture(); act(() => shortcut.focus?.()); expect(f.open).toHaveBeenCalledOnce();
  f.mounted.rerender(f.view(true, false)); expect(f.focus).not.toHaveBeenCalled();
  f.mounted.rerender(f.view(true, true)); expect(f.focus).toHaveBeenCalledOnce();
  expect(requestAnimationFrame).not.toHaveBeenCalled();
});
it("does not queue rejected opens or ordinary later expansion autofocus", () => {
  const f = fixture(vi.fn(() => false)); act(() => shortcut.focus?.());
  f.mounted.rerender(f.view(true, true)); expect(f.focus).not.toHaveBeenCalled(); expect(requestAnimationFrame).not.toHaveBeenCalled();
});
it.each(["collapse", "navigation", "failure"])("retires a pending Search focus after %s", reason => {
  const f = fixture(); act(() => shortcut.focus?.()); f.mounted.rerender(f.view(true, false));
  if (reason === "collapse") f.mounted.rerender(f.view(false, false));
  if (reason === "navigation") f.mounted.rerender(f.view(true, false, false));
  if (reason === "failure") f.mounted.rerender(f.view(true, false, true, false));
  f.mounted.rerender(f.view(true, true)); expect(f.focus).not.toHaveBeenCalled();
});
