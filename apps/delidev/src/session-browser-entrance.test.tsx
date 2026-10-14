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
import { useRef } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  BrowserCapability,
  BrowserProfileSchema,
  BrowserProfileState,
  BrowserService,
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { SessionBrowser, type BrowserHandle } from "./session-browser";
import { SessionTabBar } from "./session-tab-bar";
import {
  SessionTabKind,
  SessionTabsProvider,
  sessionTabKey,
  useSessionTabs,
} from "./session-tabs";
import {
  dispatchAnimation,
  setAnimation,
  stubReducedMotion,
} from "./session-tool-entrance-helpers";

const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
vi.mock("@tauri-apps/api/event", () => ({
  listen: vi.fn(async () => () => {}),
}));

// A synthetic native-local page owner with one restored page. Nothing here
// establishes CEF, account or platform acceptance.
function fixture() {
  const accountId = newRequestId(),
    profileId = newRequestId(),
    pageId = newRequestId();
  const profile = create(BrowserProfileSchema, {
    id: profileId,
    revision: 1n,
    serverId: newRequestId(),
    deviceId: newRequestId(),
    accountId,
    state: BrowserProfileState.ACTIVE,
  });
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    revision: 7n,
  });
  const transport = createRouterTransport((router) =>
    router.service(BrowserService, {
      registerBrowserProfile: async () => ({ profile }),
      listBrowserProfiles: async () => ({
        profiles: [profile],
        nextPageToken: "",
      }),
      getBrowserCapabilities: () => ({
        capabilities: [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1],
      }),
    }),
  );
  const state = () => ({
    tabs: {
      tabs: [{ id: pageId, url: "https://fixture.test/1" }],
      selected: pageId,
    },
    removal_pending: false,
  });
  // A held inventory/observation reply that resolves late.
  let hold: Promise<void> | undefined;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages") {
      if (args.request.action === "observe") await hold;
      return { state: state(), receipt: null };
    }
    if (operation === "browser_tab_shortcuts") return undefined;
    return state();
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    accountId,
    session,
    transport,
    client,
    hold: () => {
      let release = () => {};
      hold = new Promise<void>((resolve) => {
        release = resolve;
      });
      return () => {
        hold = undefined;
        release();
      };
    },
  };
}
type Fixture = ReturnType<typeof fixture>;

function Workspace({ f, active }: { f: Fixture; active: boolean }) {
  const tabs = useSessionTabs(f.session.id);
  const handle = useRef<BrowserHandle | undefined>(undefined);
  return (
    <>
      <SessionTabBar
        id={f.session.id}
        tabs={tabs.tabs}
        selected={tabs.selected}
        select={(key) => tabs.store.select(f.session.id, key)}
        close={(key) => tabs.store.close(f.session.id, key)}
      />
      <SessionBrowser
        session={f.session}
        accountId={f.accountId}
        active={active}
        handle={handle}
        selectedPage={
          tabs.tab.kind === SessionTabKind.Page ? tabs.tab : undefined
        }
      />
    </>
  );
}
function tree(f: Fixture, active = true) {
  return (
    <>
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <SessionTabsProvider>
              <Workspace f={f} active={active} />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    </>
  );
}

const header = () =>
  document.querySelector<HTMLElement>("header.browser-header");
const entranceNodes = () => document.querySelectorAll("[data-entrance]");
const nativeOperations = (operation: string) =>
  native.mock.calls.filter(([name]) => name === operation).length;
const selectPage = () =>
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
const selectConversation = () =>
  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));

beforeEach(() => {
  stubReducedMotion(false);
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    x: 100,
    y: 100,
    left: 100,
    top: 100,
    width: 300,
    height: 200,
    right: 400,
    bottom: 300,
    toJSON: () => ({}),
  });
  native.mockReset();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete document.documentElement.dataset.animation;
});

it("animates only the browser header once per selected-page activation and never the native viewport", async () => {
  const f = fixture();
  render(tree(f));
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  // Nothing is selected yet, so there is no activation.
  expect(entranceNodes().length).toBe(0);
  selectPage();
  await waitFor(() =>
    expect(native.mock.calls.some(([name]) => name === "open_browser")).toBe(
      true,
    ),
  );
  const node = header()!;
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(entranceNodes().length).toBe(1);
  const viewport = document.querySelector(".browser-viewport")!;
  for (const guard of [
    viewport,
    document.querySelector(".browser-chrome")!,
    viewport.closest("section.session-browser")!,
    viewport.closest(".session-app-panel")!,
  ])
    expect(guard.hasAttribute("data-entrance")).toBe(false);
  // The Information dialog is never a descendant of the animated header.
  fireEvent.click(node.querySelector("button")!);
  const dialog = document.querySelector("dialog")!;
  expect(dialog).not.toBeNull();
  expect(dialog.closest("header")).toBeNull();
  dispatchAnimation(node);
  expect(entranceNodes().length).toBe(0);
});

it("replays once per explicit return and keeps native calls when a held observation resolves late", async () => {
  const f = fixture();
  render(tree(f));
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  selectPage();
  await waitFor(() => expect(header()).not.toBeNull());
  dispatchAnimation(header()!);
  const release = f.hold();
  // Switching away during a held native observation hides the page.
  selectConversation();
  await act(async () => {
    await Promise.resolve();
  });
  expect(entranceNodes().length).toBe(0);
  selectPage();
  expect(header()!.getAttribute("data-entrance")).toBe("y2");
  expect(entranceNodes().length).toBe(1);
  dispatchAnimation(header()!);
  await waitFor(() => expect(nativeOperations("open_browser")).toBe(2));
  await act(async () => {
    release();
    await Promise.resolve();
  });
  // The late result cannot replay the entrance.
  expect(entranceNodes().length).toBe(0);
  // One presentation per explicit activation: two opens, one hide, no extra.
  expect(nativeOperations("open_browser")).toBe(2);
  expect(
    native.mock.calls.filter(([, args]) => args?.action === "hide"),
  ).toHaveLength(1);
});

it("leaves no stale attribute after rapid leave and re-entry and ignores an inactive session", async () => {
  const f = fixture();
  const view = render(tree(f));
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  selectPage();
  await waitFor(() => expect(header()).not.toBeNull());
  expect(entranceNodes().length).toBe(1);
  selectConversation();
  expect(entranceNodes().length).toBe(0);
  selectPage();
  expect(entranceNodes().length).toBe(1);
  view.rerender(tree(f, false));
  expect(entranceNodes().length).toBe(0);
  view.rerender(tree(f, true));
  expect(entranceNodes().length).toBe(1);
});

it("is suppressed by Animation Off and reduced motion without replay on re-enable", async () => {
  const change = stubReducedMotion(false);
  const f = fixture();
  render(tree(f));
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(2));
  selectPage();
  await waitFor(() => expect(header()).not.toBeNull());
  expect(header()!.getAttribute("data-entrance")).toBe("y2");
  await setAnimation("false");
  expect(entranceNodes().length).toBe(0);
  await setAnimation(undefined);
  expect(entranceNodes().length).toBe(0);
  selectConversation();
  selectPage();
  expect(header()!.getAttribute("data-entrance")).toBe("y2");
  change(true);
  await waitFor(() => expect(entranceNodes().length).toBe(0));
  change(false);
  expect(entranceNodes().length).toBe(0);
});
