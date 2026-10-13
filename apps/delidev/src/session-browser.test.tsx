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
  };
  model.selected = model.pages[0]?.id ?? "";
  const state = () => ({
    tabs: {
      tabs: model.pages.map((page) => ({ ...page })),
      selected: model.selected,
    },
    removal_pending: false,
  });
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
        newPage={() => setDialog(true)}
      />
      <button type="button" onClick={() => setDialog(true)}>
        Open tool Browser
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
  const tree = (
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
  return render(strict ? <StrictMode>{tree}</StrictMode> : tree);
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
  fireEvent.click(screen.getByRole("button", { name: "New browser page" }));
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

it("opens the same address dialog from + and the Open tool entry and creates nothing on dismissal", async () => {
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
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/2 tab" }),
  );
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

it("keeps the descriptor with recovery for an unconfirmed close and settles it by observation only", async () => {
  const f = fixture({ pages: 2 });
  f.model.outcome = "cleanup-pending";
  f.model.settleOnObserve = false;
  mount(f);
  await waitFor(() => expect(tabNames()).toHaveLength(3));
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("native view cleanup is not confirmed");
  // Metadata is committed but the descriptor remains until native proof.
  expect(f.model.pages).toHaveLength(1);
  expect(tabNames()).toHaveLength(3);
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
  fireEvent.click(
    screen.getByRole("button", { name: "Close fixture.test/1 tab" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("The browser action failed");
  expect(tabNames()).toHaveLength(2);
  expect(f.model.pages).toHaveLength(1);
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

it("does not retry a failed shared-selection reconciliation without a new selection", async () => {
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
const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 60));
  });

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
