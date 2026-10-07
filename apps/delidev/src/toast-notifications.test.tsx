// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useEffect } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { NotificationProvider, ToastKind, useNotifications, type NotificationController } from "./toast-notifications";
import { i18n, ownedMessage } from "./localization";

afterEach(() => vi.useRealTimers());
function fixture() {
  let controller!: NotificationController;
  let renders = 0;
  function Consumer() { controller = useNotifications(); renders++; return <main id="main" tabIndex={-1}><input aria-label="Draft" /><button>Other action</button></main>; }
  const view = (identity = "same") => <NotificationProvider key={identity}><Consumer /></NotificationProvider>;
  const mounted = render(view());
  return { mounted, view, get controller() { return controller; }, get renders() { return renders; } };
}
function tick(ms: number) { act(() => vi.advanceTimersByTime(ms)); }

it("language updates an existing toast without republishing or restarting its expiry", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  const value = fixture();
  const notify = vi.spyOn(value.controller, "notify");
  act(() => { value.controller.notify({ id: "original-request", kind: ToastKind.Success, message: ownedMessage("notification-settings.savedToast") }); });
  tick(1000);
  const toast = document.querySelector(".toast-notification");
  await act(() => i18n.changeLanguage("ko"));
  expect(document.querySelector(".toast-notification")).toBe(toast);
  expect(screen.getByText("알림 환경 설정을 저장했습니다.")).toBeTruthy();
  expect(notify).toHaveBeenCalledTimes(1);
  tick(3999); expect(document.querySelector(".toast-notification")).toBe(toast);
  tick(1); expect(document.querySelector(".toast-notification")).toBeNull();
});

it("language preserves a focused toast close button and original text", async () => {
  const value = fixture();
  act(() => { value.controller.notify({ id: "original", kind: ToastKind.Warning, message: "Untranslated original user text <b>inert</b>", durationMs: 0 }); });
  const close = screen.getByRole("button", { name: "Dismiss notification: Untranslated original user text <b>inert</b>" });
  act(() => close.focus());
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("button", { name: "알림 닫기: Untranslated original user text <b>inert</b>" })).toBe(close);
  expect(document.activeElement).toBe(close);
  expect(screen.getByText("Untranslated original user text <b>inert</b>")).toBeTruthy();
});

it("renders inert text with distinct accessible kinds and does not rerender consumers", () => {
  const value = fixture();
  const draft = screen.getByRole("textbox"); draft.focus();
  act(() => {
    value.controller.notify({ id: "success", kind: ToastKind.Success, message: "Saved", durationMs: 0 });
    value.controller.notify({ id: "info", kind: ToastKind.Info, message: "<a href='https://example.com'>Text</a>", durationMs: 0 });
    value.controller.notify({ id: "warning", kind: ToastKind.Warning, message: "Check settings", durationMs: 0 });
    value.controller.notify({ id: "error", kind: ToastKind.Error, message: "Failed", durationMs: 0 });
  });
  expect(value.renders).toBe(1);
  expect(document.activeElement).toBe(draft);
  expect(screen.getAllByRole("status")).toHaveLength(3);
  expect(screen.queryByRole("link")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Dismiss notification: Saved" }));
  expect(screen.getByRole("alert").textContent).toBe("Error: Failed");
  act(() => value.controller.notify({ id: "error", kind: ToastKind.Error, message: "Updated", durationMs: 0 }));
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(screen.getByRole("alert").textContent).toBe("Error: Updated");
});

it("pauses automatic expiry for hover and keyboard focus and restores draft focus on Escape", () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  const value = fixture();
  const draft = screen.getByRole("textbox"); draft.focus();
  act(() => { value.controller.notify({ kind: ToastKind.Success, message: "Saved" }); });
  const toast = screen.getByText("Saved").closest("article")!;
  tick(2000); fireEvent.mouseEnter(toast); tick(10000);
  const close = within(toast).getByRole("button"); act(() => close.focus());
  fireEvent.mouseLeave(toast); tick(10000);
  expect(screen.getByText("Saved")).toBeTruthy();
  fireEvent.keyDown(close, { key: "Escape" });
  expect(screen.queryByText("Saved")).toBeNull();
  expect(document.activeElement).toBe(draft);
  expect(vi.getTimerCount()).toBe(0);
});

it("pauses expiry when the document is hidden", () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  const value = fixture();
  act(() => { value.controller.notify({ kind: ToastKind.Info, message: "Visible time" }); });
  tick(1000);
  const hidden = vi.spyOn(document, "hidden", "get");
  hidden.mockReturnValue(true); fireEvent(document, new Event("visibilitychange"));
  tick(10000);
  expect(screen.getByText("Visible time")).toBeTruthy();
  hidden.mockReturnValue(false); fireEvent(document, new Event("visibilitychange"));
  tick(3999); expect(screen.getByText("Visible time")).toBeTruthy();
  tick(1); expect(screen.queryByText("Visible time")).toBeNull();
});

it("uses the main content fallback when the previous focus target is now disabled", () => {
  const value = fixture();
  const draft = screen.getByRole("textbox") as HTMLInputElement; draft.focus();
  act(() => { value.controller.notify({ kind: ToastKind.Info, message: "Saved", durationMs: 0 }); });
  const close = screen.getByRole("button", { name: "Dismiss notification: Saved" });
  act(() => close.focus()); draft.disabled = true;
  fireEvent.keyDown(close, { key: "Escape" });
  expect(document.activeElement).toBe(document.getElementById("main"));
});

it("defers presentation behind modals but ignores the wide nonmodal sidebar region", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  const value = fixture();
  const dialog = document.createElement("dialog"); document.body.append(dialog);
  try {
    dialog.setAttribute("role", "region"); dialog.showModal();
    act(() => { value.controller.notify({ kind: ToastKind.Info, message: "Deferred" }); });
    await act(async () => {});
    expect(screen.getByRole("region", { name: "Notifications" }).hidden).toBe(false);
    tick(1000);
    dialog.setAttribute("role", "dialog");
    await act(async () => {});
    expect(screen.queryByRole("region", { name: "Notifications" })).toBeNull();
    tick(10000);
    dialog.close();
    await act(async () => {});
    expect(screen.getByText("Deferred")).toBeTruthy();
    tick(3999); expect(screen.getByText("Deferred")).toBeTruthy();
    tick(1); expect(screen.queryByText("Deferred")).toBeNull();
  } finally { dialog.remove(); }
});

it("keeps one viewport during Strict Mode replay and rejects calls from a disposed connection", () => {
  let controller!: NotificationController;
  function Consumer() {
    controller = useNotifications();
    useEffect(() => { controller.notify({ id: "once", kind: ToastKind.Info, message: "One notification", durationMs: 0 }); }, []);
    return null;
  }
  const view = (identity: string) => <StrictMode><NotificationProvider key={identity}><Consumer /></NotificationProvider></StrictMode>;
  const mounted = render(view("first"));
  expect(screen.getAllByText("One notification")).toHaveLength(1);
  expect(screen.getAllByRole("region", { name: "Notifications" })).toHaveLength(1);
  const original = controller;
  mounted.rerender(view("second"));
  act(() => original.notify({ kind: ToastKind.Error, message: "Late callback" }));
  expect(screen.queryByText("Late callback")).toBeNull();
  expect(screen.getAllByText("One notification")).toHaveLength(1);
  mounted.unmount();
  expect(document.querySelector(".toast-viewport")).toBeNull();
});

it("checks an already open modal when the first toast arrives after an idle interval", async () => {
  const value = fixture();
  const dialog = document.createElement("dialog"); document.body.append(dialog);
  try {
    dialog.showModal();
    await act(async () => {});
    act(() => { value.controller.notify({ kind: ToastKind.Warning, message: "Waiting for dialog", durationMs: 0 }); });
    expect(screen.queryByRole("region", { name: "Notifications" })).toBeNull();
    dialog.close();
    await act(async () => {});
    expect(screen.getByText("Waiting for dialog")).toBeTruthy();
  } finally { dialog.remove(); }
});

it("retains notifications across same-provider navigation and clears them on identity replacement", () => {
  const value = fixture();
  act(() => { value.controller.notify({ kind: ToastKind.Success, message: "Retained", durationMs: 0 }); });
  value.mounted.rerender(value.view());
  expect(screen.getByText("Retained")).toBeTruthy();
  value.mounted.rerender(value.view("replacement"));
  expect(screen.queryByText("Retained")).toBeNull();
});
