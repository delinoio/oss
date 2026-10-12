import { listen } from "@tauri-apps/api/event";
import {
  useSessionTabsStore,
  SessionTabKind,
  sessionTabKey,
} from "./session-tabs";
import { shortcutModalVisible } from "./shortcuts";
// SPDX-License-Identifier: Apache-2.0
import {
  copy,
  useLocale,
  ownedMessage,
  useProductMessage,
} from "./localization";
import { useEffect, useRef, useState, type RefObject } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke } from "@tauri-apps/api/core";
import {
  BrowserQuery,
  BrowserCapability,
  BrowserProfileState,
  newRequestId,
  type Resource,
  type BrowserProfile,
} from "@delinoio/delidev-api-client";

import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { useBrowserHost } from "./host-capabilities";

enum BrowserAction {
  Navigate = "navigate",
  Back = "back",
  Forward = "forward",
  Reload = "reload",
  SelectTab = "select-tab",
  Resize = "resize",
  Hide = "hide",
}
interface BrowserState {
  tabs: { tabs: { id: string; url: string }[]; selected: string };
  removal_pending: boolean;
}
const idPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function browserProfile(
  profile: BrowserProfile | undefined,
  accountId: string,
): string {
  if (
    !profile ||
    profile.revision === 0n ||
    ![profile.id, profile.serverId, profile.deviceId, profile.accountId].every(
      (id) => idPattern.test(id),
    ) ||
    profile.accountId !== accountId ||
    profile.state !== BrowserProfileState.ACTIVE ||
    profile.deletionRequestId
  )
    throw new Error("Browser profile ownership is unavailable.");
  return profile.id;
}

function browserState(value: BrowserState): BrowserState {
  if (
    !value ||
    typeof value.removal_pending !== "boolean" ||
    !Array.isArray(value.tabs?.tabs) ||
    value.tabs.tabs.length > 16 ||
    typeof value.tabs.selected !== "string"
  )
    throw new Error("Local browser state is unavailable.");
  const ids = new Set<string>();
  for (const tab of value.tabs.tabs) {
    if (
      !idPattern.test(tab.id) ||
      ids.has(tab.id) ||
      typeof tab.url !== "string" ||
      tab.url.length > 8192
    )
      throw new Error("Local browser state is unavailable.");
    const url = new URL(tab.url);
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password
    )
      throw new Error("Local browser state is unavailable.");
    ids.add(tab.id);
  }
  if (ids.size ? !ids.has(value.tabs.selected) : value.tabs.selected !== "")
    throw new Error("Local browser state is unavailable.");
  return value;
}
export interface BrowserController {
  closePage: (pageId: string) => void;
}
interface TabbedBrowser {
  active?: boolean;
  presentationActive?: boolean;
  dialogOpen?: boolean;
  dismissDialog?: () => void;
  controller?: RefObject<Map<string, BrowserController>>;
  selectedPage?: { profile: string; id: string; title: string; label?: string };
  openPage?: (page: {
    profile: string;
    id: string;
    title: string;
    label?: string;
  }) => void;
}
enum PageAction {
  Inventory = "inventory",
  Create = "create",
  Close = "close",
  Observe = "observe",
}
enum PagePhase {
  Accepted = "accepted",
  CleanupPending = "cleanup-pending",
  Completed = "completed",
  Failed = "failed",
}
interface PageReceipt {
  operation_id: string;
  page_id: string;
  phase: PagePhase;
  metadata_committed: boolean;
  native_cleanup_complete: boolean;
  failure: string | null;
}
interface PageInventory {
  state: BrowserState;
  receipt: PageReceipt | null;
  closed_pages: string[];
}
interface PageOperation {
  action: PageAction.Create | PageAction.Close;
  operation_id: string;
  url?: string;
  page_id?: string;
  sent: boolean;
  receipt?: PageReceipt;
  uncertain: boolean;
  selection?: string;
}
function pageInventory(
  value: PageInventory,
  operation?: PageOperation,
): PageInventory {
  browserState(value?.state);
  if (
    !Array.isArray(value.closed_pages) ||
    value.closed_pages.length > 4096 ||
    (value.state.removal_pending && value.closed_pages.length > 0) ||
    !value.closed_pages.every(
      (id) =>
        typeof id === "string" &&
        idPattern.test(id) &&
        !value.state.tabs.tabs.some((page) => page.id === id),
    )
  )
    throw new Error("Browser operation proof is unavailable.");
  if (operation) {
    const receipt = value.receipt;
    if (
      !receipt ||
      receipt.operation_id !== operation.operation_id ||
      !idPattern.test(receipt.page_id) ||
      !Object.values(PagePhase).includes(receipt.phase) ||
      typeof receipt.metadata_committed !== "boolean" ||
      typeof receipt.native_cleanup_complete !== "boolean" ||
      (receipt.failure !== null && typeof receipt.failure !== "string") ||
      (operation.action === PageAction.Close &&
        receipt.page_id !== operation.page_id) ||
      (receipt.phase === PagePhase.Completed &&
        (receipt.failure !== null ||
          !receipt.metadata_committed ||
          !receipt.native_cleanup_complete ||
          value.state.removal_pending ||
          (operation.action === PageAction.Close &&
            value.state.tabs.tabs.some((page) => page.id === receipt.page_id))))
    )
      throw new Error("Browser operation proof is unavailable.");
  } else if (value.receipt !== null)
    throw new Error("Browser inventory proof is unavailable.");
  return value;
}

export interface BrowserLayoutControls {
  wide: boolean;
  expanded: boolean;
  toggleExpanded: () => void;
}
function BrowserHeader({
  close,
  layout,
}: {
  close: () => void;
  layout?: BrowserLayoutControls;
}) {
  const [information, setInformation] = useState(false);
  return (
    <>
      <header className="browser-header">
        <h3>{copy("session-browser.browser_d31de1")}</h3>
        <small>{copy("session-browser.accountProfile")}</small>
        <div className="browser-header-actions">
          <button
            type="button"
            aria-label={copy("session-browser.information")}
            onClick={() => setInformation(true)}
          >
            ⓘ
          </button>
          {layout ? (
            <button
              type="button"
              disabled={!layout.wide}
              onClick={layout.toggleExpanded}
            >
              {copy(
                layout.expanded
                  ? "session-browser.restore"
                  : "session-browser.expand",
              )}
            </button>
          ) : null}
        </div>
      </header>
      {information ? (
        <Modal
          title={copy("session-browser.information")}
          close={() => setInformation(false)}
          className="browser-information"
          focusClose
        >
          <p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p>
          <p>{copy("session-browser.enterAWebAddressThenOpen_60a48b")}</p>
        </Modal>
      ) : null}
    </>
  );
}
export function browserTabTitle(address: string): string {
  try {
    const url = new URL(address);
    return `${url.host}${url.pathname === "/" ? "" : url.pathname}`;
  } catch {
    return address;
  }
}
function UnavailableBrowserDialog({ close }: { close: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <Modal
      title={copy("session-browser.newPage")}
      close={close}
      className="browser-new-page"
      initialFocus={input}
      trapFocus
    >
      <form onSubmit={(event) => event.preventDefault()}>
        <p>{copy("session-browser.sharingSummary")}</p>
        <label>
          {copy("session-browser.address_56ef8f")}
          <input
            ref={input}
            maxLength={8192}
            placeholder="https://example.com/"
          />
        </label>
        <p>
          {copy("session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8")}
        </p>
        <footer>
          <button type="button" onClick={close}>
            {copy("session-browser.cancel")}
          </button>
          <button disabled>{copy("session-browser.openPage")}</button>
        </footer>
      </form>
    </Modal>
  );
}

export function SessionBrowser({
  session,
  accountId,
  close,
  layout,
  active = true,
  presentationActive = active,
  dialogOpen = false,
  dismissDialog = () => {},
  controller,
  selectedPage,
  openPage,
}: {
  session: Resource;
  accountId: string;
  close: () => void;
  layout?: BrowserLayoutControls;
} & TabbedBrowser) {
  useLocale();
  const available = useBrowserHost();
  if (!available)
    return dialogOpen ? (
      <UnavailableBrowserDialog close={dismissDialog} />
    ) : presentationActive ? (
      <section
        className="session-browser"
        aria-label={copy("session-browser.sessionBrowser_47d746")}
      >
        <div className="browser-chrome">
          <BrowserHeader close={close} layout={layout} />
          <p>
            {copy(
              "session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8",
            )}
          </p>
        </div>
      </section>
    ) : null;
  return (
    <NativeSessionBrowser
      session={session}
      accountId={accountId}
      close={close}
      layout={layout}
      active={active}
      presentationActive={presentationActive}
      dialogOpen={dialogOpen}
      dismissDialog={dismissDialog}
      controller={controller}
      selectedPage={selectedPage}
      openPage={openPage}
    />
  );
}
function NativeSessionBrowser({
  session,
  accountId,
  close,
  layout,
  active = true,
  presentationActive = active,
  dialogOpen = false,
  dismissDialog = () => {},
  controller,
  selectedPage,
  openPage,
}: {
  session: Resource;
  accountId: string;
  close: () => void;
  layout?: BrowserLayoutControls;
} & TabbedBrowser) {
  useLocale();
  const [address, setAddress] = useState("");
  const [profileId, setProfileId] = useState<string>();
  const [state, setState] = useState<BrowserState>();
  const [failure, setFailure] = useProductMessage();
  const [busy, setBusyState] = useState(false);
  const actionBusy = useRef(false);
  const actionVersion = useRef(0);
  const setBusy = (value: boolean) => {
    actionBusy.current = value;
    setBusyState(value);
  };
  const [retry, setRetry] = useState(0);
  const [pageDraft, setPageDraft] = useState("");
  const pageInput = useRef<HTMLInputElement>(null);
  const [operation, setOperation] = useState<PageOperation>();
  const operationRef = useRef<PageOperation | undefined>(undefined);
  const nativeBusy = useRef(false);
  const [nativePending, setNativePending] = useState(false);
  const [submitted, setSubmitted] = useState<{
    url: string;
    revision: bigint;
    registered: boolean;
    selection: string;
  }>();
  const needsPresentation = useRef(false);
  needsPresentation.current = Boolean(
    selectedPage &&
    selectedPage.profile === profileId &&
    state?.tabs.tabs.some((tab) => tab.id === selectedPage.id),
  );
  const viewport = useRef<HTMLDivElement>(null);
  const addressInput = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const presentation = useRef("");
  const shortcutAdmission = useRef("");
  const tabsStore = useSessionTabsStore();
  const activeRef = useRef(active);
  activeRef.current = presentationActive;
  const owner = useRef({ profileId, retry });
  owner.current = { profileId, retry };
  const selectionAttempt = useRef("");
  const selectionTarget = useRef("");
  const selectedPageRef = useRef(selectedPage);
  selectedPageRef.current = selectedPage;
  const observe = (result: BrowserState, profile: string) => {
    setState(result);
    if (resolutionActive.current)
      tabsStore.refreshPages(
        session.id,
        profile,
        result.tabs.tabs.map((tab) => ({
          id: tab.id,
          title: browserTabTitle(tab.url),
          label: tab.url,
        })),
      );
  };
  const capabilities = useQuery(
    BrowserQuery.getBrowserCapabilities,
    {},
    { retry: false, enabled: active },
  );
  const supported =
    capabilities.data?.capabilities.includes(
      BrowserCapability.PROTECTED_DEVICE_PROFILE_V1,
    ) === true;
  const registration = useRetainedMutation(
    `browser-register:${session.id}:${accountId}`,
    BrowserQuery.registerBrowserProfile,
    (response) => {
      try {
        setProfileId(browserProfile(response.profile, accountId));
        setSubmitted((original) =>
          original ? { ...original, registered: true } : original,
        );
        setFailure(undefined);
      } catch {
        setFailure(ownedMessage("session-browser.extra.5092c54369dd"));
      }
    },
    (response, request) =>
      Boolean(browserProfile(response.profile, request.accountId)),
  );
  const [profilePage, setProfilePage] = useState("");
  const [inventoryReady, setInventoryReady] = useState(false);
  const [inventoryEpoch, setInventoryEpoch] = useState(0);
  const profileTokens = useRef(new Set<string>());
  const profiles = useQuery(
    BrowserQuery.listBrowserProfiles,
    { pageSize: 100, pageToken: profilePage },
    {
      retry: false,
      enabled: active && supported && !profileId && idPattern.test(accountId),
    },
  );
  const dialogVisible = useRef(dialogOpen);
  dialogVisible.current = dialogOpen;
  const resolutionActive = useRef(active);
  resolutionActive.current = active;
  const operationRunner = useRef<
    (operation: PageOperation, observe: boolean) => Promise<void>
  >(async () => {});
  const pageResult = (value: PageInventory) => {
    observe(value.state, profileId!);
    if (resolutionActive.current && !value.state.removal_pending)
      tabsStore.reconcilePages(
        session.id,
        profileId!,
        value.state.tabs.tabs.map((page) => ({
          id: page.id,
          title: browserTabTitle(page.url),
          label: page.url,
        })),
      );
    for (const pageId of value.closed_pages)
      tabsStore.dismissPage(session.id, profileId!, pageId);
    setInventoryReady(true);
  };
  const focusSelected = () => {
    requestAnimationFrame(() => {
      const current = tabsStore.snapshot(session.id);
      const index = current.tabs.findIndex(
        (tab) => sessionTabKey(tab) === current.selected,
      );
      document
        .getElementById(`session-tab-${session.id}-${index}`)
        ?.focus({ preventScroll: true });
    });
  };
  const runOperation = async (original: PageOperation, read: boolean) => {
    if (
      !profileId ||
      nativeBusy.current ||
      !alive.current ||
      operationRef.current?.operation_id !== original.operation_id ||
      (!read && original.sent)
    )
      return;
    nativeBusy.current = true;
    setNativePending(true);
    actionVersion.current += 1;
    if (!read) original.sent = true;
    try {
      const value = pageInventory(
        await invoke<PageInventory>("browser_pages", {
          profileId,
          request: read
            ? {
                action: PageAction.Observe,
                operation_id: original.operation_id,
              }
            : {
                action: original.action,
                operation_id: original.operation_id,
                ...(original.url ? { url: original.url } : {}),
                ...(original.page_id ? { page_id: original.page_id } : {}),
              },
        }),
        original,
      );
      if (
        !alive.current ||
        operationRef.current?.operation_id !== original.operation_id
      )
        return;
      original.receipt = value.receipt!;
      original.uncertain = false;
      const before = tabsStore.snapshot(session.id);
      const closeKey =
        original.action === PageAction.Close
          ? sessionTabKey({
              kind: SessionTabKind.Page,
              profile: profileId,
              id: original.page_id!,
              title: "",
            })
          : "";
      const closeIndex = before.tabs.findIndex(
        (tab) => sessionTabKey(tab) === closeKey,
      );
      const closeControlFocused =
        closeIndex >= 0 &&
        document
          .getElementById(`session-tab-${session.id}-${closeIndex}`)
          ?.parentElement?.contains(document.activeElement);
      const focusClosure = before.selected === closeKey || closeControlFocused;
      pageResult(value);
      setFailure(undefined);
      if (original.receipt.phase === PagePhase.Completed) {
        if (original.action === PageAction.Close) {
          tabsStore.dismissPage(session.id, profileId, original.page_id!);
          if (resolutionActive.current && focusClosure) focusSelected();
        } else {
          const page = value.state.tabs.tabs.find(
            (page) => page.id === original.receipt?.page_id,
          );
          if (
            page &&
            resolutionActive.current &&
            (dialogVisible.current || before.selected === original.selection)
          ) {
            openPage?.({
              profile: profileId,
              id: page.id,
              title: browserTabTitle(page.url),
              label: page.url,
            });
            dismissDialog();
            setPageDraft("");
            focusSelected();
          }
        }
        operationRef.current = undefined;
        setOperation(undefined);
        setSubmitted(undefined);
      } else {
        setOperation({ ...original });
        if (
          original.receipt.failure ||
          original.receipt.phase === PagePhase.Failed
        )
          setFailure(ownedMessage("session-browser.extra.51fee7071e2b"));
      }
    } catch {
      if (
        alive.current &&
        operationRef.current?.operation_id === original.operation_id
      ) {
        original.uncertain = true;
        setOperation({ ...original });
        setFailure(ownedMessage("session-browser.operationUnknown"));
        console.warn("delidev.browser_page", {
          phase: "observation_unavailable",
          action: original.action,
        });
      }
    } finally {
      nativeBusy.current = false;
      if (alive.current) setNativePending(false);
    }
  };
  operationRunner.current = runOperation;
  const submitPage = () => {
    if (
      !validAddress(pageDraft) ||
      operationRef.current ||
      submitted ||
      !active ||
      !supported ||
      !inventoryReady ||
      nativeBusy.current ||
      registration.busy ||
      registration.uncertain
    )
      return;
    setFailure(undefined);
    setSubmitted({
      url: pageDraft,
      revision: session.revision,
      registered: false,
      selection: tabsStore.snapshot(session.id).selected,
    });
    void registration.send({
      session: {
        id: session.id,
        expectedRevision: session.revision,
        requestId: newRequestId(),
      },
      accountId,
    });
  };
  useEffect(() => {
    if (
      submitted &&
      !submitted.registered &&
      registration.error &&
      !registration.busy &&
      !registration.uncertain
    )
      setSubmitted(undefined);
  }, [
    submitted,
    registration.error,
    registration.busy,
    registration.uncertain,
  ]);
  useEffect(() => {
    if (!profiles.data || profileId) return;
    try {
      const matching = profiles.data.profiles.filter(
        (profile) => profile.accountId === accountId,
      );
      if (matching.length > 1)
        throw new Error("Browser profile proof is unavailable.");
      if (matching.length) {
        setProfileId(browserProfile(matching[0], accountId));
      } else if (profiles.data.nextPageToken) {
        if (
          profileTokens.current.has(profiles.data.nextPageToken) ||
          profileTokens.current.size >= 64
        )
          throw new Error("Browser profile proof is unavailable.");
        profileTokens.current.add(profiles.data.nextPageToken);
        setProfilePage(profiles.data.nextPageToken);
      } else setInventoryReady(true);
    } catch {
      setFailure(ownedMessage("session-browser.extra.5092c54369dd"));
    }
  }, [profiles.data, profileId, accountId]);
  useEffect(() => {
    if (!profileId || !submitted?.registered || operationRef.current) return;
    const original: PageOperation = {
      action: PageAction.Create,
      operation_id: newRequestId(),
      url: submitted.url,
      selection: submitted.selection,
      sent: false,
      uncertain: false,
    };
    operationRef.current = original;
    setOperation(original);
    void operationRunner.current(original, false);
  }, [profileId, submitted]);
  useEffect(() => {
    if (!profileId) return;
    controller?.current.set(profileId, {
      closePage: (pageId) => {
        if (
          !resolutionActive.current ||
          !inventoryReady ||
          state?.removal_pending
        )
          return;
        if (operationRef.current || nativeBusy.current) {
          setFailure(ownedMessage("session-browser.operationUnknown"));
          return;
        }
        const original: PageOperation = {
          action: PageAction.Close,
          operation_id: newRequestId(),
          page_id: pageId,
          sent: false,
          uncertain: false,
        };
        operationRef.current = original;
        setOperation(original);
        void operationRunner.current(original, false);
      },
    });
    return () => {
      controller?.current.delete(profileId);
    };
  }, [profileId, controller, inventoryReady, state?.removal_pending]);
  useEffect(() => {
    if (!profileId || (!active && !operationRef.current)) return;
    let disposed = false,
      reading = false;
    const read = async () => {
      if (disposed || reading || nativeBusy.current) return;
      const pending = operationRef.current;
      if (pending && pending.receipt?.phase !== PagePhase.Failed) {
        await operationRunner.current(pending, true);
        return;
      }
      reading = true;
      const version = actionVersion.current;
      try {
        const value = pageInventory(
          await invoke<PageInventory>("browser_pages", {
            profileId,
            request: { action: PageAction.Inventory },
          }),
        );
        if (
          !disposed &&
          alive.current &&
          version === actionVersion.current &&
          !nativeBusy.current
        )
          pageResult(value);
      } catch {
        if (!disposed)
          setFailure(ownedMessage("session-browser.extra.c6d1ec80c1c4"));
      } finally {
        reading = false;
      }
    };
    void read();
    const timer = window.setInterval(() => void read(), 1000);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [profileId, active, Boolean(operation), inventoryEpoch]);
  useEffect(() => {
    const url = state?.tabs.tabs.find(
      (page) =>
        page.id === selectedPage?.id && selectedPage.profile === profileId,
    )?.url;
    if (url) setAddress(url);
  }, [selectedPage?.id, selectedPage?.profile, profileId, state?.tabs.tabs]);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    if (!profileId || !viewport.current) return;
    let disposed = false,
      opened = false,
      opening = false,
      resizing = false;
    let lastBounds = "",
      queued = false,
      failed = false,
      ownedViewId = "";
    let hiding = false,
      hideRequired = false,
      hideRetry: number | undefined;
    const node = viewport.current;
    const visible = () =>
      activeRef.current &&
      needsPresentation.current &&
      node.isConnected &&
      !node.closest("[hidden], [inert]") &&
      !Array.from(
        documentGlobal().querySelectorAll(
          'dialog[open]:not([role="region"]), [role="dialog"]:not(dialog)',
        ),
      ).some((dialog) => {
        // The responsive sidebar retains a closed dialog, and its wide layout is
        // an open nonmodal region. Only a visible dialog should cover the child.
        const style = getComputedStyle(dialog),
          rect = dialog.getBoundingClientRect();
        return (
          !dialog.closest("[hidden], [inert]") &&
          style.display !== "none" &&
          style.visibility !== "hidden" &&
          rect.width > 0 &&
          rect.height > 0
        );
      }) &&
      node.getBoundingClientRect().width >= 1 &&
      node.getBoundingClientRect().height >= 1;
    const bounds = () => {
      const r = node.getBoundingClientRect();
      let left = Math.max(0, r.left),
        top = Math.max(0, r.top),
        right = Math.min(window.innerWidth, r.right),
        bottom = Math.min(window.innerHeight, r.bottom);
      for (
        let parent = node.parentElement;
        parent;
        parent = parent.parentElement
      ) {
        const style = getComputedStyle(parent),
          rect = parent.getBoundingClientRect();
        if (/auto|scroll|hidden|clip/.test(style.overflowX)) {
          left = Math.max(left, rect.left);
          right = Math.min(right, rect.right);
        }
        if (/auto|scroll|hidden|clip/.test(style.overflowY)) {
          top = Math.max(top, rect.top);
          bottom = Math.min(bottom, rect.bottom);
        }
      }
      return {
        x: left,
        y: top,
        width: Math.max(0, right - left),
        height: Math.max(0, bottom - top),
      };
    };
    const hide = async () => {
      if (!opened || hiding) return;
      hideRequired = true;
      hiding = true;
      const viewId = ownedViewId;
      let closed = false;
      try {
        await invoke("control_browser", {
          profileId,
          viewId,
          action: BrowserAction.Hide,
        });
        opened = false;
        ownedViewId = "";
        lastBounds = "";
        hideRequired = false;
        closed = true;
        if (presentation.current === viewId) presentation.current = "";
        if (hideRetry !== undefined) {
          window.clearTimeout(hideRetry);
          hideRetry = undefined;
        }
      } catch {
        if (!disposed)
          setFailure(ownedMessage("session-browser.extra.a520c066c88f"));
        // Keep exact cleanup ownership after unmount. A failed Hide has not
        // released the raw child; retry only this closure, never an open/action.
        if (hideRetry === undefined)
          hideRetry = window.setTimeout(() => {
            hideRetry = undefined;
            void hide();
          }, 250);
      } finally {
        hiding = false;
        if (closed && !disposed) {
          queued = false;
          void update();
        }
      }
    };
    const update = async () => {
      if (disposed || failed) return;
      if (opening || resizing || hiding) {
        queued = true;
        return;
      }
      if (opened && hideRequired) {
        await hide();
        return;
      }
      const area = bounds(),
        geometry = JSON.stringify(area);
      if (!visible() || area.width < 1 || area.height < 1) {
        await hide();
        return;
      }
      if (!opened) {
        opening = true;
        const viewId = newRequestId();
        ownedViewId = viewId;
        presentation.current = viewId;
        try {
          const result = browserState(
            await invoke<BrowserState>("open_browser", {
              profileId,
              viewId,
              url: "",
              pageId:
                selectedPageRef.current?.profile === profileId
                  ? selectedPageRef.current.id
                  : undefined,
              bounds: area,
            }),
          );
          opened = true;
          lastBounds = geometry;
          if (disposed || presentation.current !== viewId) {
            await hide();
            return;
          }
          if (!visible()) {
            await hide();
            return;
          }
          observe(result, profileId);
        } catch {
          failed = true;
          if (!disposed) {
            setBusy(false);
            setFailure(ownedMessage("session-browser.extra.08759b664829"));
          }
        } finally {
          opening = false;
          if (queued) {
            queued = false;
            void update();
          }
        }
      } else {
        if (geometry === lastBounds) return;
        resizing = true;
        const viewId = presentation.current;
        const version = actionVersion.current;
        try {
          const result = browserState(
            await invoke<BrowserState>("control_browser", {
              profileId,
              viewId,
              action: BrowserAction.Resize,
              bounds: area,
            }),
          );
          if (
            !disposed &&
            opened &&
            visible() &&
            presentation.current === viewId
          ) {
            lastBounds = geometry;
            if (!actionBusy.current && actionVersion.current === version)
              observe(result, profileId);
          }
        } catch {
          lastBounds = "";
          if (!disposed)
            setFailure(ownedMessage("session-browser.extra.1b8cff3d45fb"));
        } finally {
          resizing = false;
          if (queued) {
            queued = false;
            void update();
          }
        }
      }
    };
    void update();
    const move = () => void update();
    window.addEventListener("scroll", move, true);
    window.addEventListener("resize", move);
    const observer = new ResizeObserver(() => void update());
    observer.observe(node);
    const visibility = new MutationObserver(() => void update());
    visibility.observe(documentGlobal().body, {
      subtree: true,
      attributes: true,
      attributeFilter: ["hidden", "inert", "open", "class"],
      childList: true,
    });
    const timer = window.setInterval(() => {
      if (!opened || disposed || !visible() || actionBusy.current) return;
      const viewId = presentation.current;
      const version = actionVersion.current;
      void invoke<BrowserState>("browser_state", {
        profileId,
        viewId,
      })
        .then((value) => {
          if (
            !disposed &&
            opened &&
            visible() &&
            presentation.current === viewId &&
            !actionBusy.current &&
            actionVersion.current === version
          )
            observe(browserState(value), profileId);
        })
        .catch(() => {
          if (!disposed)
            setFailure(ownedMessage("session-browser.extra.c6d1ec80c1c4"));
        });
    }, 1000);
    return () => {
      disposed = true;
      window.removeEventListener("scroll", move, true);
      window.removeEventListener("resize", move);
      observer.disconnect();
      visibility.disconnect();
      window.clearInterval(timer);
      void hide();
    };
    // Address changes navigate the existing native tab only at the explicit Go action.
    // Reopening after a modal restores native local tabs without another registration.
  }, [profileId, retry]);
  const control = async (action: BrowserAction, tabId?: string) => {
    if (
      !profileId ||
      actionBusy.current ||
      nativeBusy.current ||
      operationRef.current ||
      !activeRef.current ||
      !needsPresentation.current ||
      !presentation.current ||
      shortcutModalVisible() ||
      !viewport.current?.isConnected ||
      viewport.current.closest("[hidden], [inert]") ||
      state?.removal_pending
    )
      return;
    const viewId = presentation.current;
    const originalOwner = owner.current;
    const selected = tabsStore.snapshot(session.id).selected;
    const version = ++actionVersion.current;
    const owned = () =>
      alive.current &&
      owner.current.profileId === profileId &&
      owner.current.retry === originalOwner.retry &&
      actionVersion.current === version;
    const current = () =>
      owned() &&
      activeRef.current &&
      presentation.current === viewId &&
      !shortcutModalVisible() &&
      !viewport.current?.closest("[hidden], [inert]");
    // A read started before this action cannot roll its result back afterward.
    setBusy(true);
    setFailure(undefined);
    try {
      const result = browserState(
        await invoke<BrowserState>("control_browser", {
          profileId,
          viewId,
          action,
          url: address,
          tabId,
        }),
      );
      if (current()) {
        observe(result, profileId);
        if (
          openPage &&
          tabsStore.snapshot(session.id).selected === selected &&
          [BrowserAction.SelectTab, BrowserAction.Navigate].includes(action)
        ) {
          const tab = result.tabs.tabs.find(
            (value) => value.id === result.tabs.selected,
          );
          if (tab)
            openPage({
              profile: profileId,
              id: tab.id,
              title: browserTabTitle(tab.url),
              label: tab.url,
            });
        }
      }
    } catch {
      if (current())
        setFailure(ownedMessage("session-browser.extra.51fee7071e2b"));
    } finally {
      if (
        alive.current &&
        owner.current.profileId === profileId &&
        owner.current.retry === originalOwner.retry
      )
        setBusy(false);
    }
  };
  useEffect(() => {
    const target = selectedPage
      ? JSON.stringify([
          profileId,
          retry,
          presentation.current,
          selectedPage.profile,
          selectedPage.id,
        ])
      : "";
    if (selectionTarget.current !== target) {
      selectionTarget.current = target;
      selectionAttempt.current = "";
    }
    if (
      busy ||
      nativePending ||
      Boolean(operation) ||
      !presentation.current ||
      !presentationActive ||
      shortcutModalVisible() ||
      viewport.current?.closest("[hidden], [inert]") ||
      !selectedPage ||
      selectedPage.profile !== profileId ||
      !state ||
      state.removal_pending ||
      state.tabs.selected === selectedPage.id ||
      !state.tabs.tabs.some((tab) => tab.id === selectedPage.id)
    )
      return;
    const attempt = JSON.stringify([
      profileId,
      presentation.current,
      selectedPage.id,
    ]);
    // Busy completion reconciles the latest selection once. A rejected or lost
    // SelectTab reply cannot become an automatic retry on every observation.
    if (selectionAttempt.current === attempt) return;
    selectionAttempt.current = attempt;
    void control(BrowserAction.SelectTab, selectedPage.id);
  }, [
    presentationActive,
    busy,
    nativePending,
    operation,
    selectedPage?.id,
    selectedPage?.profile,
    profileId,
    retry,
    state,
  ]);
  useEffect(() => {
    if (!profileId || !openPage) return;
    let disposed = false;
    let unlisten: (() => void) | undefined;
    void listen<{
      profile_id: string;
      view_id: string;
      position: number;
      token: string;
    }>("session-tab-selection", (event) => {
      if (
        !disposed &&
        activeRef.current &&
        event.payload.profile_id === profileId &&
        event.payload.view_id === presentation.current &&
        event.payload.token === shortcutAdmission.current &&
        !shortcutModalVisible()
      ) {
        document
          .getElementById(
            `session-tab-${session.id}-${event.payload.position - 1}`,
          )
          ?.focus({ preventScroll: true });
        tabsStore.position(session.id, event.payload.position);
      }
    })
      .then((stop) => {
        if (disposed) stop();
        else unlisten = stop;
      })
      .catch(() => {
        if (!disposed)
          console.warn("delidev.browser_shortcuts", {
            stage: "listener",
            classification: "unavailable",
          });
      });
    return () => {
      disposed = true;
      unlisten?.();
    };
  }, [profileId, session.id, tabsStore]);
  useEffect(() => {
    if (!profileId || !openPage) return;
    let disposed = false;
    const update = () => {
      const viewId = presentation.current;
      if (!viewId) return;
      const token = newRequestId();
      shortcutAdmission.current = token;
      void invoke("browser_tab_shortcuts", {
        profileId,
        viewId,
        token,
        count:
          presentationActive && !shortcutModalVisible()
            ? Math.min(9, tabsStore.snapshot(session.id).tabs.length)
            : 0,
      }).catch(() => {
        if (!disposed)
          console.warn("delidev.browser_shortcuts", {
            stage: "admission",
            classification: "unavailable",
          });
      });
    };
    update();
    const observer = new MutationObserver(update);
    observer.observe(document.body, {
      subtree: true,
      attributes: true,
      attributeFilter: ["open", "hidden", "inert"],
      childList: true,
    });
    const stop = tabsStore.subscribe(update);
    const timer = window.setInterval(update, 250);
    return () => {
      disposed = true;
      shortcutAdmission.current = "";
      window.clearInterval(timer);
      observer.disconnect();
      stop();
      const viewId = presentation.current;
      if (viewId)
        void invoke("browser_tab_shortcuts", {
          profileId,
          viewId,
          token: newRequestId(),
          count: 0,
        }).catch(() => {});
    };
  }, [
    presentationActive,
    profileId,
    state?.tabs.selected,
    tabsStore,
    session.id,
  ]);
  const blocked =
    !presentationActive || busy || nativePending || state?.removal_pending;
  const creationBlocked =
    !active ||
    !supported ||
    !inventoryReady ||
    capabilities.isFetching ||
    profiles.isFetching ||
    registration.busy ||
    registration.uncertain ||
    nativePending ||
    Boolean(operation) ||
    Boolean(submitted) ||
    state?.removal_pending ||
    (state?.tabs.tabs.length ?? 0) >= 16 ||
    !validAddress(pageDraft) ||
    !idPattern.test(accountId);
  return (
    <>
      <section
        hidden={!presentationActive || !needsPresentation.current}
        inert={!presentationActive || !needsPresentation.current}
        className={`session-app-panel session-browser${profileId ? " browser-registered" : ""}`}
        aria-label={copy("session-browser.sessionBrowser_47d746")}
      >
        <div className="browser-chrome">
          <BrowserHeader close={close} layout={layout} />
          {failure ? <p role="alert">{failure}</p> : null}
          {state?.removal_pending ? (
            <p role="alert">
              {copy("session-browser.thisAccountWasDeletedItsProfile_4cf6fc")}
            </p>
          ) : null}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void control(BrowserAction.Navigate);
            }}
            className="browser-address"
          >
            <button
              type="button"
              disabled={blocked}
              onClick={() => void control(BrowserAction.Back)}
              aria-label={copy("session-browser.back_76900f")}
            >
              ←
            </button>
            <button
              type="button"
              disabled={blocked}
              onClick={() => void control(BrowserAction.Forward)}
              aria-label={copy("session-browser.forward_f1c65e")}
            >
              →
            </button>
            <button
              type="button"
              disabled={blocked}
              onClick={() => void control(BrowserAction.Reload)}
              aria-label={copy("session-browser.reload_bdc090")}
            >
              ↻
            </button>
            <label>
              {copy("session-browser.address_56ef8f")}
              <input
                ref={addressInput}
                value={address}
                maxLength={8192}
                onChange={(event) => setAddress(event.target.value)}
                placeholder="https://example.com/"
              />
            </label>
            <button disabled={blocked || !validAddress(address)}>
              {copy("session-browser.go_6cc851")}
            </button>
            <details className="browser-overflow">
              <summary aria-label={copy("session-browser.moreActions")}>
                ⋯
              </summary>
              <button
                type="button"
                disabled={blocked}
                onClick={() => {
                  setFailure(undefined);
                  setRetry((value) => value + 1);
                }}
              >
                {copy("session-browser.retryNativeView_0d84d0")}
              </button>
            </details>
          </form>
        </div>
        <div
          ref={viewport}
          className="browser-viewport"
          aria-label={copy("session-browser.untrustedBrowserContent_9c54bc")}
        />
      </section>
      {operation ? (
        <div
          className="browser-operation-recovery"
          hidden={!active}
          role="status"
        >
          <p>
            {copy(
              operation.action === PageAction.Create
                ? "session-browser.createPending"
                : operation.receipt?.metadata_committed
                  ? "session-browser.cleanupPending"
                  : "session-browser.closePending",
            )}
          </p>
          <button
            type="button"
            disabled={nativePending}
            onClick={() => void runOperation(operation, true)}
          >
            {copy("session-browser.observeOperation")}
          </button>
          {failure ? <p role="alert">{failure}</p> : null}
        </div>
      ) : null}
      {dialogOpen ? (
        <Modal
          title={copy("session-browser.newPage")}
          close={dismissDialog}
          className="browser-new-page"
          initialFocus={pageInput}
          trapFocus
        >
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (!creationBlocked) submitPage();
            }}
          >
            <p>{copy("session-browser.sharingSummary")}</p>
            <label>
              {copy("session-browser.address_56ef8f")}
              <input
                ref={pageInput}
                value={pageDraft}
                maxLength={8192}
                onChange={(event) => setPageDraft(event.target.value)}
                placeholder="https://example.com/"
              />
            </label>
            {!inventoryReady ||
            capabilities.isFetching ||
            profiles.isFetching ? (
              <p role="status">{copy("session-browser.loading")}</p>
            ) : null}
            {!supported && !capabilities.isFetching ? (
              <p role="status">
                {copy(
                  "session-browser.protectedBrowserProfilesAreUnavailableUntil_033b80",
                )}
              </p>
            ) : null}
            {(state?.tabs.tabs.length ?? 0) >= 16 ? (
              <p role="status">{copy("session-browser.pageLimit")}</p>
            ) : null}
            {state?.removal_pending ? (
              <p role="alert">
                {copy("session-browser.thisAccountWasDeletedItsProfile_4cf6fc")}
              </p>
            ) : null}
            {failure ? <p role="alert">{failure}</p> : null}
            <Problem
              error={capabilities.error || profiles.error || registration.error}
            />
            {failure || capabilities.error || profiles.error ? (
              <button
                type="button"
                disabled={nativePending || Boolean(operation)}
                onClick={() => {
                  setFailure(undefined);
                  if (supported && !profileId) void profiles.refetch();
                  void capabilities.refetch();
                  setInventoryEpoch((value) => value + 1);
                }}
              >
                {copy("session-browser.retryInventory")}
              </button>
            ) : null}
            {registration.uncertain ? (
              <button
                type="button"
                disabled={registration.busy}
                onClick={registration.retry}
              >
                {copy("session-browser.retryTheSameRegistration_c86ef9")}
              </button>
            ) : null}
            {operation ? (
              <div role="status">
                <p>
                  {copy(
                    operation.action === PageAction.Create
                      ? "session-browser.createPending"
                      : "session-browser.closePending",
                  )}
                </p>
                <button
                  type="button"
                  disabled={nativePending}
                  onClick={() => void runOperation(operation, true)}
                >
                  {copy("session-browser.observeOperation")}
                </button>
              </div>
            ) : null}
            {operation?.receipt?.phase === PagePhase.Failed &&
            !operation.receipt.metadata_committed ? (
              <button
                type="button"
                onClick={() => {
                  if (
                    nativeBusy.current ||
                    operationRef.current?.receipt?.phase !== PagePhase.Failed ||
                    operationRef.current.receipt.metadata_committed
                  )
                    return;
                  operationRef.current = undefined;
                  setOperation(undefined);
                  setSubmitted(undefined);
                }}
              >
                {copy("session-browser.clearFailedOperation")}
              </button>
            ) : null}
            <footer>
              <button type="button" onClick={dismissDialog}>
                {copy("session-browser.cancel")}
              </button>
              <button disabled={creationBlocked}>
                {copy("session-browser.openPage")}
              </button>
            </footer>
          </form>
        </Modal>
      ) : null}
    </>
  );
}
// Keep DOM access distinct from the versioned resource-document decoder.
const documentGlobal = () => globalThis.document;

export function validAddress(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (
      ["http:", "https:"].includes(url.protocol) &&
      !url.username &&
      !url.password &&
      raw.length <= 8192 &&
      !/[\0\r\n]/.test(raw)
    );
  } catch {
    return false;
  }
}

export function SessionBrowsers({
  session,
  accountId,
  active,
  presentationActive,
  selectedPage,
  openPage,
  dialogOpen,
  dismissDialog,
  controller,
}: {
  session: Resource;
  accountId: string;
  active: boolean;
  presentationActive: boolean;
  selectedPage?: TabbedBrowser["selectedPage"];
  openPage: NonNullable<TabbedBrowser["openPage"]>;
  dialogOpen: boolean;
  dismissDialog: () => void;
  controller: NonNullable<TabbedBrowser["controller"]>;
}) {
  const [accounts, setAccounts] = useState([accountId]);
  useEffect(() => {
    setAccounts((current) =>
      current.includes(accountId) ? current : [...current, accountId],
    );
  }, [accountId]);
  return (
    <>
      {accounts.map((ownedAccount) => (
        <SessionBrowser
          key={ownedAccount}
          session={session}
          accountId={ownedAccount}
          close={() => {}}
          active={active && ownedAccount === accountId}
          presentationActive={presentationActive && ownedAccount === accountId}
          selectedPage={selectedPage}
          openPage={openPage}
          dialogOpen={dialogOpen && ownedAccount === accountId}
          dismissDialog={dismissDialog}
          controller={controller}
        />
      ))}
    </>
  );
}
