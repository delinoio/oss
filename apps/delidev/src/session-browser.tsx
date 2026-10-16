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
import {
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
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
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
  type EntranceProps,
} from "./entrance-motion";

enum BrowserAction {
  Navigate = "navigate",
  Back = "back",
  Forward = "forward",
  Reload = "reload",
  SelectTab = "select-tab",
  Resize = "resize",
  Hide = "hide",
}
/** Native-local page operations. Only Create and Close mutate a page. */
// The read-only profile enumeration state. Only Found and Absent admit a new
// page: Absent is a settled read with no matching profile and no further token.
enum ProfileDiscovery {
  Loading = "loading",
  Paginating = "paginating",
  Failed = "failed",
  Found = "found",
  Absent = "absent",
}
enum DiscoveryProof {
  Absent = "absent",
  Failed = "failed",
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
const pageLimit = 16;
// Pages opened or closed by another window align at this read-only cadence; a
// pending original operation is observed every second instead.
const inventoryInterval = 3000;
interface BrowserState {
  tabs: { tabs: { id: string; url: string }[]; selected: string };
  removal_pending: boolean;
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
  /**
   * Set when a removal-pending reply was normalized to an empty inventory: the
   * page ids the host actually listed, kept only to prove an absent Close.
   */
  rawPageIds?: string[];
}
/** The one in-flight explicit page operation and its original identity. */
interface PageOperation {
  action: PageAction.Create | PageAction.Close;
  operation_id: string;
  url?: string;
  page_id?: string;
  /** Set once the original mutation was handed to the native command. */
  sent: boolean;
  /** The reply was lost; only observation may settle the original. */
  uncertain: boolean;
  receipt?: PageReceipt;
  /** The selected workspace tab when a Create was submitted. */
  selection?: string;
}
interface CloseFocus {
  operationId: string;
  control: Element;
  navigation: number;
}
export interface BrowserHandle {
  /** Requests closure of one retained native page; descriptors close on proof. */
  closePage: (profile: string, pageId: string) => void;
}
interface PageDescriptor {
  profile: string;
  id: string;
  title: string;
  label?: string;
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
    value.tabs.tabs.length > pageLimit ||
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

/**
 * A removal-pending profile is unavailable, so its inventory is authoritative
 * and empty even when the host replies with the loaded tabs (a committed
 * Create can race with removal). The raw reply is only used to prove the
 * original receipt; reconciliation and completion handling see no pages.
 */
function pendingInventory(value: PageInventory): PageInventory {
  if (!value.state.removal_pending || !value.state.tabs.tabs.length)
    return value;
  return {
    ...value,
    state: { ...value.state, tabs: { tabs: [], selected: "" } },
    rawPageIds: value.state.tabs.tabs.map((page) => page.id),
  };
}

/**
 * A rejected receipt proof. A removal-pending reply still carries an
 * independently validated (empty) inventory, so the profile state can be
 * reconciled while the receipt itself stays rejected and unsettled.
 */
class BrowserProofError extends Error {
  constructor(
    message: string,
    readonly inventory?: PageInventory,
  ) {
    super(message);
  }
}

/** Proves a native page reply against the operation that requested it. */
function pageInventory(
  value: PageInventory,
  operation?: PageOperation,
): PageInventory {
  browserState(value?.state);
  const reject = (message: string): never => {
    throw new BrowserProofError(
      message,
      value.state.removal_pending
        ? { ...pendingInventory(value), receipt: null }
        : undefined,
    );
  };
  const receipt = value.receipt;
  if (!operation) {
    if (receipt !== null) reject("Browser inventory proof is invalid.");
    return pendingInventory(value);
  }
  if (receipt === null) return pendingInventory(value);
  if (
    !receipt ||
    receipt.operation_id !== operation.operation_id ||
    !idPattern.test(receipt.page_id) ||
    !Object.values(PagePhase).includes(receipt.phase) ||
    typeof receipt.metadata_committed !== "boolean" ||
    typeof receipt.native_cleanup_complete !== "boolean" ||
    (receipt.failure !== null && typeof receipt.failure !== "string") ||
    (operation.action === PageAction.Close &&
      receipt.page_id !== operation.page_id)
  )
    reject("Browser operation proof is invalid.");
  if (receipt.phase === PagePhase.Completed) {
    const listed = value.state.tabs.tabs.some(
      (page) => page.id === receipt.page_id,
    );
    // The original receipt is validated independently of the current profile
    // availability. A removal-pending inventory is treated as empty, so a
    // completed Create is not required to be listed there; a Close proof is
    // still checked against the raw listing.
    if (
      receipt.failure !== null ||
      !receipt.metadata_committed ||
      !receipt.native_cleanup_complete ||
      (operation.action === PageAction.Close
        ? listed
        : !listed && !value.state.removal_pending)
    )
      reject("Browser operation proof is invalid.");
  }
  return pendingInventory(value);
}

/** A command failure that proves nothing was admitted may be retried freshly. */
function pageOperationWasNotAdmitted(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    (error as { kind?: unknown }).kind === "not-admitted"
  );
}

interface TabbedBrowser {
  /** The session view is the visible one. */
  active?: boolean;
  /** The New browser page dialog is requested. */
  dialogOpen?: boolean;
  dismissDialog?: () => void;
  handle?: RefObject<BrowserHandle | undefined>;
  /** Shared status band below the workspace tabs. */
  statusTarget?: HTMLElement | null;
  selectedPage?: PageDescriptor;
}
function BrowserHeader({ entrance }: { entrance?: EntranceProps }) {
  const [information, setInformation] = useState(false);
  return (
    <>
      <header className="browser-header" {...entrance}>
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
function pageDescriptors(
  profile: string,
  state: BrowserState,
): { id: string; title: string; label: string }[] {
  return state.tabs.tabs.map((page) => ({
    id: page.id,
    title: browserTabTitle(page.url),
    label: page.url,
  }));
}

function NewPageDialog({
  address,
  setAddress,
  close,
  submit,
  disabled,
  guidance,
  children,
}: {
  address: string;
  setAddress: (value: string) => void;
  close: () => void;
  submit: () => void;
  disabled: boolean;
  guidance?: string;
  children?: ReactNode;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <Modal
      title={copy("session-browser.newPage")}
      close={close}
      className="browser-new-page"
      initialFocus={input}
      trapFocus
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!disabled) submit();
        }}
      >
        <p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p>
        <label>
          {copy("session-browser.address_56ef8f")}
          <input
            ref={input}
            value={address}
            maxLength={8192}
            onChange={(event) => setAddress(event.target.value)}
            placeholder="https://example.com/"
          />
        </label>
        {guidance ? <p role="status">{guidance}</p> : null}
        {children}
        <footer>
          <button type="button" onClick={close}>
            {copy("session-browser.cancel")}
          </button>
          <button type="submit" disabled={disabled || !validAddress(address)}>
            {copy("session-browser.openPage")}
          </button>
        </footer>
      </form>
    </Modal>
  );
}

export function SessionBrowser({
  session,
  accountId,
  active = true,
  dialogOpen = false,
  dismissDialog = () => {},
  handle,
  statusTarget,
  selectedPage,
}: {
  session: Resource;
  accountId: string;
} & TabbedBrowser) {
  useLocale();
  const available = useBrowserHost();
  if (!available)
    return dialogOpen ? (
      <NewPageDialog
        address=""
        setAddress={() => {}}
        close={dismissDialog}
        submit={() => {}}
        disabled
        guidance={copy(
          "session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8",
        )}
      />
    ) : null;
  return (
    <NativeSessionBrowser
      session={session}
      accountId={accountId}
      active={active}
      dialogOpen={dialogOpen}
      dismissDialog={dismissDialog}
      handle={handle}
      statusTarget={statusTarget}
      selectedPage={selectedPage}
    />
  );
}
function NativeSessionBrowser({
  session,
  accountId,
  active = true,
  dialogOpen = false,
  dismissDialog = () => {},
  handle,
  statusTarget,
  selectedPage,
}: {
  session: Resource;
  accountId: string;
} & TabbedBrowser) {
  useLocale();
  const queryClient = useQueryClient();
  // An explicit activation is the rising edge of "the session is visible and a
  // page is selected", derived from props only. It never follows the
  // asynchronous inventory, native observation or `needsPresentation`, so a
  // late result cannot replay it. The Y2 entrance moves only the static
  // header, never the native viewport, its layout or hit-test containers.
  const presenting = active && Boolean(selectedPage);
  const presentations = useActivationCount(presenting);
  const headerEntrance = useEntranceActivation(
    `${session.id}:browser:${presentations}`,
    presenting,
    EntranceMotion.Y2,
    // The panel stays mounted while hidden, so leaving settles an in-flight
    // entrance instead of leaving the attribute on an inactive tool.
    true,
  );
  const [address, setAddress] = useState("");
  const [draft, setDraft] = useState("");
  const [profileId, setProfileId] = useState<string>();
  const [state, setState] = useState<BrowserState>();
  const [failure, setFailure] = useProductMessage();
  // Create/Close problems stay with their operation, outside the page pane.
  const [operationFailure, setOperationFailure] = useProductMessage();
  const [inventoryFailure, setInventoryFailure] = useProductMessage();
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const [operation, setOperation] = useState<PageOperation>();
  const operationRef = useRef<PageOperation | undefined>(undefined);
  const nativeBusy = useRef(false);
  // A read that overlaps a mutation is discarded: only a read that began after
  // the last mutation settled may align the descriptors.
  const mutationEpoch = useRef(0);
  const [profilePage, setProfilePage] = useState("");
  // The terminal enumeration proof for one account; cleared whenever the walk
  // continues or is explicitly retried.
  const [proof, setProof] = useState<{
    accountId: string;
    kind: DiscoveryProof;
  }>();
  // Re-runs the read-only initial inventory after a failed read.
  const [inventoryRetry, setInventoryRetry] = useState(0);
  // Every settled profile page of the current walk, keyed by its request token.
  const profileWalk = useRef(
    new Map<string, { matches: BrowserProfile[]; next: string }>(),
  );
  const needsPresentation = useRef(false);
  needsPresentation.current = Boolean(
    selectedPage &&
    selectedPage.profile === profileId &&
    state?.tabs.tabs.some((tab) => tab.id === selectedPage.id),
  );
  const viewport = useRef<HTMLDivElement>(null);
  const alive = useRef(true);
  const presentation = useRef("");
  const shortcutAdmission = useRef("");
  const tabsStore = useSessionTabsStore();
  // Identifies this view as the holder of its unsettled Close in the shared
  // store, so every Session keeps the page descriptor until the proof settles.
  const closeOwner = useRef({});
  const heldClose = useRef<{ profile: string; page: string }>(undefined);
  // Resolves once sibling product windows recorded this view's pending Close.
  const closeSynced = useRef<Promise<void>>(Promise.resolve());
  const closeFocus = useRef<CloseFocus>(undefined);
  const releaseClose = () => {
    const held = heldClose.current;
    heldClose.current = undefined;
    if (held)
      tabsStore.releasePendingClose(
        held.profile,
        held.page,
        closeOwner.current,
      );
  };
  const activeRef = useRef(active);
  activeRef.current = active;
  const selectedPageRef = useRef(selectedPage);
  selectedPageRef.current = selectedPage;
  const dialogVisible = useRef(dialogOpen);
  dialogVisible.current = dialogOpen;
  // The last shared-selection reconciliation sent natively, so a failed
  // SelectTab is never retried without a new selection or native state.
  const reconciled = useRef("");
  const reconciledSelection = useRef("");
  // The view ID whose native child exists. `presentation` is claimed when an
  // open starts, so selection reconciliation waits for this to be published.
  const [nativeView, setNativeView] = useState("");
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
        setFailure(undefined);
      } catch {
        setOperationFailure(ownedMessage("session-browser.extra.5092c54369dd"));
        // Registration was accepted but proves no usable profile.
        operationRef.current = undefined;
        setOperation(undefined);
      }
    },
  );
  // Reading the retained profiles never registers one: restoration and the
  // initial inventory are read-only.
  const profiles = useQuery(
    BrowserQuery.listBrowserProfiles,
    { pageSize: 100, pageToken: profilePage },
    {
      retry: false,
      enabled: active && supported && !profileId && idPattern.test(accountId),
    },
  );
  useEffect(() => {
    // A cached earlier read may predate a registration; only a settled read
    // can establish that this account has no profile.
    if (!profiles.data || profiles.isFetching || profileId) return;
    try {
      const matching = profiles.data.profiles.filter(
        (profile) => profile.accountId === accountId,
      );
      // Pages are keyed by their request token, so re-processing the same
      // settled page is idempotent. The first page has the empty token.
      profileWalk.current.set(profilePage, {
        matches: matching,
        next: profiles.data.nextPageToken,
      });
      const walked = [...profileWalk.current.values()];
      const found = walked.flatMap((page) => page.matches);
      if (found.length > 1)
        throw new Error("Browser profile proof is unavailable.");
      const next = profiles.data.nextPageToken;
      if (next) {
        // A found profile stays a candidate: a later page could still hold a
        // second matching profile, so only the terminal page admits it. The
        // walk is bounded to 64 pages, so a token returned by page 64 or a
        // repeated token is invalid proof.
        if (profileWalk.current.has(next) || profileWalk.current.size >= 64)
          throw new Error("Browser profile proof is unavailable.");
        setProof(undefined);
        setProfilePage(next);
      } else if (found.length) {
        setProfileId(browserProfile(found[0], accountId));
      } else {
        // This account has no retained profile, so no page descriptor of an
        // earlier account's profile can remain in the workspace.
        tabsStore.reconcilePages(session.id, "", []);
        setProof({ accountId, kind: DiscoveryProof.Absent });
      }
    } catch {
      setFailure(ownedMessage("session-browser.extra.5092c54369dd"));
      setProof({ accountId, kind: DiscoveryProof.Failed });
    }
  }, [profiles.data, profiles.isFetching, profileId, accountId]);
  const currentProof = proof?.accountId === accountId ? proof.kind : undefined;
  const discovery: ProfileDiscovery = profileId
    ? ProfileDiscovery.Found
    : profiles.isFetching
      ? profilePage
        ? ProfileDiscovery.Paginating
        : ProfileDiscovery.Loading
      : profiles.error || currentProof === DiscoveryProof.Failed
        ? ProfileDiscovery.Failed
        : currentProof === DiscoveryProof.Absent
          ? ProfileDiscovery.Absent
          : profilePage
            ? ProfileDiscovery.Paginating
            : ProfileDiscovery.Loading;
  // A discovered profile also needs an observed inventory: an unread inventory
  // is unknown, never an empty one.
  const inventoryKnown = state !== undefined;
  // A failed inventory read invalidates the earlier observation until a later
  // read succeeds, so a stale inventory never admits a Create.
  const admitted = profileId
    ? inventoryKnown && !inventoryFailure
    : discovery === ProfileDiscovery.Absent;
  // Re-reads profiles from the first page only; no mutation is repeated.
  const restartDiscovery = (
    phase: "discovery_retry" | "discovery_reactivated",
  ) => {
    console.info("delidev.browser_profiles", { phase });
    setFailure(undefined);
    setProof(undefined);
    profileWalk.current.clear();
    // Successful profile pages stay fresh in the query cache, so a restarted
    // walk would replay them without any list request. Invalidate every cached
    // page first; the invalidated first-page query then refetches on mount.
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: BrowserQuery.listBrowserProfiles,
        cardinality: "finite",
      }),
      refetchType: "none",
    });
    if (profilePage) setProfilePage("");
    else void profiles.refetch();
  };
  const retryDiscovery = () => restartDiscovery("discovery_retry");
  // Re-enabling the query refetches only the current page, which would pair a
  // fresh page with entries retained from before the session was inactive, so
  // a reactivated session restarts its unresolved walk from the first page.
  const wasActive = useRef(active);
  useEffect(() => {
    const reactivated = active && !wasActive.current;
    wasActive.current = active;
    if (reactivated && supported && !profileId && idPattern.test(accountId))
      restartDiscovery("discovery_reactivated");
  }, [active]);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      // An abandoned operation can no longer be settled by this view.
      releaseClose();
    };
  }, []);
  const focusSelectedTab = () => {
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
  const closePageWithFocus = (original: PageOperation, profile: string) => {
    const intent = closeFocus.current;
    const transfer =
      intent?.operationId === original.operation_id &&
      activeRef.current &&
      tabsStore.navigation(session.id) === intent.navigation &&
      document.activeElement === intent.control;
    tabsStore.closePageEverywhere(profile, original.page_id!);
    if (!transfer) return;
    const target = tabsStore.snapshot(session.id).selected;
    requestAnimationFrame(() => {
      if (!alive.current || !activeRef.current) return;
      const current = tabsStore.snapshot(session.id);
      if (current.selected !== target) return;
      // Removing the focused control may reset activeElement to body. A later
      // focus on any surviving control belongs to the user and wins.
      if (
        document.activeElement !== intent.control &&
        document.activeElement !== document.body
      )
        return;
      const index = current.tabs.findIndex(
        (tab) => sessionTabKey(tab) === target,
      );
      const fallback = document.getElementById(
        `session-tab-${session.id}-${index}`,
      );
      if (fallback?.isConnected && !fallback.closest("[hidden], [inert]"))
        fallback.focus({ preventScroll: true });
    });
  };
  // Pages the current operation still owns keep their descriptors until the
  // original proof settles.
  const ownedPages = () =>
    new Set(
      operationRef.current?.action === PageAction.Close &&
        operationRef.current.page_id
        ? [operationRef.current.page_id]
        : [],
    );
  const observeInventory = (value: PageInventory, profile: string) => {
    setState(value.state);
    // A removal-pending inventory is authoritative and empty, so it drops this
    // profile's stale Page descriptors like any other inventory. Reconciliation
    // is store-only and never sends a native mutation.
    tabsStore.reconcilePages(
      session.id,
      profile,
      pageDescriptors(profile, value.state),
      ownedPages(),
    );
    // Inactive retained Sessions do not poll, so the same authoritative proof
    // also drops this profile's unheld Page descriptors from their snapshots.
    if (value.state.removal_pending)
      tabsStore.reconcileUnavailableProfile(profile, ownedPages());
  };
  const finishOperation = () => {
    releaseClose();
    closeFocus.current = undefined;
    operationRef.current = undefined;
    setOperation(undefined);
  };
  const runOperation = async (original: PageOperation, observe: boolean) => {
    if (
      !profileId ||
      nativeBusy.current ||
      !alive.current ||
      operationRef.current?.operation_id !== original.operation_id ||
      (!observe && original.sent)
    )
      return;
    nativeBusy.current = true;
    mutationEpoch.current += 1;
    setBusy(true);
    if (!observe) original.sent = true;
    try {
      // Sibling windows must own the unsettled Close before the native page can
      // disappear from their inventory reads.
      if (!observe && original.action === PageAction.Close) {
        await closeSynced.current;
        if (
          !alive.current ||
          operationRef.current?.operation_id !== original.operation_id
        )
          return;
        // A pagehide or disposal during the wait withdrew the hold; sending
        // the native Close now would leave siblings without an owner.
        if (
          !heldClose.current ||
          !tabsStore.holdsPendingClose(
            heldClose.current.profile,
            heldClose.current.page,
            closeOwner.current,
          )
        ) {
          original.sent = false;
          finishOperation();
          setOperationFailure(
            ownedMessage("session-browser.extra.51fee7071e2b"),
          );
          return;
        }
        // The barrier passed: the native Close is dispatched below, so its
        // hold must survive a bfcache suspension.
        tabsStore.markPendingCloseSent(
          heldClose.current.profile,
          heldClose.current.page,
          closeOwner.current,
        );
      }
      const value = pageInventory(
        await invoke<PageInventory>("browser_pages", {
          profileId,
          request: observe
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
      const receipt = value.receipt;
      if (!receipt) {
        // Observation found no original admission: the lost request never
        // reached the native command, so nothing can have changed. The
        // inventory proves the page's state, never a repeated mutation.
        observeInventory(value, profileId);
        if (
          original.action === PageAction.Close &&
          // The raw listing, not the normalized empty inventory, proves absence.
          !(
            value.rawPageIds ?? value.state.tabs.tabs.map((page) => page.id)
          ).includes(original.page_id!)
        ) {
          closePageWithFocus(original, profileId);
          finishOperation();
        } else {
          finishOperation();
          setOperationFailure(
            ownedMessage("session-browser.extra.51fee7071e2b"),
          );
        }
        return;
      }
      original.receipt = receipt;
      original.uncertain = false;
      const before = tabsStore.snapshot(session.id);
      observeInventory(value, profileId);
      setOperationFailure(undefined);
      if (receipt.phase === PagePhase.Completed) {
        if (original.action === PageAction.Close) {
          closePageWithFocus(original, profileId);
        } else {
          const page = value.state.tabs.tabs.find(
            (value) => value.id === receipt.page_id,
          );
          if (page) {
            // The confirmed page is the one explicit selection, unless the
            // user has since moved to another tab with the dialog dismissed.
            if (
              dialogVisible.current ||
              before.selected === original.selection
            ) {
              tabsStore.open(session.id, {
                kind: SessionTabKind.Page,
                profile: profileId,
                id: page.id,
                title: browserTabTitle(page.url),
                label: page.url,
              });
              focusSelectedTab();
            }
            setDraft("");
            if (dialogVisible.current) dismissDialog();
          }
        }
        finishOperation();
      } else {
        setOperation({ ...original });
        if (receipt.phase === PagePhase.Failed) {
          // The mutation is proven not to have been published: a fresh
          // operation is safe.
          finishOperation();
          setOperationFailure(
            ownedMessage("session-browser.extra.51fee7071e2b"),
          );
        }
      }
    } catch (error) {
      if (
        alive.current &&
        operationRef.current?.operation_id === original.operation_id
      ) {
        if (!observe && pageOperationWasNotAdmitted(error)) {
          finishOperation();
          setOperationFailure(
            ownedMessage("session-browser.extra.51fee7071e2b"),
          );
          console.warn("delidev.browser_page", {
            phase: "rejected_before_admission",
            action: original.action,
          });
          return;
        }
        // The receipt stays rejected and the operation unsettled, but the
        // independently validated removal-pending inventory still drops stale
        // sibling descriptors; the closing page stays owned by `ownedPages`.
        if (error instanceof BrowserProofError && error.inventory)
          observeInventory(error.inventory, profileId);
        original.uncertain = true;
        setOperation({ ...original });
        setOperationFailure(ownedMessage("session-browser.operationUnknown"));
        console.warn("delidev.browser_page", {
          phase: "observation_unavailable",
          action: original.action,
        });
      }
    } finally {
      nativeBusy.current = false;
      mutationEpoch.current += 1;
      if (alive.current) setBusy(false);
    }
  };
  const runner = useRef(runOperation);
  runner.current = runOperation;
  const startOperation = (next: PageOperation) => {
    releaseClose();
    closeFocus.current = undefined;
    if (next.action === PageAction.Close && next.page_id && profileId) {
      const snapshot = tabsStore.snapshot(session.id);
      const key = sessionTabKey({
        kind: SessionTabKind.Page,
        profile: profileId,
        id: next.page_id,
        title: "",
      });
      const index = snapshot.tabs.findIndex(
        (tab) => sessionTabKey(tab) === key,
      );
      const control = document.activeElement;
      if (
        activeRef.current &&
        index >= 0 &&
        control &&
        document
          .getElementById(`session-tab-${session.id}-${index}`)
          ?.parentElement?.contains(control)
      )
        closeFocus.current = {
          operationId: next.operation_id,
          control,
          navigation: tabsStore.navigation(session.id),
        };
      heldClose.current = { profile: profileId, page: next.page_id };
      closeSynced.current = tabsStore.holdPendingClose(
        profileId,
        next.page_id,
        closeOwner.current,
      );
    }
    operationRef.current = next;
    setOperation(next);
    setOperationFailure(undefined);
  };
  const submitPage = () => {
    if (
      !validAddress(draft) ||
      operationRef.current ||
      !active ||
      !supported ||
      !idPattern.test(accountId) ||
      registration.busy ||
      registration.uncertain ||
      !admitted ||
      state?.removal_pending ||
      (state?.tabs.tabs.length ?? 0) >= pageLimit
    )
      return;
    startOperation({
      action: PageAction.Create,
      operation_id: newRequestId(),
      url: draft,
      sent: false,
      uncertain: false,
      selection: tabsStore.snapshot(session.id).selected,
    });
    // An existing profile is reused; a missing one is registered only now.
    if (!profileId)
      void registration.send({
        session: {
          id: session.id,
          expectedRevision: session.revision,
          requestId: newRequestId(),
        },
        accountId,
      });
  };
  // The unsent Create waits for registration to produce its profile.
  useEffect(() => {
    if (!profileId || !operation || operation.sent) return;
    void runner.current(operation, false);
  }, [profileId, operation]);
  useEffect(() => {
    if (
      operation &&
      !operation.sent &&
      !profileId &&
      registration.error &&
      !registration.busy &&
      !registration.uncertain
    ) {
      finishOperation();
    }
  }, [operation, profileId, registration.error, registration.busy]);
  useEffect(() => {
    if (!handle) return;
    handle.current = {
      closePage: (profile, pageId) => {
        if (
          !profileId ||
          profile !== profileId ||
          !alive.current ||
          state?.removal_pending
        )
          return;
        if (operationRef.current || nativeBusy.current) {
          setOperationFailure(ownedMessage("session-browser.busyOperation"));
          return;
        }
        const next: PageOperation = {
          action: PageAction.Close,
          operation_id: newRequestId(),
          page_id: pageId,
          sent: false,
          uncertain: false,
        };
        startOperation(next);
      },
    };
    return () => {
      handle.current = undefined;
    };
  }, [profileId, handle, state?.removal_pending]);
  // Native-local reads: one inventory when the view becomes active, then a
  // periodic read-only refresh. A pending original operation is observed
  // instead; observation never repeats its mutation.
  useEffect(() => {
    if (!profileId || (!active && !operationRef.current)) return;
    let disposed = false,
      reading = false;
    const observePending = async () => {
      const pending = operationRef.current;
      if (disposed || nativeBusy.current || !pending?.sent) return;
      await runner.current(pending, true);
    };
    const readInventory = async () => {
      if (
        disposed ||
        reading ||
        nativeBusy.current ||
        operationRef.current ||
        !activeRef.current
      )
        return;
      reading = true;
      const epoch = mutationEpoch.current;
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
          !nativeBusy.current &&
          epoch === mutationEpoch.current &&
          !operationRef.current
        ) {
          observeInventory(value, profileId);
          setInventoryFailure(undefined);
        }
      } catch (error) {
        if (
          error instanceof BrowserProofError &&
          error.inventory &&
          !disposed &&
          alive.current &&
          !nativeBusy.current &&
          epoch === mutationEpoch.current &&
          !operationRef.current
        )
          observeInventory(error.inventory, profileId);
        if (!disposed)
          setInventoryFailure(
            ownedMessage("session-browser.extra.c6d1ec80c1c4"),
          );
      } finally {
        reading = false;
      }
    };
    void readInventory();
    void observePending();
    const operations = window.setInterval(() => void observePending(), 1000);
    const inventories = window.setInterval(
      () => void readInventory(),
      inventoryInterval,
    );
    return () => {
      disposed = true;
      window.clearInterval(operations);
      window.clearInterval(inventories);
    };
  }, [profileId, active, Boolean(operation), inventoryRetry]);
  // The toolbar address follows the selected page's observed URL.
  const selectedUrl = state?.tabs.tabs.find(
    (page) =>
      page.id === selectedPage?.id && selectedPage?.profile === profileId,
  )?.url;
  useEffect(() => {
    if (selectedUrl) setAddress(selectedUrl);
  }, [selectedPage?.id, selectedUrl]);
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
        if (!disposed) setNativeView("");
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
        const page = selectedPageRef.current;
        if (!page) return;
        opening = true;
        const viewId = newRequestId();
        ownedViewId = viewId;
        presentation.current = viewId;
        try {
          // Presentation restores one retained page by identity; it never
          // creates an address or a page.
          const result = browserState(
            await invoke<BrowserState>("open_browser", {
              profileId,
              viewId,
              pageId: page.id,
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
          setNativeView(viewId);
          setState(result);
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
        try {
          const result = browserState(
            await invoke<BrowserState>("control_browser", {
              profileId,
              viewId,
              action: BrowserAction.Resize,
              bounds: area,
            }),
          );
          if (!disposed && opened && presentation.current === viewId) {
            lastBounds = geometry;
            setState(result);
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
      if (!opened || disposed || !visible()) return;
      void invoke<BrowserState>("browser_state", {
        profileId,
        viewId: presentation.current,
      })
        .then((value) => {
          if (!disposed) setState(browserState(value));
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
      setNativeView("");
      void hide();
    };
    // Reopening after a modal restores the native page without another registration.
  }, [profileId, retry]);
  const control = async (action: BrowserAction, tabId?: string) => {
    if (!profileId || busy || state?.removal_pending || !presentation.current)
      return;
    setBusy(true);
    setFailure(undefined);
    try {
      const result = browserState(
        await invoke<BrowserState>("control_browser", {
          profileId,
          viewId: presentation.current,
          action,
          url: address,
          tabId,
        }),
      );
      if (alive.current) setState(result);
    } catch {
      if (alive.current)
        setFailure(ownedMessage("session-browser.extra.51fee7071e2b"));
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  useEffect(() => {
    const selection = selectedPage
      ? sessionTabKey({ kind: SessionTabKind.Page, ...selectedPage })
      : "";
    if (reconciledSelection.current !== selection) {
      // A failed attempt belongs to one continuous shared selection, including
      // departures to non-page tabs while the browser manager stays mounted.
      reconciledSelection.current = selection;
      reconciled.current = "";
    }
    if (
      !presentation.current ||
      nativeView !== presentation.current ||
      !active ||
      !selectedPage ||
      selectedPage.profile !== profileId ||
      !state ||
      state.tabs.selected === selectedPage.id ||
      !state.tabs.tabs.some((tab) => tab.id === selectedPage.id)
    ) {
      if (state && selectedPage && state.tabs.selected === selectedPage.id)
        reconciled.current = "";
      return;
    }
    // A busy action drops SelectTab; busy is a dependency so the current shared
    // selection is reconciled once when the action settles.
    if (busy) return;
    // A replacement native view is a new reconciliation opportunity even when
    // the shared selection and observed native tab are unchanged. This covers
    // returning while the previous view's asynchronous Hide is still pending.
    const attempt = `${profileId}:${selectedPage.id}:${state.tabs.selected}:${nativeView}`;
    if (reconciled.current === attempt) return;
    reconciled.current = attempt;
    void control(BrowserAction.SelectTab, selectedPage.id);
  }, [
    active,
    selectedPage?.id,
    selectedPage?.profile,
    profileId,
    state?.tabs.selected,
    busy,
    nativeView,
  ]);
  useEffect(() => {
    // Shared labels follow the latest observed URL of owned pages only; the
    // store ignores unknown pages and unchanged labels.
    if (!profileId || !state) return;
    for (const tab of state.tabs.tabs)
      tabsStore.updatePage(session.id, {
        profile: profileId,
        id: tab.id,
        title: browserTabTitle(tab.url),
        label: tab.url,
      });
  }, [state, profileId]);
  useEffect(() => {
    if (!profileId) return;
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
    if (!profileId) return;
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
          active && !shortcutModalVisible()
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
  }, [active, profileId, state?.tabs.selected, tabsStore, session.id]);
  const blocked =
    !active ||
    busy ||
    registration.busy ||
    registration.uncertain ||
    state?.removal_pending;
  const checkStatus = () => {
    const pending = operationRef.current;
    if (pending?.sent) void runner.current(pending, true);
  };
  const operationNotice = operation?.sent ? (
    operation.uncertain ||
    operation.receipt?.phase === PagePhase.CleanupPending ? (
      <div className="browser-operation" role="alert">
        <p>
          {copy(
            operation.uncertain
              ? "session-browser.operationUnknown"
              : "session-browser.cleanupPending",
          )}
        </p>
        <button type="button" disabled={busy} onClick={checkStatus}>
          {copy("session-browser.checkStatus")}
        </button>
      </div>
    ) : (
      <div className="browser-operation" role="status">
        <p>
          {copy(
            operation.action === PageAction.Close
              ? "session-browser.closing"
              : "session-browser.opening",
          )}
        </p>
      </div>
    )
  ) : null;
  // A close problem stays visible below the workspace tabs for any selected
  // pane; create problems are presented by their dialog.
  const stripNotice =
    operation?.action === PageAction.Close ? operationNotice : null;
  const stripFailure =
    operationFailure && !operation?.uncertain && !dialogOpen ? (
      <div className="browser-operation" role="alert">
        <p>{operationFailure}</p>
        <button type="button" onClick={() => setOperationFailure(undefined)}>
          {copy("session-browser.dismiss")}
        </button>
      </div>
    ) : null;
  const statusContent =
    stripNotice || stripFailure ? (
      <div className="terminal-status-strip browser-status-strip">
        {stripNotice}
        {stripFailure}
      </div>
    ) : null;
  const atLimit = (state?.tabs.tabs.length ?? 0) >= pageLimit;
  const createPending = operation?.action === PageAction.Create;
  const guidance = !idPattern.test(accountId)
    ? copy("session-browser.accountRequired")
    : !supported
      ? capabilities.error
        ? undefined
        : copy(
            "session-browser.protectedBrowserProfilesAreUnavailableUntil_033b80",
          )
      : state?.removal_pending
        ? copy("session-browser.thisAccountWasDeletedItsProfile_4cf6fc")
        : atLimit
          ? copy("session-browser.pageLimit")
          : profileId
            ? inventoryKnown || inventoryFailure
              ? undefined
              : copy("session-browser.inventoryPending")
            : discovery === ProfileDiscovery.Failed
              ? copy("session-browser.discoveryFailed")
              : discovery === ProfileDiscovery.Absent
                ? undefined
                : copy("session-browser.discoveryPending");
  return (
    <>
      {statusContent
        ? statusTarget
          ? createPortal(statusContent, statusTarget)
          : statusContent
        : null}
      {profileId ? (
        <div
          className="session-app-panel"
          hidden={!active || !needsPresentation.current}
          inert={!active || !needsPresentation.current}
        >
          <section
            className="session-browser browser-registered"
            aria-label={copy("session-browser.sessionBrowser_47d746")}
          >
            <div className="browser-chrome">
              <BrowserHeader entrance={headerEntrance} />
              {failure ? <p role="alert">{failure}</p> : null}
              {inventoryFailure ? <p role="alert">{inventoryFailure}</p> : null}
              {state?.removal_pending ? (
                <p role="alert">
                  {copy(
                    "session-browser.thisAccountWasDeletedItsProfile_4cf6fc",
                  )}
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
                      setRetry((n) => n + 1);
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
              hidden={!needsPresentation.current}
              aria-label={copy(
                "session-browser.untrustedBrowserContent_9c54bc",
              )}
            />
          </section>
        </div>
      ) : null}
      {dialogOpen ? (
        <NewPageDialog
          address={draft}
          setAddress={setDraft}
          close={dismissDialog}
          submit={submitPage}
          disabled={Boolean(
            createPending ||
            !active ||
            !supported ||
            !idPattern.test(accountId) ||
            registration.busy ||
            registration.uncertain ||
            !admitted ||
            state?.removal_pending ||
            atLimit ||
            (operation && operation.action !== PageAction.Create),
          )}
          guidance={guidance}
        >
          <Problem
            error={capabilities.error}
            actions={
              <button
                type="button"
                disabled={capabilities.isFetching}
                onClick={() => void capabilities.refetch()}
              >
                {copy("session-browser.retryCapability")}
              </button>
            }
          />
          {registration.uncertain ? (
            <button
              type="button"
              disabled={registration.busy}
              onClick={registration.retry}
            >
              {copy("session-browser.retryTheSameRegistration_c86ef9")}
            </button>
          ) : null}
          <Problem error={registration.error} />
          {!profileId && discovery === ProfileDiscovery.Failed ? (
            <button
              type="button"
              disabled={profiles.isFetching}
              onClick={retryDiscovery}
            >
              {copy("session-browser.retryProfileRead")}
            </button>
          ) : null}
          {profileId && inventoryFailure ? (
            <>
              <p role="alert">{inventoryFailure}</p>
              <button
                type="button"
                onClick={() => {
                  // The failure stays until a read succeeds and replaces it.
                  setInventoryRetry((n) => n + 1);
                }}
              >
                {copy("session-browser.retryInventoryRead")}
              </button>
            </>
          ) : null}
          {operationFailure ? <p role="alert">{operationFailure}</p> : null}
          {createPending ? operationNotice : null}
        </NewPageDialog>
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
      raw.length <= 8192
    );
  } catch {
    return false;
  }
}
