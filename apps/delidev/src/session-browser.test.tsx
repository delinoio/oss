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
import { beforeEach, expect, it, vi } from "vitest";
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
  type BrowserHandle,
} from "./session-browser";
import { BrowserHostProvider } from "./host-capabilities";
import { SessionTabBar } from "./session-tab-bar";
import {
  SessionTabKind,
  SessionTabsProvider,
  sessionTabKey,
  useSessionTabs,
  useSessionTabsStore,
  SessionTabsStore as SessionTabsStoreImpl,
  type SessionTabsStore,
} from "./session-tabs";

const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
vi.mock("@tauri-apps/api/event", () => ({
  listen: vi.fn(async () => () => {}),
}));
beforeEach(() => {
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

enum Phase {
  Accepted = "accepted",
  CleanupPending = "cleanup-pending",
  Completed = "completed",
  Failed = "failed",
}
interface Receipt {
  operation_id: string;
  page_id: string;
  phase: Phase;
  metadata_committed: boolean;
  native_cleanup_complete: boolean;
  failure: string | null;
}
interface Page {
  id: string;
  url: string;
}
type Request = {
  action: "inventory" | "create" | "close" | "observe";
  operation_id?: string;
  url?: string;
  page_id?: string;
};

/**
 * A synthetic native-local page owner: it keeps one profile inventory and
 * original operation receipts exactly as the native command contract does.
 * Nothing here establishes CEF, account or platform acceptance.
 */
function fixture(options: { registered?: boolean; pages?: number } = {}) {
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
  let registered = options.registered ?? true;
  const register = vi.fn(async (_request: unknown) => {
    registered = true;
    return { profile };
  });
  const list = vi.fn(async () => ({
    profiles: registered ? [profile] : [],
    nextPageToken: "",
  }));
  const transport = createRouterTransport((router) =>
    router.service(BrowserService, {
      registerBrowserProfile: register,
      listBrowserProfiles: list,
      getBrowserCapabilities: () => ({
        capabilities: [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1],
      }),
    }),
  );
  const model = {
    pages: Array.from({ length: options.pages ?? 0 }, (_, index) => ({
      id: newRequestId(),
      url: `https://fixture.test/${index + 1}`,
    })) as Page[],
    selected: "",
    receipts: new Map<string, Receipt>(),
    requests: [] as Request[],
    // Which outcome the next mutation reports.
    outcome: "complete" as
      "complete" | "lose-reply" | "cleanup-pending" | "fail" | "not-admitted",
    // The receipt is settled by the next observation.
    settleOnObserve: true,
    // The profile is being removed: native reports an empty pending inventory.
    removalPending: false,
  };
  model.selected = model.pages[0]?.id ?? "";
  const state = () =>
    model.removalPending
      ? { tabs: { tabs: [], selected: "" }, removal_pending: true }
      : {
          tabs: {
            tabs: model.pages.map((page) => ({ ...page })),
            selected: model.selected,
          },
          removal_pending: false,
        };
  const receipt = (
    request: Request,
    page_id: string,
    phase: Phase,
  ): Receipt => ({
    operation_id: request.operation_id!,
    page_id,
    phase,
    metadata_committed: phase !== Phase.Failed,
    native_cleanup_complete: phase === Phase.Completed,
    failure: phase === Phase.Failed ? "storage-unavailable" : null,
  });
  const pages = (request: Request) => {
    model.requests.push(request);
    if (request.action === "inventory")
      return { state: state(), receipt: null };
    if (request.action === "observe") {
      const known = model.receipts.get(request.operation_id!);
      if (known?.phase === Phase.CleanupPending && model.settleOnObserve) {
        known.phase = Phase.Completed;
        known.native_cleanup_complete = true;
      }
      return { state: state(), receipt: known ?? null };
    }
    if (model.receipts.has(request.operation_id!))
      return {
        state: state(),
        receipt: model.receipts.get(request.operation_id!),
      };
    if (model.outcome === "not-admitted")
      throw { kind: "not-admitted", failure: "busy" };
    const phase =
      model.outcome === "cleanup-pending"
        ? Phase.CleanupPending
        : model.outcome === "fail"
          ? Phase.Failed
          : Phase.Completed;
    let page_id = request.page_id ?? newRequestId();
    if (phase !== Phase.Failed) {
      if (request.action === "create") {
        model.pages.push({ id: page_id, url: request.url! });
        model.selected = page_id;
      } else {
        const index = model.pages.findIndex((page) => page.id === page_id);
        model.pages = model.pages.filter((page) => page.id !== page_id);
        if (model.selected === page_id)
          model.selected = model.pages[Math.max(0, index - 1)]?.id ?? "";
      }
    }
    const made = receipt(request, page_id, phase);
    model.receipts.set(request.operation_id!, made);
    if (model.outcome === "lose-reply") {
      // The mutation is committed but the reply is lost.
      model.outcome = "complete";
      throw new Error("reply lost");
    }
    return { state: state(), receipt: made };
  };
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages") return pages(args.request);
    if (operation === "control_browser" && args.action === "select-tab")
      model.selected = args.tabId;
    if (operation === "browser_tab_shortcuts") return undefined;
    return state();
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    accountId,
    profileId,
    profile,
    session,
    register,
    list,
    model,
    state,
    transport,
    client,
  };
}
type Fixture = ReturnType<typeof fixture>;

/** Mirrors the SessionView wiring: shared tab bar, + button and page close. */
function Workspace({ f, active = true }: { f: Fixture; active?: boolean }) {
  const tabs = useSessionTabs(f.session.id);
  const [dialog, setDialog] = useState(false);
  const handle = useRef<BrowserHandle | undefined>(undefined);
  return (
    <>
      <SessionTabBar
        id={f.session.id}
        tabs={tabs.tabs}
        selected={tabs.selected}
        select={(key) => tabs.store.select(f.session.id, key)}
        close={(key) => {
          const tab = tabs.tabs.find((value) => sessionTabKey(value) === key);
          if (tab?.kind === SessionTabKind.Page)
            handle.current?.closePage(tab.profile, tab.id);
          else tabs.store.close(f.session.id, key);
        }}
      />
      <button type="button" onClick={() => setDialog(true)}>
        Open tool Browser
      </button>
      <button
        type="button"
        onClick={() =>
          tabs.store.open(f.session.id, { kind: SessionTabKind.Files })
        }
      >
        Open Files fixture tab
      </button>
      <SessionBrowser
        session={f.session}
        accountId={f.accountId}
        active={active}
        dialogOpen={dialog && active}
        dismissDialog={() => setDialog(false)}
        handle={handle}
        selectedPage={
          tabs.tab.kind === SessionTabKind.Page ? tabs.tab : undefined
        }
      />
    </>
  );
}
function mount(f: Fixture, strict = false, active = true) {
  const tree = (visible: boolean) => (
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <SessionTabsProvider>
            <Workspace f={f} active={visible} />
          </SessionTabsProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(
    strict ? <StrictMode>{tree(active)}</StrictMode> : tree(active),
  );
  return {
    ...view,
    setActive: (visible: boolean) =>
      view.rerender(
        strict ? <StrictMode>{tree(visible)}</StrictMode> : tree(visible),
      ),
  };
}
const calls = (operation: string) =>
  native.mock.calls.filter(([name]) => name === operation);
const pageCalls = (action: Request["action"]) =>
  native.mock.calls
    .filter(([name]) => name === "browser_pages")
    .map(([, args]) => args.request as Request)
    .filter((request) => request.action === action);
const tabNames = () =>
  screen.getAllByRole("tab").map((tab) => tab.getAttribute("title"));
async function openDialog() {
  fireEvent.click(screen.getByRole("button", { name: "Open tool Browser" }));
  return screen.findByRole("dialog", { name: "New browser page" });
}
async function submit(dialog: HTMLElement, address: string) {
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Address" }), {
    target: { value: address },
  });
  const open = within(dialog).getByRole("button", { name: "Open page" });
  await waitFor(() => expect((open as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(open);
}

it("an unavailable browser host does not register a profile or call native code", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    revision: 1n,
  });
  const view = render(
    <BrowserHostProvider available={false}>
      <SessionBrowser
        session={session}
        accountId={newRequestId()}
        dialogOpen
        dismissDialog={() => {}}
      />
    </BrowserHostProvider>,
  );
  const dialog = screen.getByRole("dialog", { name: "New browser page" });
  expect(
    within(dialog).getByText(
      "CEF browser profiles require the supported desktop host.",
    ),
  ).toBeTruthy();
  expect(
    within(dialog).getByRole("button", { name: "Open page" }),
  ).toHaveProperty("disabled", true);
  expect(native).not.toHaveBeenCalled();
  view.unmount();
});

it("restores retained pages as peer tabs without registering, creating or presenting one", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  expect(tabNames()).toEqual([
    "Conversation",
    "https://fixture.test/1",
    "https://fixture.test/2",
  ]);
  // Conversation stays selected: restoration selects nothing and shows nothing.
  expect(
    screen
      .getByRole("tab", { name: "Conversation" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(screen.queryByRole("tab", { name: "Browser" })).toBeNull();
  expect(screen.queryByRole("group", { name: "Browser tabs" })).toBeNull();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
  expect(calls("open_browser")).toHaveLength(0);
  // Repeated reads do not duplicate descriptors.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 3200));
  });
  expect(tabNames()).toHaveLength(3);
  expect(pageCalls("create")).toHaveLength(0);
});

it("never creates anything while the profile is absent or the inventory is empty", async () => {
  const f = fixture({ registered: false });
  mount(f);
  await waitFor(() => expect(f.list).toHaveBeenCalled());
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
  expect(tabNames()).toEqual(["Conversation"]);
  expect(f.register).not.toHaveBeenCalled();
  expect(native).not.toHaveBeenCalled();
});

it("opens the address dialog from the Open tool Browser entry and creates nothing on dismissal", async () => {
  const f = fixture({ registered: false });
  mount(f);
  const dialog = await openDialog();
  expect(dialog.className).toContain("browser-new-page");
  const address = within(dialog).getByRole("textbox", { name: "Address" });
  expect(document.activeElement).toBe(address);
  expect(
    within(dialog).getByRole("button", { name: "Open page" }),
  ).toHaveProperty("disabled", true);
  expect(
    within(dialog).getByText(/Shared with this account's sessions/),
  ).toBeTruthy();
  // Cancel, the X control and Escape all dismiss without any request.
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "Open tool Browser" }));
  const again = await screen.findByRole("dialog", { name: "New browser page" });
  fireEvent.click(
    within(again).getByRole("button", { name: "Close New browser page" }),
  );
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await openDialog();
  fireEvent(
    screen.getByRole("dialog"),
    new Event("cancel", { cancelable: true }),
  );
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
  expect(tabNames()).toEqual(["Conversation"]);
});

it.each([
  "file:///fixture",
  "https://user:secret@fixture.test/",
  "javascript:alert(1)",
  "not an address",
])(
  "keeps Open page disabled without native authority for %s",
  async (address) => {
    const f = fixture({ registered: false });
    mount(f);
    const dialog = await openDialog();
    fireEvent.change(within(dialog).getByRole("textbox", { name: "Address" }), {
      target: { value: address },
    });
    expect(
      within(dialog).getByRole("button", { name: "Open page" }),
    ).toHaveProperty("disabled", true);
    fireEvent.submit(dialog.querySelector("form")!);
    expect(f.register).not.toHaveBeenCalled();
    expect(pageCalls("create")).toHaveLength(0);
  },
);

it("registers a missing profile only on submission, then creates and selects exactly one page", async () => {
  const f = fixture({ registered: false });
  mount(f, true);
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/new");
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(f.register).toHaveBeenCalledTimes(1);
  expect(f.register.mock.calls[0][0]).toMatchObject({
    accountId: f.accountId,
    session: { id: f.session.id, expectedRevision: 7n },
  });
  expect(pageCalls("create")).toHaveLength(1);
  expect(pageCalls("create")[0]).toMatchObject({
    url: "https://fixture.test/new",
  });
  expect(pageCalls("create")[0].operation_id).toMatch(/^[0-9a-f-]{36}$/);
  const tab = screen.getByRole("tab", { name: "https://fixture.test/new" });
  expect(tab.getAttribute("aria-selected")).toBe("true");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  // Presentation restores the created page by identity and never a URL.
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  expect(calls("open_browser")[0][1]).toMatchObject({
    profileId: f.profileId,
    pageId: f.model.pages[0].id,
  });
  expect(calls("open_browser")[0][1]).not.toHaveProperty("url");
  expect(f.model.pages).toHaveLength(1);
});

it("creates exactly one page in a restored nonempty profile and selects the confirmed identity", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/third");
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(1);
  expect(tabNames().at(-1)).toBe("https://fixture.test/third");
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/third" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(f.model.pages).toHaveLength(3);
});

it("preserves the exact uncertain registration request and creates one page after its retry", async () => {
  const f = fixture({ registered: false });
  f.register.mockRejectedValueOnce(
    new ConnectError("uncertain", Code.Unavailable),
  );
  mount(f);
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/new");
  await within(dialog).findByRole("button", {
    name: "Retry the same registration",
  });
  expect(f.register).toHaveBeenCalledTimes(1);
  expect(pageCalls("create")).toHaveLength(0);
  const original = f.register.mock.calls[0][0];
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Retry the same registration" }),
  );
  await waitFor(() => expect(pageCalls("create")).toHaveLength(1));
  expect(f.register.mock.calls[1][0]).toEqual(original);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(pageCalls("create")).toHaveLength(1);
});

it("rejects a foreign or deleted profile before any native page operation", async () => {
  const f = fixture({ registered: false });
  f.profile.accountId = newRequestId();
  mount(f);
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/new");
  await within(dialog).findByText(
    "Browser profile ownership is unavailable. Refresh the session.",
  );
  expect(native).not.toHaveBeenCalled();
  expect(() =>
    browserProfile(
      {
        ...f.profile,
        accountId: f.accountId,
        state: BrowserProfileState.REMOVAL_PENDING,
        deletionRequestId: newRequestId(),
      },
      f.accountId,
    ),
  ).toThrow();
});

it("settles a lost Create reply by observing the original operation and never creates a second page", async () => {
  const f = fixture({ pages: 1 });
  f.model.outcome = "lose-reply";
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/lost");
  const check = await within(dialog).findByRole("button", {
    name: "Check status",
  });
  expect(pageCalls("create")).toHaveLength(1);
  const original = pageCalls("create")[0].operation_id;
  // Dismissal does not drop ownership; reopening resumes the same recovery and
  // never offers a fresh creation while the original outcome is unsettled.
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  const reopened = await openDialog();
  expect(
    within(reopened).getByRole("button", { name: "Open page" }),
  ).toHaveProperty("disabled", true);
  fireEvent.click(
    within(reopened).getByRole("button", { name: "Check status" }),
  );
  expect(check).toBeTruthy();
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const observed = pageCalls("observe");
  expect(observed.length).toBeGreaterThan(0);
  expect(observed.every((request) => request.operation_id === original)).toBe(
    true,
  );
  expect(pageCalls("create")).toHaveLength(1);
  expect(f.model.pages).toHaveLength(2);
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/lost" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("treats a rejection before admission as a safe failure and sends a fresh identity on retry", async () => {
  const f = fixture({ pages: 1 });
  f.model.outcome = "not-admitted";
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/rejected");
  await within(dialog).findByText(
    "The browser action failed. Check its address and the current server and account authorization.",
  );
  expect(tabNames()).toHaveLength(2);
  expect(
    within(dialog).queryByRole("button", { name: "Check status" }),
  ).toBeNull();
  f.model.outcome = "complete";
  await submit(dialog, "https://fixture.test/rejected");
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const creates = pageCalls("create");
  expect(creates).toHaveLength(2);
  expect(creates[0].operation_id).not.toBe(creates[1].operation_id);
  expect(f.model.pages).toHaveLength(2);
});

it("reports a failed Create without a descriptor and allows an explicit retry", async () => {
  const f = fixture({ pages: 1 });
  f.model.outcome = "fail";
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/failed");
  await within(dialog).findByRole("alert");
  expect(tabNames()).toHaveLength(2);
  expect(f.model.pages).toHaveLength(1);
});

it("disables creation with guidance at the 16-page limit", async () => {
  const f = fixture({ pages: 16 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(17));
  const dialog = await openDialog();
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Address" }), {
    target: { value: "https://fixture.test/seventeen" },
  });
  expect(
    within(dialog).getByText(
      "A profile holds at most 16 browser pages. Close one to open another.",
    ),
  ).toBeTruthy();
  expect(
    within(dialog).getByRole("button", { name: "Open page" }),
  ).toHaveProperty("disabled", true);
  expect(pageCalls("create")).toHaveLength(0);
});

it("closes the actual native page for an inactive tab, keeping the selection and showing nothing", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const [first] = f.model.pages;
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(pageCalls("close")).toHaveLength(1);
  expect(pageCalls("close")[0]).toMatchObject({ page_id: first.id });
  expect(f.model.pages.map((page) => page.url)).toEqual([
    "https://fixture.test/2",
  ]);
  expect(
    screen
      .getByRole("tab", { name: "Conversation" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  // The inactive page was never presented before or after its closure.
  expect(calls("open_browser")).toHaveLength(0);
  expect(
    native.mock.calls.filter(
      ([name, args]) => name === "control_browser" && args.action !== "hide",
    ),
  ).toHaveLength(0);
});

it("keeps the page descriptor until the original close is confirmed, then selects and focuses the left neighbor", async () => {
  const f = fixture({ pages: 2 });
  let release!: () => void;
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages" && args.request.action === "close")
      await new Promise<void>((resolve) => (release = resolve));
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/2 tab",
  });
  close.focus();
  fireEvent.click(close);
  await waitFor(() => expect(release).toBeTypeOf("function"));
  // Pending: the descriptor and its selection remain, with inline progress.
  expect(tabNames()).toHaveLength(3);
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/2" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(screen.getByRole("status").textContent).toContain("Closing the page");
  await act(async () => release());
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const neighbor = screen.getByRole("tab", { name: "https://fixture.test/1" });
  expect(neighbor.getAttribute("aria-selected")).toBe("true");
  await waitFor(() => expect(document.activeElement).toBe(neighbor));
  expect(pageCalls("close")).toHaveLength(1);
});

it("focuses the actual selected tool-tab fallback after acknowledged Page close", async () => {
  const f = fixture({ pages: 1 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(
    screen.getByRole("button", { name: "Open Files fixture tab" }),
  );
  const dialog = await openDialog();
  await submit(dialog, "https://fixture.test/new");
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("tab", { name: "https://fixture.test/new" }),
    ),
  );
  const close = screen.getByRole("button", {
    name: "Close fixture.test/new tab",
  });
  close.focus();
  fireEvent.click(close);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const fallback = screen.getByRole("tab", { name: "Files" });
  await waitFor(() => expect(document.activeElement).toBe(fallback));
  expect(fallback.getAttribute("aria-selected")).toBe("true");
});

it("uses the same focus handoff after deferred native cleanup", async () => {
  const f = fixture({ pages: 2 });
  f.model.outcome = "cleanup-pending";
  f.model.settleOnObserve = false;
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/2 tab",
  });
  close.focus();
  fireEvent.click(close);
  await screen.findByRole("alert");
  expect(document.activeElement).toBe(close);
  f.model.settleOnObserve = true;
  await waitFor(() => expect(tabNames()).toHaveLength(2), { timeout: 2500 });
  const fallback = screen.getByRole("tab", {
    name: "https://fixture.test/1",
  });
  await waitFor(() => expect(document.activeElement).toBe(fallback));
});

it("does not steal focus from a surviving control after Close is pending", async () => {
  const f = fixture({ pages: 2 });
  let release!: () => void;
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages" && args.request.action === "close")
      await new Promise<void>((resolve) => (release = resolve));
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/2 tab",
  });
  close.focus();
  fireEvent.click(close);
  await waitFor(() => expect(release).toBeTypeOf("function"));
  const unrelated = screen.getByRole("button", {
    name: "Open Files fixture tab",
  });
  unrelated.focus();
  await act(async () => release());
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(document.activeElement).toBe(unrelated);
});

it("does not move focus into a session hidden before Close acknowledges", async () => {
  const f = fixture({ pages: 2 });
  let release!: () => void;
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages" && args.request.action === "close")
      await new Promise<void>((resolve) => (release = resolve));
    return base(operation, args);
  });
  const view = mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/2 tab",
  });
  close.focus();
  fireEvent.click(close);
  await waitFor(() => expect(release).toBeTypeOf("function"));
  view.setActive(false);
  const unrelated = screen.getByRole("button", {
    name: "Open Files fixture tab",
  });
  unrelated.focus();
  await act(async () => release());
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(document.activeElement).toBe(unrelated);
});

it("keeps the descriptor with recovery for an unconfirmed close and settles it by observation only", async () => {
  const f = fixture({ pages: 2 });
  f.model.outcome = "cleanup-pending";
  f.model.settleOnObserve = false;
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/1 tab",
  });
  close.focus();
  fireEvent.click(close);
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("native view cleanup is not confirmed");
  // Metadata is committed but the descriptor remains until native proof.
  expect(f.model.pages).toHaveLength(1);
  expect(tabNames()).toHaveLength(3);
  expect(document.activeElement).toBe(close);
  const original = pageCalls("close")[0].operation_id;
  f.model.settleOnObserve = true;
  fireEvent.click(within(alert).getByRole("button", { name: "Check status" }));
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(pageCalls("close")).toHaveLength(1);
  expect(
    pageCalls("observe").every((request) => request.operation_id === original),
  ).toBe(true);
});

it("keeps the descriptor and shows a failure when the native close failed", async () => {
  const f = fixture({ pages: 1 });
  f.model.outcome = "fail";
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const close = screen.getByRole("button", {
    name: "Close fixture.test/1 tab",
  });
  close.focus();
  fireEvent.click(close);
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("The browser action failed");
  expect(tabNames()).toHaveLength(2);
  expect(f.model.pages).toHaveLength(1);
  expect(document.activeElement).toBe(close);
  // Dismissal clears the notice, and a later close is a fresh operation.
  fireEvent.click(within(alert).getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByRole("alert")).toBeNull();
  f.model.outcome = "complete";
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toHaveLength(1));
  const closes = pageCalls("close");
  expect(closes).toHaveLength(2);
  expect(closes[0].operation_id).not.toBe(closes[1].operation_id);
});

it("settles a lost Close reply without replaying the closure", async () => {
  const f = fixture({ pages: 2 });
  f.model.outcome = "lose-reply";
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const alert = await screen.findByRole("alert");
  expect(tabNames()).toHaveLength(3);
  fireEvent.click(within(alert).getByRole("button", { name: "Check status" }));
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(pageCalls("close")).toHaveLength(1);
  expect(pageCalls("observe").length).toBeGreaterThan(0);
});

it("closing the last page selects Conversation, keeps the profile and leaves zero pages", async () => {
  const f = fixture({ pages: 1 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
  expect(
    screen
      .getByRole("tab", { name: "Conversation" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(f.model.pages).toHaveLength(0);
  expect(f.register).not.toHaveBeenCalled();
  // Subsequent observation restores nothing and creates nothing.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 3200));
  });
  expect(tabNames()).toEqual(["Conversation"]);
  expect(pageCalls("create")).toHaveLength(0);
  expect(f.model.pages).toHaveLength(0);
});

/**
 * Mounts the workspace for the fixture session beside a second, unmounted
 * Session that shares the same tabs store, exactly like a retained inactive
 * Session whose browser manager is not polling.
 */
function mountWithRetainedSession(f: Fixture) {
  const captured: { store?: SessionTabsStore } = {};
  function Probe() {
    captured.store = useSessionTabsStore();
    return null;
  }
  render(
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <SessionTabsProvider>
            <Probe />
            <Workspace f={f} />
          </SessionTabsProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return captured as { store: SessionTabsStore };
}
const retained = "retained-session";
const pageTab = (profile: string, id: string, title = id) => ({
  kind: SessionTabKind.Page as const,
  profile,
  id,
  title,
  label: title,
});
const retainedKeys = (store: SessionTabsStore) =>
  store.snapshot(retained).tabs.map(sessionTabKey);

it("removes an acknowledged page from retained sessions, keeping foreign profiles and other pages", async () => {
  const f = fixture({ pages: 2 });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const [first, second] = f.model.pages;
  const store = captured.store;
  store.open(retained, pageTab(f.profileId, second.id));
  store.open(retained, pageTab(f.profileId, first.id));
  store.open(retained, pageTab("foreign-profile", first.id));
  // The closed page is the selected descriptor of the retained session.
  store.select(retained, sessionTabKey(pageTab(f.profileId, first.id)));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(retainedKeys(store)).toEqual([
    SessionTabKind.Conversation,
    sessionTabKey(pageTab(f.profileId, second.id)),
    sessionTabKey(pageTab("foreign-profile", first.id)),
  ]);
  expect(store.snapshot(retained).selected).toBe(
    sessionTabKey(pageTab(f.profileId, second.id)),
  );
  // One native close only; the cleanup is store-level.
  expect(pageCalls("close")).toHaveLength(1);
  expect(calls("open_browser")).toHaveLength(0);
});

it("keeps an unrelated selected tab of a retained session when its page descriptor is removed", async () => {
  const f = fixture({ pages: 1 });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const [first] = f.model.pages;
  const store = captured.store;
  store.open(retained, pageTab(f.profileId, first.id));
  store.open(retained, { kind: SessionTabKind.Diagnostics });
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
  expect(retainedKeys(store)).toEqual([
    SessionTabKind.Conversation,
    SessionTabKind.Diagnostics,
  ]);
  expect(store.snapshot(retained).selected).toBe(SessionTabKind.Diagnostics);
});

it("removes the page from retained sessions when a lost Close is observed without an admission", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_pages" && args.request.action === "close") {
      // Another owner removed the page, but this request never reached the
      // native command: no receipt exists for the original operation.
      f.model.pages = f.model.pages.filter(
        (page) => page.id !== args.request.page_id,
      );
      f.model.selected = f.model.pages[0]?.id ?? "";
      f.model.requests.push(args.request);
      throw new Error("request lost");
    }
    return base(operation, args);
  });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const [first, second] = f.model.pages;
  const store = captured.store;
  store.open(retained, pageTab(f.profileId, first.id));
  store.open(retained, pageTab(f.profileId, second.id));
  store.select(retained, sessionTabKey(pageTab(f.profileId, first.id)));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const alert = await screen.findByRole("alert");
  expect(retainedKeys(store)).toContain(
    sessionTabKey(pageTab(f.profileId, first.id)),
  );
  fireEvent.click(within(alert).getByRole("button", { name: "Check status" }));
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(retainedKeys(store)).toEqual([
    SessionTabKind.Conversation,
    sessionTabKey(pageTab(f.profileId, second.id)),
  ]);
  expect(store.snapshot(retained).selected).toBe(SessionTabKind.Conversation);
  expect(pageCalls("close")).toHaveLength(1);
});

it("cancels the Close without a native call when pagehide withdrew its hold during the barrier", async () => {
  const f = fixture({ pages: 1 });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  // The window went inactive while the barrier was pending.
  const holds = vi
    .spyOn(captured.store, "holdsPendingClose")
    .mockReturnValue(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await screen.findByRole("alert");
  expect(holds).toHaveBeenCalled();
  expect(pageCalls("close")).toHaveLength(0);
  expect(f.model.pages).toHaveLength(1);
  expect(tabNames()).toHaveLength(2);
  // A later Close is a fresh operation that proceeds once the hold stands.
  holds.mockRestore();
  fireEvent.click(
    within(screen.getByRole("alert")).getByRole("button", { name: "Dismiss" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  await waitFor(() => expect(tabNames()).toHaveLength(1));
  expect(pageCalls("close")).toHaveLength(1);
});

it("restores the hold of a sent unsettled Close after a bfcache return without resending it", async () => {
  const f = fixture({ pages: 1 });
  f.model.outcome = "cleanup-pending";
  f.model.settleOnObserve = false;
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const [first] = f.model.pages;
  const store = captured.store;
  store.open(retained, pageTab(f.profileId, first.id));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const pending = await screen.findByRole("alert");
  expect(pageCalls("close")).toHaveLength(1);
  const owned = () => store.snapshot(retained).tabs.map(sessionTabKey);
  // pagehide withdraws the hold; a persisted pageshow restores the same one.
  act(() => {
    window.dispatchEvent(new Event("pagehide"));
  });
  const restored = new Event("pageshow");
  Object.defineProperty(restored, "persisted", { value: true });
  act(() => {
    window.dispatchEvent(restored);
  });
  // The restored hold keeps the descriptor through an empty inventory.
  store.reconcilePages(retained, f.profileId, []);
  expect(owned()).toContain(sessionTabKey(pageTab(f.profileId, first.id)));
  expect(pageCalls("close")).toHaveLength(1);
  // The original proof still settles it everywhere without a second Close.
  f.model.settleOnObserve = true;
  fireEvent.click(
    within(pending).getByRole("button", { name: "Check status" }),
  );
  await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
  expect(owned()).toEqual([SessionTabKind.Conversation]);
  expect(pageCalls("close")).toHaveLength(1);
});

it("retains the descriptor in every session while a close fails or stays unconfirmed", async () => {
  const f = fixture({ pages: 1 });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  const [first] = f.model.pages;
  const store = captured.store;
  store.open(retained, pageTab(f.profileId, first.id));
  const before = store.snapshot(retained);
  f.model.outcome = "fail";
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const failed = await screen.findByRole("alert");
  expect(failed.textContent).toContain("The browser action failed");
  expect(store.snapshot(retained)).toBe(before);
  fireEvent.click(within(failed).getByRole("button", { name: "Dismiss" }));
  f.model.outcome = "cleanup-pending";
  f.model.settleOnObserve = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const pending = await screen.findByRole("alert");
  expect(pending.textContent).toContain("native view cleanup is not confirmed");
  expect(store.snapshot(retained)).toBe(before);
  // Confirmation by observation finally removes it everywhere.
  f.model.settleOnObserve = true;
  fireEvent.click(
    within(pending).getByRole("button", { name: "Check status" }),
  );
  await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
  expect(retainedKeys(store)).toEqual([SessionTabKind.Conversation]);
});

it("aligns this workspace with pages opened or closed by another owner", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const added = { id: newRequestId(), url: "https://other.test/" };
  await act(async () => {
    f.model.pages.push(added);
    f.model.pages = f.model.pages.filter(
      (page) => page.url !== "https://fixture.test/1",
    );
    f.model.selected = f.model.pages[0].id;
  });
  await waitFor(
    () =>
      expect(tabNames()).toEqual([
        "Conversation",
        "https://fixture.test/2",
        "https://other.test/",
      ]),
    { timeout: 6000 },
  );
  // Observation only aligned descriptors; it issued no mutation.
  expect(pageCalls("create")).toHaveLength(0);
  expect(pageCalls("close")).toHaveLength(0);
});

it("presents the selected page by identity, hides it when leaving and uses a new view when returning", async () => {
  const f = fixture({ pages: 1 });
  const view = mount(f, true);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const first = calls("open_browser")[0][1];
  expect(first).toMatchObject({
    profileId: f.profileId,
    pageId: f.model.pages[0].id,
  });
  expect(first).not.toHaveProperty("url");
  expect(view.container.querySelector("iframe,webview,script,a")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      "control_browser",
      expect.objectContaining({ action: "hide", viewId: first.viewId }),
    ),
  );
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  expect(calls("open_browser")[1][1].viewId).not.toBe(first.viewId);
  view.unmount();
  await waitFor(() =>
    expect(
      native.mock.calls.some(
        ([, args]) =>
          args.action === "hide" &&
          args.viewId === calls("open_browser")[1][1].viewId,
      ),
    ).toBe(true),
  );
});

it("retains the exact view after failed hide and blocks reopening until closure", async () => {
  const f = fixture({ pages: 1 });
  const base = native.getMockImplementation()!;
  let attempts = 0,
    finish!: () => void;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "control_browser" && args.action === "hide") {
      if (++attempts === 1) throw new Error("busy");
      if (attempts === 2)
        await new Promise<void>((resolve) => (finish = resolve));
    }
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const original = calls("open_browser")[0][1];
  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await screen.findByText(
    "The browser view could not be hidden. Retrying native closure.",
  );
  await waitFor(() => expect(attempts).toBe(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await act(async () => {
    fireEvent.resize(window);
  });
  expect(calls("open_browser")).toHaveLength(1);
  expect(
    native.mock.calls
      .filter(([, args]) => args.action === "hide")
      .every(([, args]) => args.viewId === original.viewId),
  ).toBe(true);
  await act(async () => finish());
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  expect(calls("open_browser")[1][1].viewId).not.toBe(original.viewId);
});

it("retries identical geometry after a failed native resize and caches only success", async () => {
  const f = fixture({ pages: 1 });
  const base = native.getMockImplementation()!;
  let top = 100,
    attempts = 0;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    () => ({
      x: 100,
      y: top,
      left: 100,
      top,
      width: 300,
      height: 200,
      right: 400,
      bottom: top + 200,
      toJSON: () => ({}),
    }),
  );
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "resize" &&
      ++attempts === 1
    )
      throw new Error("transient native resize failure");
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  top = 125;
  fireEvent.resize(window);
  await screen.findByText("The browser view could not be updated.");
  fireEvent.resize(window);
  await waitFor(() => expect(attempts).toBe(2));
  const resizes = native.mock.calls.filter(
    ([, args]) => args.action === "resize",
  );
  expect(resizes[1][1]).toEqual(resizes[0][1]);
  await act(async () => {
    fireEvent.resize(window);
  });
  expect(attempts).toBe(2);
});

it("surfaces a delayed native child failure and reopens only after explicit retry", async () => {
  const f = fixture({ pages: 1 });
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "browser_state") throw new Error("sidecar-failed");
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const first = calls("open_browser")[0][1];
  await screen.findByText(
    "The local browser state is unavailable.",
    {},
    { timeout: 2500 },
  );
  expect(calls("open_browser")).toHaveLength(1);
  const summary = screen.getByLabelText("More browser actions");
  fireEvent.click(summary);
  fireEvent.click(screen.getByRole("button", { name: "Retry native view" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  expect(calls("open_browser")[1][1].viewId).not.toBe(first.viewId);
});

it("hides only for a visible modal and reopens afterwards", async () => {
  const f = fixture({ pages: 1 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const first = calls("open_browser")[0][1];
  const modal = document.createElement("dialog");
  modal.setAttribute("open", "");
  try {
    await act(async () => {
      document.body.append(modal);
    });
    await waitFor(() =>
      expect(native).toHaveBeenCalledWith(
        "control_browser",
        expect.objectContaining({ action: "hide", viewId: first.viewId }),
      ),
    );
    await act(async () => {
      modal.remove();
    });
    await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  } finally {
    modal.remove();
  }
});

it("opens the information modal over the retained page without registering again", async () => {
  const f = fixture({ pages: 1 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const information = screen.getByRole("button", {
    name: "Browser information",
  });
  information.focus();
  fireEvent.click(information);
  const dialog = screen.getByRole("dialog", { name: "Browser information" });
  expect(dialog.textContent).toContain(
    "Tabs, cookies, history and browser credentials stay local.",
  );
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      "control_browser",
      expect.objectContaining({
        action: "hide",
        viewId: calls("open_browser")[0][1].viewId,
      }),
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Close Browser information" }),
  );
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  expect(f.register).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(information);
});

it("keeps navigation commands on the original profile and view and exposes no page picker or pane close", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const original = calls("open_browser")[0][1];
  // The pane has no inner page row, + control or header close.
  expect(screen.queryByRole("button", { name: "New tab" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Close browser" })).toBeNull();
  expect(
    screen.getAllByRole("button", { name: /^Close .* tab$/ }),
  ).toHaveLength(2);
  for (const name of ["Back", "Forward", "Reload"]) {
    fireEvent.click(screen.getByRole("button", { name }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name })).toHaveProperty(
        "disabled",
        false,
      ),
    );
  }
  fireEvent.change(
    screen.getAllByRole("textbox", { name: "Address" }).at(-1)!,
    {
      target: { value: "https://fixture.test/explicit" },
    },
  );
  fireEvent.click(screen.getByRole("button", { name: "Go" }));
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      "control_browser",
      expect.objectContaining({
        action: "navigate",
        profileId: f.profileId,
        viewId: original.viewId,
        url: "https://fixture.test/explicit",
      }),
    ),
  );
  const controls = native.mock.calls.filter(
    ([operation, args]) =>
      operation === "control_browser" &&
      !["resize", "hide"].includes(args.action),
  );
  expect(controls.map(([, args]) => args.action)).toEqual([
    "back",
    "forward",
    "reload",
    "navigate",
  ]);
  expect(
    controls.every(
      ([, args]) =>
        args.profileId === f.profileId && args.viewId === original.viewId,
    ),
  ).toBe(true);
  expect(f.register).not.toHaveBeenCalled();
});

it("reconciles only the latest shared selection once after a busy action settles", async () => {
  const f = fixture({ pages: 3 });
  const base = native.getMockImplementation()!;
  let release!: () => void;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "control_browser" && args.action === "reload")
      await new Promise<void>((resolve) => (release = resolve));
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  fireEvent.click(screen.getByRole("button", { name: "Reload" }));
  await waitFor(() => expect(release).toBeTypeOf("function"));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/3" }));
  expect(selects()).toHaveLength(0);
  await act(async () => release());
  await waitFor(() => expect(selects()).toHaveLength(1));
  expect(selects()[0][1]).toMatchObject({ tabId: f.model.pages[2].id });
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(1);
});

it("keeps newer shared page C and reconciles native C after deferred B selection", async () => {
  const f = fixture({ pages: 3 });
  const base = native.getMockImplementation()!;
  const pendingB = deferred<void>();
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "select-tab" &&
      args.tabId === f.model.pages[1].id
    )
      await pendingB.promise;
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(1));
  expect(selects()[0][1]).toMatchObject({ tabId: f.model.pages[1].id });
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/3" }));
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/3" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(selects()).toHaveLength(1);
  await act(async () => pendingB.resolve());
  await waitFor(() => expect(selects()).toHaveLength(2));
  expect(selects()[1][1]).toMatchObject({ tabId: f.model.pages[2].id });
  await waitFor(() => expect(f.model.selected).toBe(f.model.pages[2].id));
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/3" })
      .getAttribute("aria-selected"),
  ).toBe("true");
});

it("does not reopen B after selecting Conversation during deferred B reconciliation", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  const pendingB = deferred<void>();
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "select-tab" &&
      args.tabId === f.model.pages[1].id
    )
      await pendingB.promise;
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(1));
  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await act(async () => pendingB.resolve());
  await waitFor(() =>
    expect(native).toHaveBeenCalledWith(
      "control_browser",
      expect.objectContaining({ action: "hide" }),
    ),
  );
  expect(selects()).toHaveLength(1);
  expect(
    screen
      .getByRole("tab", { name: "Conversation" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(calls("open_browser")).toHaveLength(1);
});

it("completes deferred reconciliation without another request when B stays selected", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  const pendingB = deferred<void>();
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "select-tab" &&
      args.tabId === f.model.pages[1].id
    )
      await pendingB.promise;
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(1));
  await act(async () => pendingB.resolve());
  await waitFor(() => expect(f.model.selected).toBe(f.model.pages[1].id));
  expect(selects()).toHaveLength(1);
  expect(
    screen
      .getByRole("tab", { name: "https://fixture.test/2" })
      .getAttribute("aria-selected"),
  ).toBe("true");
});

it("defers shared selection until the pending native open completes", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  let release!: () => void;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "open_browser")
      await new Promise<void>((resolve) => (release = resolve));
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  // The opened page is already the native selection, so completion leaves the
  // observed selection unchanged.
  f.model.selected = f.model.pages[0].id;
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(release).toBeTypeOf("function"));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(0);
  await act(async () => release());
  await waitFor(() => expect(selects()).toHaveLength(1));
  expect(selects()[0][1]).toMatchObject({ tabId: f.model.pages[1].id });
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(1);
});

it("suppresses an unchanged failure but retries after selecting the native page and returning", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "control_browser" && args.action === "select-tab")
      throw new Error("rejected");
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const selects = () =>
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action === "select-tab",
    );
  await waitFor(() => expect(selects()).toHaveLength(1));
  await screen.findByText(
    "The browser action failed. Check its address and the current server and account authorization.",
  );
  await new Promise((resolve) => setTimeout(resolve, 80));
  expect(selects()).toHaveLength(1);
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(2));
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(2);
});

it("retries a failed page selection once after Conversation and a new native view", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  let fail = true;
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "select-tab" &&
      fail
    ) {
      fail = false;
      throw new Error("rejected");
    }
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const selects = () =>
    calls("control_browser").filter(([, args]) => args.action === "select-tab");
  await waitFor(() => expect(selects()).toHaveLength(1));
  await screen.findByText(
    "The browser action failed. Check its address and the current server and account authorization.",
  );
  expect(f.model.selected).toBe(f.model.pages[0].id);

  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await waitFor(() =>
    expect(
      calls("control_browser").some(([, args]) => args.action === "hide"),
    ).toBe(true),
  );
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  await waitFor(() => expect(selects()).toHaveLength(2));
  expect(selects().map(([, args]) => args.tabId)).toEqual([
    f.model.pages[1].id,
    f.model.pages[1].id,
  ]);
  await waitFor(() => expect(f.model.selected).toBe(f.model.pages[1].id));
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(2);
});

it("waits for a replacement native view before retrying a pending selection", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  let releaseHide!: () => void;
  let hideStarted = false;
  native.mockImplementation(async (operation: string, args: any) => {
    if (
      operation === "control_browser" &&
      args.action === "hide" &&
      !hideStarted
    ) {
      hideStarted = true;
      await new Promise<void>((resolve) => (releaseHide = resolve));
    }
    if (operation === "control_browser" && args.action === "select-tab")
      throw new Error("rejected");
    return base(operation, args);
  });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  const first = calls("open_browser")[0][1];
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const selects = () =>
    calls("control_browser").filter(([, args]) => args.action === "select-tab");
  await waitFor(() => expect(selects()).toHaveLength(1));
  await screen.findByText(
    "The browser action failed. Check its address and the current server and account authorization.",
  );

  fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
  await waitFor(() => expect(releaseHide).toBeTypeOf("function"));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(2));
  expect(selects()[1][1].viewId).toBe(first.viewId);

  await act(async () => releaseHide());
  await waitFor(() => expect(calls("open_browser")).toHaveLength(2));
  await waitFor(() => expect(selects()).toHaveLength(3));
  expect(selects()[2][1].viewId).toBe(calls("open_browser")[1][1].viewId);
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(3);
});

it("retries a failed page selection after leaving for another non-page tab", async () => {
  const f = fixture({ pages: 2 });
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    if (operation === "control_browser" && args.action === "select-tab")
      throw new Error("rejected");
    return base(operation, args);
  });
  const captured = mountWithRetainedSession(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/1" }));
  await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  const selects = () =>
    calls("control_browser").filter(([, args]) => args.action === "select-tab");
  await waitFor(() => expect(selects()).toHaveLength(1));
  await screen.findByText(
    "The browser action failed. Check its address and the current server and account authorization.",
  );

  act(() =>
    captured.store?.open(f.session.id, { kind: SessionTabKind.Diagnostics }),
  );
  await waitFor(() =>
    expect(
      calls("control_browser").some(([, args]) => args.action === "hide"),
    ).toBe(true),
  );
  expect(selects()).toHaveLength(1);
  fireEvent.click(screen.getByRole("tab", { name: "https://fixture.test/2" }));
  await waitFor(() => expect(selects()).toHaveLength(2));
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(selects()).toHaveLength(2);
});

it("refreshes owned shared labels from observed URLs without selecting or navigating", async () => {
  const f = fixture({ pages: 2 });
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  const selected = screen
    .getByRole("tab", { name: "Conversation" })
    .getAttribute("aria-selected");
  await act(async () => {
    f.model.pages[0] = {
      ...f.model.pages[0],
      url: "https://redirect.test/landed",
    };
  });
  await waitFor(
    () => expect(tabNames()[1]).toBe("https://redirect.test/landed"),
    {
      timeout: 6000,
    },
  );
  expect(
    screen.getByRole("tab", { name: "https://redirect.test/landed" })
      .textContent,
  ).toBe("redirect.test/landed");
  expect(
    screen
      .getByRole("tab", { name: "Conversation" })
      .getAttribute("aria-selected"),
  ).toBe(selected);
  expect(
    native.mock.calls.filter(
      ([operation, args]) =>
        operation === "control_browser" && args.action !== "hide",
    ),
  ).toHaveLength(0);
});

it("sends no page operation or presentation while the session is inactive", async () => {
  const f = fixture({ pages: 1 });
  mount(f, false, false);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 100));
  });
  expect(native).not.toHaveBeenCalled();
  expect(f.list).not.toHaveBeenCalled();
  expect(tabNames()).toEqual(["Conversation"]);
});

// Removal convergence waits on the one-second inventory poll.
const SLOW_TEST = 15000;
const EMPTY_PENDING_STATE = {
  tabs: { tabs: [], selected: "" },
  removal_pending: true,
};
/** Mutating browser_pages actions and presentation commands the host receives. */
const nativeMutations = () =>
  native.mock.calls.filter(
    ([name, args]) =>
      (name === "browser_pages" &&
        ["create", "close"].includes(args.request.action)) ||
      name === "open_browser" ||
      name === "control_browser",
  );
/** Rewrites every Observe reply so tests can pair receipts with other state. */
function rewriteObserve(
  rewrite: (reply: { state: any; receipt: Receipt }) => unknown,
) {
  const base = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) => {
    const reply = await base(operation, args);
    if (
      operation === "browser_pages" &&
      args.request.action === "observe" &&
      reply.receipt
    )
      return rewrite(reply);
    return reply;
  });
}
const settle = (milliseconds = 60) =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, milliseconds));
  });

it(
  "drops stale Page descriptors on a removal-pending inventory and keeps unrelated tabs",
  async () => {
    const f = fixture({ pages: 2 });
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(3));
    fireEvent.click(
      screen.getByRole("button", { name: "Open Files fixture tab" }),
    );
    await waitFor(() => expect(tabNames()).toHaveLength(4));
    const mutations = nativeMutations().length;
    await act(async () => {
      f.model.removalPending = true;
    });
    await waitFor(() => expect(tabNames()).toHaveLength(2), { timeout: 6000 });
    expect(tabNames()).not.toContain("https://fixture.test/1");
    expect(tabNames()).not.toContain("https://fixture.test/2");
    expect(tabNames()).toContain("Conversation");
    // Repeated observation is idempotent and sends no native mutation.
    const settled = tabNames();
    await settle(2200);
    expect(tabNames()).toEqual(settled);
    expect(pageCalls("create")).toHaveLength(0);
    expect(pageCalls("close")).toHaveLength(0);
    expect(nativeMutations()).toHaveLength(mutations);
  },
  SLOW_TEST,
);

it(
  "falls back to Conversation when the selected Page is dropped by a removal-pending inventory",
  async () => {
    const f = fixture({ pages: 1 });
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    fireEvent.click(
      screen.getByRole("tab", { name: "https://fixture.test/1" }),
    );
    await waitFor(() => expect(calls("open_browser")).toHaveLength(1));
    await act(async () => {
      f.model.removalPending = true;
    });
    await waitFor(() => expect(tabNames()).toEqual(["Conversation"]), {
      timeout: 6000,
    });
    expect(
      screen
        .getByRole("tab", { name: "Conversation" })
        .getAttribute("aria-selected"),
    ).toBe("true");
    expect(pageCalls("close")).toHaveLength(0);
  },
  SLOW_TEST,
);

it(
  "converges two sessions of one account when the profile is removed",
  async () => {
    const f = fixture({ pages: 1 });
    const other = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      revision: 7n,
    });
    const second = { ...f, session: other } as Fixture;
    render(
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <SessionTabsProvider>
              <Workspace f={f} />
              <Workspace f={second} />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>,
    );
    await waitFor(() =>
      expect(
        screen.getAllByRole("tab", { name: "https://fixture.test/1" }),
      ).toHaveLength(2),
    );
    await act(async () => {
      f.model.removalPending = true;
    });
    await waitFor(
      () =>
        expect(
          screen.queryAllByRole("tab", { name: "https://fixture.test/1" }),
        ).toHaveLength(0),
      { timeout: 6000 },
    );
    expect(pageCalls("close")).toHaveLength(0);
  },
  SLOW_TEST,
);

it(
  "drops unavailable profile pages from an inactive retained session without polling it or presenting on revisit",
  async () => {
    const f = fixture({ pages: 2 });
    const other = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      revision: 9n,
    });
    const second = { ...f, session: other } as Fixture;
    const captured: { store?: SessionTabsStore } = {};
    function Probe() {
      captured.store = useSessionTabsStore();
      return null;
    }
    const tree = (inactiveVisible: boolean) => (
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <SessionTabsProvider>
              <Probe />
              <Workspace f={f} />
              <Workspace f={second} active={inactiveVisible} />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
    const view = render(tree(false));
    // Session A discovers its pages while Session B stays inactive.
    await waitFor(() =>
      expect(
        screen.getAllByRole("tab", { name: "https://fixture.test/1" }),
      ).toHaveLength(1),
    );
    const store = captured.store!;
    const [first, secondPage] = f.model.pages;
    // Session B retains stale descriptors, a foreign profile page with the same
    // id and an unrelated tab; its selected Page is the one that goes away.
    store.open(other.id, pageTab(f.profileId, first.id));
    store.open(other.id, pageTab(f.profileId, secondPage.id));
    store.open(other.id, pageTab("foreign-profile", first.id));
    store.open(other.id, { kind: SessionTabKind.Diagnostics });
    store.select(other.id, sessionTabKey(pageTab(f.profileId, secondPage.id)));
    const mutations = nativeMutations().length;
    await act(async () => {
      f.model.removalPending = true;
    });
    await waitFor(
      () =>
        expect(store.snapshot(other.id).tabs.map(sessionTabKey)).toEqual([
          SessionTabKind.Conversation,
          sessionTabKey(pageTab("foreign-profile", first.id)),
          SessionTabKind.Diagnostics,
        ]),
      { timeout: 6000 },
    );
    // The removed selected Page falls back to its left neighbor, here the
    // Conversation tab, because the earlier Page descriptors are gone too.
    expect(store.snapshot(other.id).selected).toBe(SessionTabKind.Conversation);
    // Repeating the authoritative inventory publishes nothing for B.
    const settled = store.snapshot(other.id);
    await settle(2200);
    expect(store.snapshot(other.id)).toBe(settled);
    // Revisiting B cannot present a removed stale descriptor.
    view.rerender(tree(true));
    await settle(300);
    expect(calls("open_browser")).toHaveLength(0);
    expect(nativeMutations()).toHaveLength(mutations);
  },
  SLOW_TEST,
);

it(
  "preserves the descriptor of an in-flight Close until its original proof settles under removal",
  async () => {
    const f = fixture({ pages: 2 });
    f.model.outcome = "cleanup-pending";
    f.model.settleOnObserve = false;
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(3));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    await act(async () => {
      f.model.removalPending = true;
    });
    await settle(1500);
    // The owned descriptor survives while the sibling page is dropped.
    expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]);
    f.model.settleOnObserve = true;
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
    expect(pageCalls("close")).toHaveLength(1);
  },
  SLOW_TEST,
);

it(
  "keeps the descriptor of another Session's unsettled Close under removal until its proof settles",
  async () => {
    const f = fixture({ pages: 2 });
    f.model.outcome = "cleanup-pending";
    f.model.settleOnObserve = false;
    const other = create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SESSION,
      revision: 3n,
    });
    function OtherPages() {
      const snapshot = useSessionTabs(other.id);
      return (
        <output data-testid="other-pages">
          {snapshot.tabs
            .filter((tab) => tab.kind === SessionTabKind.Page)
            .map((tab) => tab.id)
            .join(",")}
        </output>
      );
    }
    render(
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <MutationIntents>
            <SessionTabsProvider>
              <Workspace f={f} />
              <SessionBrowser session={other} accountId={f.accountId} />
              <OtherPages />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>,
    );
    const [closing, sibling] = f.model.pages.map((page) => page.id);
    const otherPages = () => screen.getByTestId("other-pages").textContent;
    await waitFor(() => expect(otherPages()).toBe(`${closing},${sibling}`));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    await act(async () => {
      f.model.removalPending = true;
    });
    // The other Session polls the empty inventory and drops the sibling, but
    // keeps the page whose original Close is still unsettled.
    await waitFor(() => expect(otherPages()).toBe(closing), { timeout: 6000 });
    await settle(3500);
    expect(otherPages()).toBe(closing);
    f.model.settleOnObserve = true;
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await waitFor(() => expect(otherPages()).toBe(""));
    expect(pageCalls("close")).toHaveLength(1);
  },
  SLOW_TEST,
);

it(
  "settles a lost Close reply observed after the profile began removal without replaying it",
  async () => {
    const f = fixture({ pages: 2 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(3));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    await act(async () => {
      f.model.removalPending = true;
    });
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await waitFor(() => expect(tabNames()).toEqual(["Conversation"]));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(pageCalls("close")).toHaveLength(1);
    // Polling stopped: nothing further is observed once the operation settled.
    const observed = pageCalls("observe").length;
    await settle(2200);
    expect(pageCalls("observe").length).toBe(observed);
    expect(pageCalls("close")).toHaveLength(1);
  },
  SLOW_TEST,
);

it(
  "settles a lost Create reply observed under removal without reviving a page or creating again",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    const dialog = await openDialog();
    await submit(dialog, "https://fixture.test/lost");
    const check = await within(dialog).findByRole("button", {
      name: "Check status",
    });
    await act(async () => {
      f.model.removalPending = true;
    });
    fireEvent.click(check);
    await waitFor(() =>
      expect(
        within(dialog).queryByRole("button", { name: "Check status" }),
      ).toBeNull(),
    );
    expect(screen.queryByText("https://fixture.test/lost")).toBeNull();
    expect(tabNames()).toEqual(["Conversation"]);
    expect(pageCalls("create")).toHaveLength(1);
    expect(calls("open_browser")).toHaveLength(0);
    // A new Create stays denied while the profile is being removed.
    fireEvent.change(within(dialog).getByRole("textbox", { name: "Address" }), {
      target: { value: "https://fixture.test/again" },
    });
    expect(
      within(dialog).getByRole("button", { name: "Open page" }),
    ).toHaveProperty("disabled", true);
    await settle(1500);
    expect(pageCalls("create")).toHaveLength(1);
  },
  SLOW_TEST,
);

it(
  "treats a nonempty removal-pending Create inventory as empty without reviving the created page",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    const dialog = await openDialog();
    await submit(dialog, "https://fixture.test/raced");
    const check = await within(dialog).findByRole("button", {
      name: "Check status",
    });
    // The host can answer a committed Create that raced with removal with the
    // loaded tabs, including the new page, next to removal_pending.
    const base = native.getMockImplementation()!;
    native.mockImplementation(async (operation: string, args: any) => {
      const reply = await base(operation, args);
      return operation === "browser_pages" &&
        ["observe", "inventory"].includes(args.request.action)
        ? { ...reply, state: { ...reply.state, removal_pending: true } }
        : reply;
    });
    fireEvent.click(check);
    await waitFor(() =>
      expect(
        within(dialog).queryByRole("button", { name: "Check status" }),
      ).toBeNull(),
    );
    expect(screen.queryByText("https://fixture.test/raced")).toBeNull();
    expect(tabNames()).toEqual(["Conversation"]);
    expect(pageCalls("create")).toHaveLength(1);
    expect(calls("open_browser")).toHaveLength(0);
    expect(
      nativeMutations().filter(([name]) => name !== "browser_pages"),
    ).toHaveLength(0);
  },
  SLOW_TEST,
);

const invalidProofs: [string, (receipt: Receipt) => Receipt][] = [
  [
    "native cleanup incomplete",
    (receipt) => ({ ...receipt, native_cleanup_complete: false }),
  ],
  [
    "metadata not committed",
    (receipt) => ({ ...receipt, metadata_committed: false }),
  ],
  [
    "a recorded failure",
    (receipt) => ({ ...receipt, failure: "storage-unavailable" }),
  ],
  [
    "a foreign operation",
    (receipt) => ({ ...receipt, operation_id: newRequestId() }),
  ],
];
it.each(invalidProofs)(
  "keeps a removal-pending Close unsettled when the receipt reports %s",
  async (_name, tamper) => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    f.model.removalPending = true;
    rewriteObserve((reply) => ({
      state: EMPTY_PENDING_STATE,
      receipt: tamper(reply.receipt),
    }));
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await waitFor(() => expect(pageCalls("observe").length).toBeGreaterThan(0));
    await settle(1500);
    expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]);
    expect(screen.getByRole("button", { name: "Check status" })).toBeTruthy();
    expect(pageCalls("close")).toHaveLength(1);
  },
  SLOW_TEST,
);

it.each(invalidProofs)(
  "still drops sibling descriptors under removal when the Close receipt reports %s",
  async (_name, tamper) => {
    const f = fixture({ pages: 2 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(3));
    const loaded = f.model.pages.map((page) => ({ ...page }));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    f.model.removalPending = true;
    // The host replies with the loaded tabs next to the rejected receipt.
    rewriteObserve((reply) => ({
      state: {
        tabs: { tabs: loaded, selected: loaded[1].id },
        removal_pending: true,
      },
      receipt: tamper(reply.receipt),
    }));
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    // The independently validated pending inventory reconciles the sibling
    // while the closing page stays protected and its operation unsettled.
    await waitFor(() =>
      expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]),
    );
    await settle(1500);
    expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]);
    expect(screen.getByRole("button", { name: "Check status" })).toBeTruthy();
    expect(pageCalls("close")).toHaveLength(1);
    expect(pageCalls("create")).toHaveLength(0);
  },
  SLOW_TEST,
);

it(
  "does not settle a Close from a removal-pending reply that still lists the page",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    const loaded = f.model.pages.map((page) => ({ ...page }));
    const settled = vi.spyOn(
      SessionTabsStoreImpl.prototype,
      "closePageEverywhere",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    f.model.removalPending = true;
    // No admission is found, and the host still lists the page next to
    // removal_pending: the normalized empty inventory must not prove absence.
    rewriteObserve(() => ({
      state: {
        tabs: { tabs: loaded, selected: loaded[0].id },
        removal_pending: true,
      },
      receipt: null,
    }));
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await waitFor(() => expect(pageCalls("observe").length).toBeGreaterThan(0));
    await settle(500);
    expect(settled).not.toHaveBeenCalled();
    settled.mockRestore();
  },
  SLOW_TEST,
);

it(
  "rejects a removal-pending Close receipt for a different page",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    f.model.removalPending = true;
    rewriteObserve((reply) => ({
      state: EMPTY_PENDING_STATE,
      receipt: { ...reply.receipt, page_id: newRequestId() },
    }));
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await settle(1500);
    expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]);
    expect(screen.getByRole("button", { name: "Check status" })).toBeTruthy();
  },
  SLOW_TEST,
);

it(
  "still requires Create presence and Close absence while the profile is available",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    const dialog = await openDialog();
    await submit(dialog, "https://fixture.test/lost");
    const check = await within(dialog).findByRole("button", {
      name: "Check status",
    });
    // An available inventory that lacks the created page contradicts the proof.
    rewriteObserve((reply) => ({
      state: { tabs: { tabs: [], selected: "" }, removal_pending: false },
      receipt: reply.receipt,
    }));
    fireEvent.click(check);
    await waitFor(() => expect(pageCalls("observe").length).toBeGreaterThan(0));
    await settle(1500);
    expect(
      within(dialog).getByRole("button", { name: "Check status" }),
    ).toBeTruthy();
    expect(pageCalls("create")).toHaveLength(1);
  },
  SLOW_TEST,
);

it(
  "still rejects a Close receipt while the closed page remains listed",
  async () => {
    const f = fixture({ pages: 1 });
    f.model.outcome = "lose-reply";
    mount(f);
    await waitFor(() => expect(tabNames()).toHaveLength(2));
    fireEvent.click(
      screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
    );
    const alert = await screen.findByRole("alert");
    rewriteObserve((reply) => ({
      state: {
        tabs: {
          tabs: [{ id: reply.receipt.page_id, url: "https://fixture.test/1" }],
          selected: reply.receipt.page_id,
        },
        removal_pending: false,
      },
      receipt: reply.receipt,
    }));
    fireEvent.click(
      within(alert).getByRole("button", { name: "Check status" }),
    );
    await settle(1500);
    expect(tabNames()).toEqual(["Conversation", "https://fixture.test/1"]);
    expect(screen.getByRole("button", { name: "Check status" })).toBeTruthy();
    expect(pageCalls("close")).toHaveLength(1);
  },
  SLOW_TEST,
);

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
}
type ProfileList = (request: {
  pageToken: string;
}) => Promise<{ profiles: unknown[]; nextPageToken: string }>;
const setList = (f: Fixture, implementation: ProfileList) =>
  (
    f.list as unknown as { mockImplementation: (value: ProfileList) => void }
  ).mockImplementation(implementation);
/** Intercepts only the native inventory read; everything else is unchanged. */
function interceptInventory(read: () => Promise<unknown>) {
  const original = native.getMockImplementation()!;
  native.mockImplementation(async (operation: string, args: any) =>
    operation === "browser_pages" && args.request.action === "inventory"
      ? read()
      : original(operation, args),
  );
}
const typeAddress = (dialog: HTMLElement, address: string) =>
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Address" }), {
    target: { value: address },
  });
const openButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", {
    name: "Open page",
  }) as HTMLButtonElement;

it("keeps Open page closed while the first profile page is pending, then registers and creates once when absence is confirmed", async () => {
  const f = fixture({ registered: false });
  const first = deferred<{ profiles: unknown[]; nextPageToken: string }>();
  setList(f, () => first.promise);
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/absent");
  await waitFor(() => expect(f.list).toHaveBeenCalledTimes(1));
  expect(openButton(dialog).disabled).toBe(true);
  expect(
    within(dialog).getByText("Checking this account's browser profile…"),
  ).toBeTruthy();
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
  first.resolve({ profiles: [], nextPageToken: "" });
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  expect(f.register).not.toHaveBeenCalled();
  // A double submission neither registers nor creates twice.
  fireEvent.submit(dialog.querySelector("form")!);
  fireEvent.submit(dialog.querySelector("form")!);
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(f.register).toHaveBeenCalledTimes(1);
  expect(pageCalls("create")).toHaveLength(1);
});

it("does not treat an incomplete walk as absence and reuses a profile found on a later page", async () => {
  const f = fixture({ registered: false });
  const second = deferred<{ profiles: unknown[]; nextPageToken: string }>();
  setList(f, async ({ pageToken }) =>
    pageToken ? second.promise : { profiles: [], nextPageToken: "next" },
  );
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/later");
  await waitFor(() => expect(f.list).toHaveBeenCalledTimes(2));
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
  second.resolve({ profiles: [f.profile], nextPageToken: "" });
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  fireEvent.click(openButton(dialog));
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(1);
});

it("blocks admission on a profile read failure and retries only the profile read", async () => {
  const f = fixture({ registered: false });
  let failing = true;
  setList(f, async () => {
    if (failing) throw new ConnectError("down", Code.Unavailable);
    return { profiles: [], nextPageToken: "" };
  });
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/retry");
  const retry = await within(dialog).findByRole("button", {
    name: "Retry profile read",
  });
  expect(
    within(dialog).getByText(/could not be confirmed/).textContent,
  ).toContain("nothing is registered or opened");
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
  failing = false;
  fireEvent.click(retry);
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  expect(
    within(dialog).queryByRole("button", { name: "Retry profile read" }),
  ).toBeNull();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
});

it("treats a repeated profile page token as an invalid proof that stays closed", async () => {
  const f = fixture({ registered: false });
  setList(f, async () => ({ profiles: [], nextPageToken: "same" }));
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/loop");
  await within(dialog).findByRole("button", { name: "Retry profile read" });
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
});

it("keeps Create closed for a discovered profile until its inventory is observed", async () => {
  const f = fixture({ pages: 16 });
  const inventory = deferred<unknown>();
  interceptInventory(() => inventory.promise);
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/seventeenth");
  await waitFor(() => expect(f.list).toHaveBeenCalled());
  await settle();
  expect(openButton(dialog).disabled).toBe(true);
  expect(
    within(dialog).getByText("Reading this profile's open pages…"),
  ).toBeTruthy();
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
  inventory.resolve({ state: f.state(), receipt: null });
  await waitFor(() => expect(tabNames()).toHaveLength(17));
  // A full inventory keeps creation closed.
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
});

it("shows an inventory read failure in the dialog and enables creation after a successful retry", async () => {
  const f = fixture({ pages: 2 });
  let failing = true;
  const original = native.getMockImplementation()!;
  interceptInventory(async () => {
    if (failing) throw new Error("inventory unavailable");
    return original("browser_pages", {
      request: { action: "inventory" },
    });
  });
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/third");
  const retry = await within(dialog).findByRole("button", {
    name: "Retry page inventory read",
  });
  expect(
    within(dialog).getByText("The local browser state is unavailable."),
  ).toBeTruthy();
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
  failing = false;
  fireEvent.click(retry);
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  expect(f.register).not.toHaveBeenCalled();
  fireEvent.click(openButton(dialog));
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  expect(pageCalls("create")).toHaveLength(1);
});

it("does not require a nonexistent profile's inventory and sends nothing on open or dismissal", async () => {
  const f = fixture({ registered: false });
  mount(f);
  const dialog = await openDialog();
  await waitFor(() => expect(f.list).toHaveBeenCalled());
  await settle();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(f.register).not.toHaveBeenCalled();
  expect(native).not.toHaveBeenCalled();
  const again = await openDialog();
  await submit(again, "https://fixture.test/once");
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(f.register).toHaveBeenCalledTimes(1);
  expect(pageCalls("create")).toHaveLength(1);
});

it("rejects absence once a token follows the 64th profile page", async () => {
  const f = fixture({ registered: false });
  setList(f, async ({ pageToken }) => ({
    profiles: [],
    nextPageToken: `page-${Number(pageToken.slice(5) || "0") + 1}`,
  }));
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/bounded");
  await within(dialog).findByRole(
    "button",
    { name: "Retry profile read" },
    { timeout: 5000 },
  );
  // Page 64 returned a token, so page 65 is never requested.
  expect(f.list).toHaveBeenCalledTimes(64);
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
}, 15000);

it("accepts a terminal 64th profile page as confirmed absence", async () => {
  const f = fixture({ registered: false });
  setList(f, async ({ pageToken }) => {
    const index = Number(pageToken.slice(5) || "0") + 1;
    return { profiles: [], nextPageToken: index < 64 ? `page-${index}` : "" };
  });
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/last");
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false), {
    timeout: 5000,
  });
  expect(f.list).toHaveBeenCalledTimes(64);
}, 15000);

const siblingProfile = (f: Fixture) =>
  create(BrowserProfileSchema, {
    id: newRequestId(),
    revision: 1n,
    serverId: newRequestId(),
    deviceId: newRequestId(),
    accountId: f.accountId,
    state: BrowserProfileState.ACTIVE,
  });

it("keeps a found profile a candidate until the terminal page confirms it is unique", async () => {
  const f = fixture({ pages: 1 });
  const second = deferred<{ profiles: unknown[]; nextPageToken: string }>();
  setList(f, async ({ pageToken }) =>
    pageToken
      ? second.promise
      : { profiles: [f.profile], nextPageToken: "more" },
  );
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/candidate");
  await waitFor(() => expect(f.list).toHaveBeenCalledTimes(2));
  await settle();
  // The candidate alone never admits Create or starts inventory reads.
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
  expect(native).not.toHaveBeenCalled();
  second.resolve({ profiles: [], nextPageToken: "" });
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  fireEvent.click(openButton(dialog));
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(1);
});

it("fails closed when a later page holds a second matching profile", async () => {
  const f = fixture({ pages: 1 });
  setList(f, async ({ pageToken }) =>
    pageToken
      ? { profiles: [siblingProfile(f)], nextPageToken: "" }
      : { profiles: [f.profile], nextPageToken: "more" },
  );
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/duplicate");
  await within(dialog).findByRole("button", { name: "Retry profile read" });
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
  expect(native).not.toHaveBeenCalled();
});

it("revokes admission when a later inventory read fails until a retry succeeds", async () => {
  const f = fixture({ pages: 2 });
  const original = native.getMockImplementation()!;
  let failing = false;
  interceptInventory(async () => {
    if (failing) throw new Error("inventory unavailable");
    return original("browser_pages", { request: { action: "inventory" } });
  });
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/stale");
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  failing = true;
  // The periodic refresh fails after an earlier successful read.
  const retry = await within(dialog).findByRole(
    "button",
    { name: "Retry page inventory read" },
    { timeout: 6000 },
  );
  expect(openButton(dialog).disabled).toBe(true);
  fireEvent.submit(dialog.querySelector("form")!);
  await settle();
  expect(pageCalls("create")).toHaveLength(0);
  // A failed retry keeps the proof revoked.
  fireEvent.click(retry);
  await settle();
  expect(openButton(dialog).disabled).toBe(true);
  expect(pageCalls("create")).toHaveLength(0);
  failing = false;
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Retry page inventory read" }),
  );
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  expect(
    within(dialog).queryByRole("button", { name: "Retry page inventory read" }),
  ).toBeNull();
  fireEvent.click(openButton(dialog));
  await waitFor(() => expect(tabNames()).toHaveLength(4));
  expect(pageCalls("create")).toHaveLength(1);
}, 20000);

const listedTokens = (f: Fixture) =>
  (f.list.mock.calls as unknown as [{ pageToken: string }][]).map(
    ([request]) => request.pageToken,
  );

it("restarts the profile walk with a network read of the first page when retried", async () => {
  const f = fixture({ registered: false });
  // The production client treats a successful read as fresh for one second.
  f.client.setDefaultOptions({ queries: { retry: false, staleTime: 1000 } });
  let failing = true;
  setList(f, async ({ pageToken }) => {
    if (!pageToken) return { profiles: [], nextPageToken: "second" };
    if (failing) throw new ConnectError("down", Code.Unavailable);
    return { profiles: [], nextPageToken: "" };
  });
  mount(f);
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/refetch");
  const retry = await within(dialog).findByRole("button", {
    name: "Retry profile read",
  });
  expect(listedTokens(f)).toEqual(["", "second"]);
  failing = false;
  fireEvent.click(retry);
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  // Both pages were read again instead of being replayed from the cache.
  expect(listedTokens(f)).toEqual(["", "second", "", "second"]);
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(0);
});

it("restarts the profile walk from the first page when an inactive session is reactivated", async () => {
  const f = fixture({ registered: false });
  // A short stale time stands in for the production client's one second.
  f.client.setDefaultOptions({ queries: { retry: false, staleTime: 30 } });
  let added = false;
  setList(f, async ({ pageToken }) =>
    pageToken
      ? { profiles: [], nextPageToken: "" }
      : { profiles: added ? [f.profile] : [], nextPageToken: "second" },
  );
  const tree = (active: boolean) => (
    <TransportProvider transport={f.transport}>
      <QueryClientProvider client={f.client}>
        <MutationIntents>
          <SessionTabsProvider>
            <Workspace f={f} active={active} />
          </SessionTabsProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(true));
  await waitFor(() => expect(listedTokens(f)).toEqual(["", "second"]));
  await settle();
  view.rerender(tree(false));
  // Another window registers this account's profile on the first page while
  // the session is inactive.
  added = true;
  await settle();
  view.rerender(tree(true));
  const dialog = await openDialog();
  typeAddress(dialog, "https://fixture.test/reactivated");
  await waitFor(() => expect(openButton(dialog).disabled).toBe(false));
  // The whole walk was read again from the first page, so the stale partial
  // evidence never proved absence. Re-enabling the query may also refetch the
  // current page once before the restart takes effect.
  expect(listedTokens(f).slice(2).slice(-2)).toEqual(["", "second"]);
  expect(f.register).not.toHaveBeenCalled();
  fireEvent.click(openButton(dialog));
  await waitFor(() => expect(tabNames()).toHaveLength(2));
  expect(f.register).not.toHaveBeenCalled();
  expect(pageCalls("create")).toHaveLength(1);
});
