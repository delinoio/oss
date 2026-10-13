// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AppearanceProvider, Theme, type AppearanceBridge } from "./appearance";
import { defaultPreferences, palettes } from "./appearance-preferences";
import { MutationIntents } from "./mutation";
import { Settings } from "./settings";

function dispatch(node: Element, type = "animationend") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: "entrance-y4" });
  act(() => {
    node.dispatchEvent(event);
  });
}
function view(children: React.ReactNode, bridge?: AppearanceBridge) {
  const transport = createRouterTransport(() => {});
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const content = (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return bridge ? (
    <AppearanceProvider bridge={bridge}>{content}</AppearanceProvider>
  ) : (
    content
  );
}
const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays the Appearance entrance once per explicit category opening and and not for categories that have not opted in", async () => {
  render(view(<Settings />));
  // The default landing category (AI Subscription) opted in too, so leave it first.
  category("Instructions");
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("Appearance");
  expect(column().getAttribute("data-entrance")).toBe("y4");
  expect(column().querySelector("h1")?.textContent).toBe("Appearance");
  const node = column();
  dispatch(node);
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // Re-selecting the current category is not a new activation.
  category("Appearance");
  expect(column()).toBe(node);
  expect(column().getAttribute("data-entrance")).toBeNull();
  // Leaving and re-entering is a new explicit activation.
  category("Instructions");
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("Appearance");
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("leaves no stale motion when the category changes before completion", () => {
  render(view(<Settings />));
  category("Appearance");
  const old = column();
  category("Instructions");
  expect(document.contains(old)).toBe(false);
  dispatch(old);
  expect(column().getAttribute("data-entrance")).toBeNull();
  expect(column().querySelector(".appearance-content")).toBeNull();
});

it("settles in-flight motion for Animation Off then OS reduced motion without replay", async () => {
  document.documentElement.dataset.animation = "true";
  const listeners = new Set<() => void>();
  const media = {
    matches: false,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  render(view(<Settings />));
  category("Appearance");
  const node = column();
  const input = node.querySelector<HTMLElement>(
    ".appearance-content :is(input, select, button):not(:disabled)",
  )!;
  input.focus();
  expect(document.activeElement).toBe(input);
  expect(node.getAttribute("data-entrance")).toBe("y4");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(node.getAttribute("data-entrance")).toBeNull();
  act(() => {
    media.matches = true;
    listeners.forEach((changed) => changed());
  });
  act(() => {
    media.matches = false;
    listeners.forEach((changed) => changed());
  });
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  expect(document.activeElement).toBe(input);
});

it("does not play when Animation is Off at activation", () => {
  document.documentElement.dataset.animation = "false";
  render(view(<Settings />));
  category("Appearance");
  expect(column().getAttribute("data-entrance")).toBeNull();
});

function editorFixture() {
  const custom = {
    version: 1 as const,
    id: "019abcde-1234-7123-8123-123456789abc",
    name: "Entrance custom theme",
    ...structuredClone(palettes.dracula),
  };
  const preferences = { ...defaultPreferences(), custom_themes: [custom] };
  const bridge: AppearanceBridge = {
    read: vi.fn(async () => ({
      revision: 1,
      theme: Theme.System,
      problem: null,
      preferences,
    })),
    update: vi.fn(async (theme, revision, next) => ({
      theme,
      revision: revision + 1,
      problem: null,
      preferences: next,
    })),
    subscribe: vi.fn(async () => () => {}),
  };
  return bridge;
}
const dialog = () => document.querySelector<HTMLDialogElement>("dialog")!;
// The entrance lives on the inner block, never on the native modal host.
const editorSurface = () => dialog().firstElementChild as HTMLElement;

it("animates the custom theme editor once per explicit opening and keeps its draft", async () => {
  render(view(<Settings />, editorFixture()));
  category("Appearance");
  dispatch(column());
  const edit = await screen.findByRole("button", {
    name: /Edit custom theme/,
  });
  fireEvent.click(edit);
  const host = dialog();
  const editor = editorSurface();
  expect(editor.getAttribute("data-entrance")).toBe("y4");
  // The native modal host and its geometry stay unanimated.
  expect(host.hasAttribute("data-entrance")).toBe(false);
  expect(editor.parentElement).toBe(host);
  expect(editor.querySelector("h2")).not.toBeNull();
  const name = screen.getByRole("textbox", { name: "Theme name" });
  // Focus is never held for the animation, and edits never replay it.
  fireEvent.change(name, { target: { value: "Edited while entering" } });
  expect(dialog()).toBe(host);
  expect(editorSurface()).toBe(editor);
  expect(editor.getAttribute("data-entrance")).toBe("y4");
  dispatch(editor);
  expect(editor.getAttribute("data-entrance")).toBeNull();
  fireEvent.change(name, { target: { value: "Edited afterwards" } });
  expect(editor.getAttribute("data-entrance")).toBeNull();
  expect((name as HTMLInputElement).value).toBe("Edited afterwards");
  // Cancel disposes immediately with no exit animation, then reopen replays.
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull();
  fireEvent.click(
    await screen.findByRole("button", { name: /Edit custom theme/ }),
  );
  expect(editorSurface().getAttribute("data-entrance")).toBe("y4");
});

it("settles the custom theme editor when Animation turns Off mid-flight", async () => {
  document.documentElement.dataset.animation = "true";
  render(view(<Settings />, editorFixture()));
  category("Appearance");
  fireEvent.click(
    await screen.findByRole("button", { name: /Edit custom theme/ }),
  );
  const host = dialog();
  const editor = editorSurface();
  const name = screen.getByRole("textbox", { name: "Theme name" });
  expect(editor.getAttribute("data-entrance")).toBe("y4");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(editor.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(editor.getAttribute("data-entrance")).toBeNull();
  expect(dialog()).toBe(host);
  expect(editorSurface()).toBe(editor);
  expect(screen.getByRole("textbox", { name: "Theme name" })).toBe(name);
});

it("settles the in-flight category entrance before a native dialog mounts beneath it", async () => {
  render(view(<Settings />, editorFixture()));
  category("Appearance");
  const content = column();
  expect(content.getAttribute("data-entrance")).toBe("y4");
  fireEvent.click(
    await screen.findByRole("button", { name: /Edit custom theme/ }),
  );
  const host = dialog();
  // No animated ancestor wraps the dialog host; only the inner block moves.
  expect(content.contains(host)).toBe(true);
  expect(content.getAttribute("data-entrance")).toBeNull();
  expect(host.parentElement?.closest("[data-entrance]")).toBeNull();
  expect(editorSurface().getAttribute("data-entrance")).toBe("y4");
  // Settling is final: closing the dialog does not replay the category.
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(content.getAttribute("data-entrance")).toBeNull();
});

it("settles the category entrance before the import dialog mounts", async () => {
  render(view(<Settings />, editorFixture()));
  category("Appearance");
  const content = column();
  expect(content.getAttribute("data-entrance")).toBe("y4");
  fireEvent.click(
    await screen.findByRole("button", { name: "Import JSON theme" }),
  );
  expect(dialog().parentElement?.closest("[data-entrance]")).toBeNull();
  expect(content.getAttribute("data-entrance")).toBeNull();
});
