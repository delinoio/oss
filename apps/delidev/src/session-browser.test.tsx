// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useRef, useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  BrowserService,
  BrowserCapability,
  BrowserProfileSchema,
  BrowserProfileState,
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import {
  SessionBrowser,
  browserProfile,
  browserTabTitle,
  validAddress,
  type BrowserController,
} from "./session-browser";
import { BrowserHostProvider } from "./host-capabilities";
import {
  SessionTabsProvider,
  SessionTabKind,
  sessionTabKey,
  useSessionTabs,
  type SessionTabsStore,
} from "./session-tabs";
import { SessionTabBar } from "./session-tab-bar";
import { i18n, SupportedLanguage } from "./localization";
const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
vi.mock("@tauri-apps/api/event", () => ({
  listen: vi.fn(async () => () => {}),
}));
const clients: QueryClient[] = [];
beforeEach(() => {
  native.mockReset();
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
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([
    { width: 300, height: 200 },
  ] as unknown as DOMRectList);
});
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.restoreAllMocks();
});
type Receipt = {
  operation_id: string;
  page_id: string;
  phase: string;
  metadata_committed: boolean;
  failure: string | null;
};
function fixture({
  count = 2,
  registered = true,
  available = true,
  supported = true,
}: {
  count?: number;
  registered?: boolean;
  available?: boolean;
  supported?: boolean;
} = {}) {
  const accountId = newRequestId(),
    profileId = newRequestId();
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
  let local = {
    tabs: {
      tabs: Array.from({ length: count }, (_, index) => ({
        id: newRequestId(),
        url: `https://fixture.test/${index}`,
      })),
      selected: "",
    },
    removal_pending: false,
  };
  local.tabs.selected = local.tabs.tabs[0]?.id ?? "";
  const original = [...local.tabs.tabs];
  const receipts = new Map<string, Receipt>();
  const closed: string[] = [];
  const register = vi.fn(async (_request: unknown) => ({ profile }));
  const list = vi.fn(async () => ({ profiles: registered ? [profile] : [] }));
  const transport = createRouterTransport((router) =>
    router.service(BrowserService, {
      registerBrowserProfile: register,
      listBrowserProfiles: list,
      getBrowserCapabilities: () => ({
        capabilities: supported
          ? [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1]
          : [],
      }),
    }),
  );
  const reply = (receipt: Receipt | null = null) => ({
    state: structuredClone(local),
    receipt: receipt ? { ...receipt } : null,
    closed_pages: [...closed],
  });
  const handle = async (command: string, input: any) => {
    if (command === "browser_pages") {
      const request = input.request;
      if (request.action === "inventory") return reply();
      if (request.action === "observe")
        return reply(receipts.get(request.operation_id)!);
      const page =
        request.action === "create"
          ? { id: newRequestId(), url: request.url }
          : local.tabs.tabs.find((page) => page.id === request.page_id)!;
      if (request.action === "create")
        local = {
          ...local,
          tabs: { tabs: [...local.tabs.tabs, page], selected: page.id },
        };
      else {
        const tabs = local.tabs.tabs.filter((value) => value.id !== page.id);
        local = {
          ...local,
          tabs: {
            tabs,
            selected: tabs.some((page) => page.id === local.tabs.selected)
              ? local.tabs.selected
              : (tabs[0]?.id ?? ""),
          },
        };
        closed.push(page.id);
      }
      const receipt = {
        operation_id: request.operation_id,
        page_id: page.id,
        phase: "completed",
        metadata_committed: true,
        failure: null,
      };
      receipts.set(receipt.operation_id, receipt);
      return reply(receipt);
    }
    if (command === "open_browser") local.tabs.selected = input.pageId;
    if (input?.action === "select-tab") local.tabs.selected = input.tabId;
    if (input?.action === "navigate")
      local.tabs.tabs = local.tabs.tabs.map((page) =>
        page.id === local.tabs.selected ? { ...page, url: input.url } : page,
      );
    return structuredClone(local);
  };
  native.mockImplementation(handle);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  let store!: SessionTabsStore;
  function Browser({ active = true }: { active?: boolean }) {
    const tabs = useSessionTabs(session.id);
    store = tabs.store;
    const [dialog, setDialog] = useState(false);
    const controller = useRef(new Map<string, BrowserController>());
    return (
      <>
        <textarea
          aria-label="Composer"
          defaultValue="retained draft"
          hidden={tabs.tab.kind !== SessionTabKind.Conversation}
        />
        <SessionTabBar
          id={session.id}
          tabs={tabs.tabs}
          selected={tabs.selected}
          select={(key) => tabs.store.select(session.id, key)}
          close={(key) => {
            const tab = tabs.tabs.find((tab) => sessionTabKey(tab) === key);
            if (tab?.kind === SessionTabKind.Page)
              controller.current.get(tab.profile)?.closePage(tab.id);
            else tabs.store.close(session.id, key);
          }}
          newPage={() => setDialog(true)}
        />
        <button onClick={() => setDialog(true)}>Browser tool</button>
        <BrowserHostProvider available={available}>
          <SessionBrowser
            session={session}
            accountId={accountId}
            close={() => {}}
            active={active}
            presentationActive={active && tabs.tab.kind === SessionTabKind.Page}
            dialogOpen={dialog}
            dismissDialog={() => setDialog(false)}
            controller={controller}
            selectedPage={
              tabs.tab.kind === SessionTabKind.Page ? tabs.tab : undefined
            }
            openPage={(page) =>
              tabs.store.open(session.id, {
                kind: SessionTabKind.Page,
                ...page,
              })
            }
          />
        </BrowserHostProvider>
      </>
    );
  }
  function View({
    active = true,
    strict = false,
  }: {
    active?: boolean;
    strict?: boolean;
  }) {
    const body = (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <SessionTabsProvider>
              <Browser active={active} />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
    return strict ? <StrictMode>{body}</StrictMode> : body;
  }
  return {
    View,
    session,
    accountId,
    profileId,
    profile,
    register,
    list,
    original,
    receipts,
    closed,
    reply,
    handle,
    get store() {
      return store;
    },
    get local() {
      return local;
    },
  };
}
const actions = (action: string) =>
  native.mock.calls.filter(
    ([command, input]) =>
      command === "browser_pages" && input.request.action === action,
  );
async function dialog(opener = "New browser page") {
  fireEvent.click(screen.getByRole("button", { name: opener }));
  const modal = await screen.findByRole("dialog", { name: "New browser page" });
  await waitFor(() =>
    expect(document.activeElement).toBe(
      within(modal).getByRole("textbox", { name: "Address" }),
    ),
  );
  return modal;
}
async function submit(url = "https://new.test/page") {
  const modal = await dialog();
  fireEvent.change(within(modal).getByRole("textbox", { name: "Address" }), {
    target: { value: url },
  });
  const open = within(modal).getByRole("button", { name: "Open page" });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(open);
}
it("restores every original page once without selecting or creating a resource", async () => {
  const f = fixture();
  render(<f.View strict />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  expect(screen.getAllByRole("tablist")).toHaveLength(1);
  expect(screen.queryByRole("tab", { name: "Browser" })).toBeNull();
  expect(screen.queryByRole("group", { name: "Browser tabs" })).toBeNull();
  expect(f.store.snapshot(f.session.id).selected).toBe(
    SessionTabKind.Conversation,
  );
  expect(
    f.store
      .snapshot(f.session.id)
      .tabs.slice(1)
      .map((tab) => (tab.kind === SessionTabKind.Page ? tab.id : "")),
  ).toEqual(f.original.map((page) => page.id));
  expect(f.register).not.toHaveBeenCalled();
  expect(actions("create")).toHaveLength(0);
  expect(
    native.mock.calls.filter(([command]) => command === "open_browser"),
  ).toHaveLength(0);
});
it.each(["Cancel", "Escape", "Close New browser page"])(
  "uses one explicit dialog and creates nothing on %s",
  async (method) => {
    const f = fixture();
    render(<f.View />);
    const opener = screen.getByRole("button", { name: "New browser page" });
    opener.focus();
    const modal = await dialog();
    expect(
      (
        within(modal).getByRole("button", {
          name: "Open page",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    if (method === "Escape")
      fireEvent(
        modal,
        new Event("cancel", { bubbles: true, cancelable: true }),
      );
    else fireEvent.click(within(modal).getByRole("button", { name: method }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(opener);
    await dialog("Browser tool");
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    expect(f.register).not.toHaveBeenCalled();
    expect(actions("create")).toHaveLength(0);
  },
);
it.each([false, true])(
  "creates exactly one explicit page with original registration proof (restored: %s)",
  async (registered) => {
    const f = fixture({ registered, count: registered ? 2 : 0 });
    render(<f.View strict />);
    await submit();
    await waitFor(() => expect(actions("create")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(f.local.tabs.tabs).toHaveLength(registered ? 3 : 1);
    expect(f.register).toHaveBeenCalledTimes(1);
    expect(f.register.mock.calls[0]?.[0]).toMatchObject({
      accountId: f.accountId,
      session: { id: f.session.id, expectedRevision: 7n },
    });
    expect(
      screen.getByRole("tab", {
        name: "https://new.test/page",
        selected: true,
      }),
    ).toBeTruthy();
    expect(
      native.mock.calls.filter(
        ([command, input]) =>
          command === "control_browser" &&
          ["new-tab", "close-tab"].includes(input.action),
      ),
    ).toHaveLength(0);
  },
);
it.each([
  "javascript:alert(1)",
  "file:///fixture",
  "https://user:secret@fixture.test/",
  "https://fixture.test/" + "a".repeat(8193),
  "https://fixture.test/\n",
])("rejects an invalid address without mutation: %s", async (url) => {
  const f = fixture();
  render(<f.View />);
  const modal = await dialog();
  fireEvent.change(within(modal).getByRole("textbox", { name: "Address" }), {
    target: { value: url },
  });
  expect(validAddress(url)).toBe(false);
  expect(
    (
      within(modal).getByRole("button", {
        name: "Open page",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(actions("create")).toHaveLength(0);
  expect(f.register).not.toHaveBeenCalled();
});
it("blocks explicit creation at sixteen pages and preserves readable guidance", async () => {
  const f = fixture({ count: 16 });
  render(<f.View />);
  const modal = await dialog();
  fireEvent.change(within(modal).getByRole("textbox", { name: "Address" }), {
    target: { value: "https://new.test/" },
  });
  await screen.findByText(
    "This account profile already has 16 pages. Close a page before opening another.",
  );
  expect(
    (
      within(modal).getByRole("button", {
        name: "Open page",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(f.register).not.toHaveBeenCalled();
});
it("retains lost creation ownership across dismissal and observes the original operation", async () => {
  const f = fixture({ count: 0 });
  let allowObserve = false;
  native.mockImplementation(async (command, input) => {
    if (
      command === "browser_pages" &&
      input.request.action === "observe" &&
      !allowObserve
    )
      throw new Error("original receipt is temporarily unavailable");
    const result = await f.handle(command, input);
    if (command === "browser_pages" && input.request.action === "create")
      throw new Error("lost synthetic reply");
    return result;
  });
  render(<f.View />);
  await submit();
  await waitFor(() => expect(actions("create")).toHaveLength(1));
  const original = actions("create")[0][1].request.operation_id;
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }),
  );
  const modal = await dialog();
  expect(
    (
      within(modal).getByRole("button", {
        name: "Open page",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  allowObserve = true;
  fireEvent.click(
    within(modal).getByRole("button", { name: "Observe original operation" }),
  );
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(actions("create")).toHaveLength(1);
  expect(actions("observe").at(-1)?.[1].request).toEqual({
    action: "observe",
    operation_id: original,
  });
  expect(f.local.tabs.tabs).toHaveLength(1);
});
it("keeps a pending creation owner mounted while its dialog is dismissed", async () => {
  const f = fixture({ count: 0 });
  let finish!: (result: any) => void;
  native.mockImplementation(async (command, input) => {
    const result = await f.handle(command, input);
    if (command === "browser_pages" && input.request.action === "create")
      return new Promise((resolve) => {
        finish = resolve.bind(null, result);
      });
    return result;
  });
  render(<f.View />);
  await submit();
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }),
  );
  const modal = await dialog();
  expect(
    (
      within(modal).getByRole("button", {
        name: "Open page",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  await act(async () => finish(undefined));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(actions("create")).toHaveLength(1);
});
it("keeps exact registration uncertainty without creating a page or changing its request", async () => {
  const f = fixture({ registered: false, count: 0 });
  f.register.mockRejectedValueOnce(
    new ConnectError("synthetic unknown registration", Code.Unavailable),
  );
  render(<f.View />);
  await submit();
  const modal = screen.getByRole("dialog");
  const retry = await within(modal).findByRole("button", {
    name: "Retry the same registration",
  });
  expect(actions("create")).toHaveLength(0);
  const original = f.register.mock.calls[0]?.[0];
  fireEvent.change(within(modal).getByRole("textbox", { name: "Address" }), {
    target: { value: "https://changed.test/" },
  });
  fireEvent.click(retry);
  await waitFor(() => expect(actions("create")).toHaveLength(1));
  expect(f.register.mock.calls[1]?.[0]).toEqual(original);
  expect(actions("create")[0][1].request.url).toBe("https://new.test/page");
});
it.each(["inactive", "active", "final"])(
  "closes the original %s page with correct presentation and focus",
  async (kind) => {
    const f = fixture({ count: kind === "final" ? 1 : 2 });
    render(<f.View />);
    await waitFor(() =>
      expect(screen.getAllByRole("tab")).toHaveLength(f.original.length + 1),
    );
    if (kind !== "inactive")
      fireEvent.click(screen.getByRole("tab", { name: f.original[0].url }));
    if (kind !== "inactive")
      await waitFor(() =>
        expect(
          native.mock.calls.some(([command]) => command === "open_browser"),
        ).toBe(true),
      );
    const opened = native.mock.calls.filter(
      ([command]) => command === "open_browser",
    ).length;
    fireEvent.click(
      screen.getByRole("button", {
        name: `Close ${browserTabTitle(f.original[0].url)} tab`,
      }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: f.original[0].url })).toBeNull(),
    );
    expect(actions("close")[0][1].request.page_id).toBe(f.original[0].id);
    expect(f.store.snapshot(f.session.id).selected).toBe(
      SessionTabKind.Conversation,
    );
    expect(
      native.mock.calls.filter(([command]) => command === "open_browser"),
    ).toHaveLength(opened);
    if (kind === "final") expect(f.local.tabs.tabs).toHaveLength(0);
    expect(
      (screen.getByRole("textbox", { name: "Composer" }) as HTMLTextAreaElement)
        .value,
    ).toBe("retained draft");
  },
);
it("retains a descriptor after close metadata publication until original cleanup settles", async () => {
  const f = fixture();
  let pending = true;
  native.mockImplementation(async (command, input) => {
    const result = await f.handle(command, input);
    if (
      command === "browser_pages" &&
      ["close", "observe"].includes(input.request.action) &&
      pending
    ) {
      return {
        ...result,
        closed_pages: [],
        receipt: {
          ...(result as ReturnType<typeof f.reply>).receipt,
          phase: "cleanup-pending",
          failure: "sidecar-failed",
        },
      };
    }
    return result;
  });
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  fireEvent.click(
    screen.getByRole("button", {
      name: `Close ${browserTabTitle(f.original[0].url)} tab`,
    }),
  );
  await screen.findByText(
    "Page metadata was removed. Original native child cleanup is still pending.",
  );
  expect(screen.getByRole("tab", { name: f.original[0].url })).toBeTruthy();
  const original = actions("close")[0][1].request.operation_id;
  pending = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Observe original operation" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("tab", { name: f.original[0].url })).toBeNull(),
  );
  expect(actions("close")).toHaveLength(1);
  expect(actions("observe").at(-1)?.[1].request.operation_id).toBe(original);
});
it("does not remove a descriptor from a malformed or lost close result", async () => {
  const f = fixture();
  native.mockImplementation(async (command, input) => {
    if (command === "browser_pages" && input.request.action === "close")
      throw new Error("synthetic lost close");
    if (command === "browser_pages" && input.request.action === "observe")
      return {
        state: f.local,
        receipt: {
          operation_id: newRequestId(),
          page_id: f.original[0].id,
          phase: "completed",
          metadata_committed: true,
          failure: null,
        },
        closed_pages: [],
      };
    return f.handle(command, input);
  });
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  fireEvent.click(
    screen.getByRole("button", {
      name: `Close ${browserTabTitle(f.original[0].url)} tab`,
    }),
  );
  await screen.findByRole("button", { name: "Observe original operation" });
  fireEvent.click(
    screen.getByRole("button", { name: "Observe original operation" }),
  );
  await waitFor(() => expect(actions("observe")).toHaveLength(1));
  expect(screen.getByRole("tab", { name: f.original[0].url })).toBeTruthy();
  expect(actions("close")).toHaveLength(1);
});
it("hides native content for information and restores only the original page", async () => {
  const f = fixture();
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: f.original[0].url }));
  await waitFor(() =>
    expect(
      native.mock.calls.some(([command]) => command === "open_browser"),
    ).toBe(true),
  );
  const original = native.mock.calls.find(
    ([command]) => command === "open_browser",
  )![1];
  fireEvent.click(screen.getByRole("button", { name: "Browser information" }));
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      "control_browser",
      expect.objectContaining({ action: "hide", viewId: original.viewId }),
    ),
  );
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Close Browser information",
    }),
  );
  await waitFor(() =>
    expect(
      native.mock.calls.filter(([command]) => command === "open_browser"),
    ).toHaveLength(2),
  );
  expect(actions("create")).toHaveLength(0);
  expect(f.register).not.toHaveBeenCalled();
  expect(document.querySelector("iframe")).toBeNull();
});
it("blocks replacement until original Hide succeeds, including after presentation departure", async () => {
  const f = fixture();
  let release = false;
  native.mockImplementation(async (command, input) => {
    if (command === "control_browser" && input.action === "hide" && !release)
      throw new Error("synthetic hide failure");
    return f.handle(command, input);
  });
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: f.original[0].url }));
  await waitFor(() =>
    expect(
      native.mock.calls.some(([command]) => command === "open_browser"),
    ).toBe(true),
  );
  const original = native.mock.calls.find(
    ([command]) => command === "open_browser",
  )![1];
  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await waitFor(() =>
    expect(
      native.mock.calls.some(
        ([command, input]) =>
          command === "control_browser" && input.action === "hide",
      ),
    ).toBe(true),
  );
  fireEvent.click(screen.getByRole("tab", { name: f.original[1].url }));
  expect(
    native.mock.calls.filter(([command]) => command === "open_browser"),
  ).toHaveLength(1);
  release = true;
  await waitFor(() =>
    expect(
      native.mock.calls.filter(([command]) => command === "open_browser"),
    ).toHaveLength(2),
  );
  expect(
    native.mock.calls
      .filter(
        ([command, input]) =>
          command === "control_browser" && input.action === "hide",
      )
      .every(([, input]) => input.viewId === original.viewId),
  ).toBe(true);
});
it("keeps navigation on the original profile/view and updates URL-derived labels in place", async () => {
  const f = fixture();
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: f.original[0].url }));
  await waitFor(() =>
    expect(
      native.mock.calls.some(([command]) => command === "open_browser"),
    ).toBe(true),
  );
  const keys = f.store.snapshot(f.session.id).tabs.map(sessionTabKey);
  const address = screen.getByRole("textbox", { name: "Address" });
  fireEvent.change(address, {
    target: { value: "https://navigation.test/new" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Go" }));
  await screen.findByRole("tab", {
    name: "https://navigation.test/new",
    selected: true,
  });
  expect(f.store.snapshot(f.session.id).tabs.map(sessionTabKey)).toEqual(keys);
  expect(actions("create")).toHaveLength(0);
  expect(
    native.mock.calls.filter(
      ([command, input]) =>
        command === "control_browser" && input.action === "navigate",
    )[0][1].profileId,
  ).toBe(f.profileId);
});
it.each([false, true])(
  "shows unsupported guidance without registration or native authority (host:%s)",
  async (available) => {
    const f = fixture({ available, supported: false });
    render(<f.View />);
    await dialog();
    expect(f.register).not.toHaveBeenCalled();
    expect(actions("create")).toHaveLength(0);
  },
);
it("uses Korean names and retains ordinary plus semantics outside numeric tab positions", async () => {
  const f = fixture();
  render(<f.View />);
  await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(3));
  await act(async () => i18n.changeLanguage(SupportedLanguage.Korean));
  const plus = screen.getByRole("button", { name: "새 브라우저 페이지" });
  expect(plus.getAttribute("role")).toBeNull();
  expect(plus.getAttribute("aria-keyshortcuts")).toBeNull();
  fireEvent.click(plus);
  const modal = screen.getByRole("dialog", { name: "새 브라우저 페이지" });
  expect(
    within(modal).getByRole("button", { name: "페이지 열기" }),
  ).toBeTruthy();
});
it("rejects deleted, foreign and malformed original profile evidence", () => {
  const f = fixture();
  expect(browserProfile(f.profile, f.accountId)).toBe(f.profileId);
  expect(() => browserProfile(f.profile, newRequestId())).toThrow();
  expect(() =>
    browserProfile(
      { ...f.profile, state: BrowserProfileState.REMOVAL_PENDING },
      f.accountId,
    ),
  ).toThrow();
  expect(() =>
    browserProfile({ ...f.profile, revision: 0n }, f.accountId),
  ).toThrow();
});
