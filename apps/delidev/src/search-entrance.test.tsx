// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ConversationSearchHitSchema,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SearchService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SidebarOutletProvider } from "./sidebar-context";
import { Search } from "./views";

function fixture() {
  const sessionId = newRequestId();
  const hit = (name: string) =>
    create(ConversationSearchHitSchema, {
      sessionName: name,
      message: create(ResourceSchema, {
        id: newRequestId(),
        sessionId,
        kind: EntityKind.MESSAGE,
        revision: 1n,
        schemaVersion: 1,
        documentJson: encode({ text: `Body of ${name}` }),
      }),
    });
  const hits = [hit("Review changes"), hit("Plan next task")];
  const searches = vi.fn((_request: unknown) => ({
    hits,
    nextPageToken: "",
  }));
  const transport = createRouterTransport((router) => {
    router.service(SearchService, { searchConversations: searches });
    router.service(ResourceService, {
      listResources: () => ({ resources: [], nextPageToken: "" }),
      getResource: () => ({ resource: undefined }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = (active = true, drawerOpen = false) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SidebarOutletProvider
          target={null}
          closeDrawer={() => undefined}
          drawerOpen={drawerOpen}
        >
          <Search active={active} open={() => undefined} />
        </SidebarOutletProvider>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { view, searches };
}

const page = () => document.querySelector<HTMLElement>("section.page")!;
const entering = () => page().getAttribute("data-entrance");
const query = () =>
  screen.getByRole("textbox", { name: "Search conversations" });

function finish(name = "entrance-y4-short", type = "animationend") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    page().dispatchEvent(event);
  });
}
function media(reduced: boolean, compact = false) {
  const listeners = new Set<() => void>();
  const state = { reduced, compact };
  vi.stubGlobal("matchMedia", (queryText: string) => ({
    get matches() {
      return queryText.includes("prefers-reduced-motion")
        ? state.reduced
        : queryText.includes("max-width: 759px")
          ? state.compact
          : false;
    },
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  }));
  return (next: boolean) =>
    act(() => {
      state.reduced = next;
      listeners.forEach((changed) => changed());
    });
}
async function submit(text: string) {
  fireEvent.change(query(), { target: { value: text } });
  await act(async () => {
    fireEvent.submit(query().closest("form")!);
  });
}

beforeEach(() => {
  media(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one 120ms Y4 entrance on the destination section and settles on its own animationend", async () => {
  const f = fixture();
  render(f.view());
  expect(entering()).toBe("y4-short");
  // Only the results section carries motion: filters, inputs and borders do not.
  expect(document.querySelectorAll("[data-entrance]").length).toBe(1);
  expect(page().contains(query())).toBe(false);
  // Focus admission never waits for the motion.
  await waitFor(() => expect(document.activeElement).toBe(query()));
  expect(entering()).toBe("y4-short");
  // A different primitive finishing does not settle it.
  finish("entrance-y4");
  expect(entering()).toBe("y4-short");
  finish();
  expect(entering()).toBeNull();
});

it("never replays for typing, submission, result replacement or refresh", async () => {
  const f = fixture();
  render(f.view());
  const node = page();
  finish();
  fireEvent.change(query(), { target: { value: "val" } });
  fireEvent.compositionStart(query());
  fireEvent.change(query(), { target: { value: "valida" } });
  fireEvent.compositionEnd(query());
  expect(entering()).toBeNull();
  await submit("validation");
  await screen.findByText("Review changes");
  expect(entering()).toBeNull();
  // Resubmitting the identical query refreshes the result set in place.
  await submit("validation");
  await waitFor(() => expect(f.searches.mock.calls.length).toBeGreaterThan(1));
  // A new query replaces results.
  await submit("another");
  await waitFor(() => expect(f.searches.mock.calls.length).toBeGreaterThan(2));
  expect(page()).toBe(node);
  expect(entering()).toBeNull();
  expect(screen.getByText("Plan next task")).toBeTruthy();
});

it("replays only for a new destination activation and keeps controllers mounted", async () => {
  const f = fixture();
  const mounted = render(f.view());
  const input = query();
  finish();
  mounted.rerender(f.view(false));
  expect(page().hidden).toBe(true);
  expect(entering()).toBeNull();
  mounted.rerender(f.view(true));
  expect(entering()).toBe("y4-short");
  expect(query()).toBe(input);
  // Rapid leave and re-entry before completion leaves one live activation.
  mounted.rerender(f.view(false));
  mounted.rerender(f.view(true));
  expect(entering()).toBe("y4-short");
  expect(document.querySelectorAll("[data-entrance]").length).toBe(1);
  finish();
  expect(entering()).toBeNull();
  // A stale completion after leaving cannot affect the inactive surface.
  mounted.rerender(f.view(false));
  expect(entering()).toBeNull();
});

it("does not animate a Search that mounts inactive until it is activated", async () => {
  const f = fixture();
  const mounted = render(f.view(false));
  expect(page().hidden).toBe(true);
  expect(entering()).toBeNull();
  mounted.rerender(f.view(true));
  expect(entering()).toBe("y4-short");
});

it("settles mid-flight for Animation Off and reduced motion without replay", async () => {
  const change = media(false);
  const f = fixture();
  const mounted = render(f.view());
  expect(entering()).toBe("y4-short");
  change(true);
  expect(entering()).toBeNull();
  change(false);
  expect(entering()).toBeNull();
  mounted.rerender(f.view(false));
  mounted.rerender(f.view(true));
  expect(entering()).toBe("y4-short");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(entering()).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(entering()).toBeNull();
});

it("does not start motion while Animation is Off or reduced motion is requested", () => {
  document.documentElement.dataset.animation = "false";
  const off = render(fixture().view());
  expect(entering()).toBeNull();
  off.unmount();
  delete document.documentElement.dataset.animation;
  media(true);
  render(fixture().view());
  expect(entering()).toBeNull();
});

it("keeps the compact drawer focus admission unchanged by the entrance", async () => {
  media(false, true);
  const f = fixture();
  const mounted = render(f.view(true, false));
  // Compact Search waits for an explicit drawer opening, not for the motion.
  await act(async () => {
    await new Promise((resolve) => window.requestAnimationFrame(resolve));
  });
  expect(document.activeElement).not.toBe(query());
  mounted.rerender(f.view(true, true));
  await waitFor(() => expect(document.activeElement).toBe(query()));
  finish();
  mounted.rerender(f.view(true, false));
  mounted.rerender(f.view(true, true));
  // Drawer opening is not a destination activation.
  expect(entering()).toBeNull();
  expect(document.activeElement).toBe(query());
});

it("defines the entrance through a static shared primitive and no inline style", () => {
  render(fixture().view());
  const source = readFileSync(join(__dirname, "views.tsx"), "utf8");
  expect(source).toMatch(/EntranceMotion\.Y4Short/u);
  expect(source).not.toMatch(/style=\{/u);
  expect(page().getAttribute("style")).toBeNull();
});
