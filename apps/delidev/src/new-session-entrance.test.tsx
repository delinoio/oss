// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ResourceService,
  SessionService,
  SystemService,
  SystemCapability,
} from "@delinoio/delidev-api-client";
import { NewSession, NewSessionKind } from "./new-session";
import { MutationIntents } from "./mutation";

// Synthetic fixtures only: the entrance contract is activation-scoped, so the
// transport stays minimal and every read resolves to an empty page.
function fixture(kind: NewSessionKind) {
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: () => ({}),
      listResources: () => ({ resources: [] }),
    });
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.AUTOMATIC_TITLES_V1],
      }),
    });
    router.service(SessionService, {});
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (active = true, activation = 1) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <NewSession
            kind={kind}
            active={active}
            ownsActivation={active}
            activation={activation}
            back={() => {}}
            openSettings={() => {}}
            open={() => {}}
            created={() => {}}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { view };
}

function dispatch(node: Element, name: string, type = "animationend") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}
function reducedMotion(initial: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}
const page = () =>
  document.querySelector<HTMLElement>("section.new-session-page")!;
const entrance = () => page().getAttribute("data-entrance");

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

const kinds = [
  [NewSessionKind.Session, "new-project-session-page"],
  [NewSessionKind.GeneralChat, "new-general-chat-page"],
] as const;

for (const [kind, pageClass] of kinds) {
  it(`plays one Y4 entrance on the ${kind} destination root only`, async () => {
    render(fixture(kind).view());
    const composer = await screen.findByRole("textbox", {
      name: "First message",
    });
    expect(page().classList.contains(pageClass)).toBe(true);
    expect(entrance()).toBe("y4");
    // Only the destination root carries motion; no inner surface or Options.
    expect(document.querySelectorAll("[data-entrance]").length).toBe(1);
    expect(page().closest("dialog, [popover]")).toBeNull();
    expect(page().querySelector("input[name$='-project']")).toBeNull();
    dispatch(page(), "entrance-y4");
    expect(entrance()).toBeNull();
    expect(screen.getByRole("textbox", { name: "First message" })).toBe(
      composer,
    );
  });

  it(`does not replay the ${kind} entrance for typing, Options or Plan Mode`, async () => {
    const f = fixture(kind);
    const mounted = render(f.view());
    const composer = await screen.findByRole("textbox", {
      name: "First message",
    });
    const node = page();
    dispatch(node, "entrance-y4");
    fireEvent.change(composer, { target: { value: "Typing after entrance" } });
    fireEvent.click(screen.getByRole("button", { name: "Options" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Plan Mode" }));
    mounted.rerender(f.view(true, 1));
    expect(entrance()).toBeNull();
    // Controllers, draft and the page element keep identity.
    expect(page()).toBe(node);
    expect(screen.getByRole("textbox", { name: "First message" })).toBe(
      composer,
    );
    expect((composer as HTMLTextAreaElement).value).toBe(
      "Typing after entrance",
    );
  });

  it(`keeps an in-flight ${kind} entrance running through typing and Options`, async () => {
    render(fixture(kind).view());
    const composer = await screen.findByRole("textbox", {
      name: "First message",
    });
    expect(entrance()).toBe("y4");
    fireEvent.change(composer, { target: { value: "Typing in flight" } });
    fireEvent.click(screen.getByRole("button", { name: "Options" }));
    // Neither restarted nor stopped early, and focus never waited for it.
    expect(entrance()).toBe("y4");
    expect(document.activeElement).toBe(composer);
    dispatch(page(), "entrance-y4");
    expect(entrance()).toBeNull();
  });

  it(`replays ${kind} only on an explicit activation or a return to the retained page`, async () => {
    const f = fixture(kind);
    const mounted = render(f.view());
    const composer = await screen.findByRole("textbox", {
      name: "First message",
    });
    fireEvent.change(composer, { target: { value: "Retained draft" } });
    dispatch(page(), "entrance-y4");
    // Navigating to Settings and back is a rising edge: one new entrance.
    mounted.rerender(f.view(false, 1));
    expect(entrance()).toBeNull();
    expect(page().hidden).toBe(true);
    mounted.rerender(f.view(true, 1));
    expect(entrance()).toBe("y4");
    expect(screen.getByRole("textbox", { name: "First message" })).toBe(
      composer,
    );
    expect((composer as HTMLTextAreaElement).value).toBe("Retained draft");
    dispatch(page(), "entrance-y4");
    expect(entrance()).toBeNull();
    // A fresh New action while already active is an explicit activation too.
    mounted.rerender(f.view(true, 2));
    expect(entrance()).toBe("y4");
    dispatch(page(), "entrance-y4");
    mounted.rerender(f.view(true, 2));
    expect(entrance()).toBeNull();
  });

  it(`leaves no stale ${kind} completion after rapid leave and re-entry`, async () => {
    const f = fixture(kind);
    const mounted = render(f.view());
    await screen.findByRole("textbox", { name: "First message" });
    expect(entrance()).toBe("y4");
    mounted.rerender(f.view(false, 1));
    mounted.rerender(f.view(true, 1));
    expect(entrance()).toBe("y4");
    // A stale cancellation from the replaced run cannot settle the new one
    // when its animation is not the entrance primitive.
    dispatch(page(), "spinner");
    expect(entrance()).toBe("y4");
    dispatch(page(), "entrance-y4");
    expect(entrance()).toBeNull();
    // Leaving mid-flight keeps the hidden page free of retained motion.
    mounted.rerender(f.view(true, 3));
    expect(entrance()).toBe("y4");
    mounted.rerender(f.view(false, 3));
    expect(page().hidden).toBe(true);
    dispatch(page(), "entrance-y4");
    expect(entrance()).toBeNull();
  });

  it(`settles the ${kind} entrance for reduced motion and Animation Off without replay`, async () => {
    const change = reducedMotion(false);
    render(fixture(kind).view());
    await screen.findByRole("textbox", { name: "First message" });
    expect(entrance()).toBe("y4");
    change(true);
    expect(entrance()).toBeNull();
    change(false);
    expect(entrance()).toBeNull();
  });

  it(`settles the ${kind} entrance when Animation turns Off and never starts while Off`, async () => {
    const mounted = render(fixture(kind).view());
    await screen.findByRole("textbox", { name: "First message" });
    expect(entrance()).toBe("y4");
    await act(async () => {
      document.documentElement.dataset.animation = "false";
      await Promise.resolve();
    });
    expect(entrance()).toBeNull();
    await act(async () => {
      document.documentElement.dataset.animation = "true";
      await Promise.resolve();
    });
    expect(entrance()).toBeNull();
    mounted.unmount();
    document.documentElement.dataset.animation = "false";
    const off = fixture(kind);
    const again = render(off.view(false, 1));
    again.rerender(off.view(true, 1));
    expect(entrance()).toBeNull();
  });

  it(`does not animate an inactive ${kind} page`, async () => {
    render(fixture(kind).view(false));
    await screen.findByRole(
      "textbox",
      { name: "First message", hidden: true },
      {},
    );
    expect(page().hidden).toBe(true);
    expect(entrance()).toBeNull();
  });
}

it("keeps Options content instant and never animates its container", async () => {
  render(fixture(NewSessionKind.Session).view());
  await screen.findByRole("textbox", { name: "First message" });
  dispatch(page(), "entrance-y4");
  const options = screen.getByRole("button", { name: "Options" });
  fireEvent.click(options);
  expect(options.getAttribute("aria-expanded")).toBe("true");
  expect(document.querySelectorAll("[data-entrance]").length).toBe(0);
});

it("declares the shared static Y4 shape without opacity, scale or inline style", () => {
  const css = readFileSync(join(__dirname, "entrance-motion.css"), "utf8");
  expect(css).toMatch(
    /\[data-entrance="y4"\]\s*\{\s*animation:\s*entrance-y4 160ms ease-out;/u,
  );
  expect(css).toMatch(
    /@keyframes entrance-y4\s*\{\s*from\s*\{\s*transform: translateY\(4px\);\s*\}\s*to\s*\{\s*transform: none;/u,
  );
  const source = readFileSync(join(__dirname, "new-session.tsx"), "utf8");
  expect(source).toContain("useEntranceActivation(");
  expect(source).toContain("EntranceMotion.Y4");
});
