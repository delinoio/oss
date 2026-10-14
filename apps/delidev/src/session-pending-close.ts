// SPDX-License-Identifier: Apache-2.0

/** Same-origin broadcast name shared by every product window of one origin. */
const channelName = "delidev.browser.pending-close";
const heartbeatMs = 5000;
const expiryMs = heartbeatMs * 3;
/**
 * How long a sibling may stay silent before the Close re-checks whether it is
 * still alive. A silent window proven alive by its liveness lock keeps being
 * awaited; only a window without that proof is presumed gone.
 */
const syncTimeoutMs = 1000;
/**
 * Without the Web Locks API there is no authoritative window list, so a Close
 * also waits this long for windows whose handshake has not arrived yet.
 */
const discoveryMs = 100;

enum PendingCloseMessageKind {
  Hold = "hold",
  Release = "release",
  /**
   * The original Close was proven complete: the page is gone everywhere, so
   * receivers drop the hold and remove the descriptor from retained Sessions.
   */
  Settled = "settled",
  Query = "query",
  /** A window announces itself so siblings can wait for its acknowledgement. */
  Presence = "presence",
  /** Acknowledges that the target window recorded the announced hold. */
  Ack = "ack",
  /** A window is going away. */
  Bye = "bye",
}

interface PendingCloseMessage {
  scope: typeof channelName;
  kind: PendingCloseMessageKind;
  window: string;
  key?: string;
  /** On a Hold: the sender waits for an Ack from every known window. */
  ack?: boolean;
  /**
   * Unique identity of one Hold announcement. An Ack echoes it, so a delayed
   * Ack for an earlier announcement of the same key is never credited to a
   * later one.
   */
  id?: string;
  /** On an Ack: the window whose Hold is acknowledged. */
  to?: string;
}

/** The slice of BroadcastChannel the registry needs (injectable in tests). */
export interface PendingCloseChannel {
  postMessage(message: unknown): void;
  onmessage: ((event: { data: unknown }) => void) | null;
  close(): void;
}

/**
 * The slice of the Web Locks API used as a liveness source. A lock held by a
 * window is released by the browser when that window closes or crashes, and
 * unlike renderer timers it is not throttled while the window is hidden.
 */
export interface PendingCloseLocks {
  /** Aborting `signal` withdraws a request that has not been granted yet. */
  request(
    name: string,
    options: { signal: AbortSignal },
    callback: () => Promise<unknown>,
  ): Promise<unknown>;
  query(): Promise<{
    held?: { name?: string }[];
    pending?: { name?: string }[];
  }>;
}

export interface PendingCloseOptions {
  /** `null` disables the Web Locks liveness source. Defaults to navigator.locks. */
  locks?: PendingCloseLocks | null;
  /** `null` disables cross-window sharing. Defaults to a BroadcastChannel. */
  channel?: PendingCloseChannel | null;
  now?: () => number;
  window?: string;
}

function defaultChannel(): PendingCloseChannel | null {
  try {
    if (typeof BroadcastChannel === "undefined") return null;
    const channel = new BroadcastChannel(channelName);
    // A Node-hosted channel must never keep a test process alive.
    (channel as unknown as { unref?: () => void }).unref?.();
    return channel as unknown as PendingCloseChannel;
  } catch {
    return null;
  }
}

function defaultLocks(): PendingCloseLocks | null {
  try {
    if (typeof navigator === "undefined" || !navigator.locks) return null;
    return navigator.locks as unknown as PendingCloseLocks;
  } catch {
    return null;
  }
}

/**
 * Names of every lock a window holds or has requested. A requested-but-pending
 * lock still proves its window exists, so it counts as live.
 */
function lockNames(state: {
  held?: { name?: string }[];
  pending?: { name?: string }[];
}): string[] {
  const names: string[] = [];
  for (const lock of [...(state.held ?? []), ...(state.pending ?? [])])
    if (typeof lock.name === "string") names.push(lock.name);
  return names;
}

function windowIdentity(): string {
  try {
    return crypto.randomUUID();
  } catch {
    return `${Date.now()}-${Math.random()}`;
  }
}

function isMessage(value: unknown): value is PendingCloseMessage {
  if (typeof value !== "object" || value === null) return false;
  const message = value as Record<string, unknown>;
  return (
    message.scope === channelName &&
    typeof message.window === "string" &&
    Object.values(PendingCloseMessageKind).includes(
      message.kind as PendingCloseMessageKind,
    ) &&
    (message.key === undefined || typeof message.key === "string") &&
    (message.ack === undefined || typeof message.ack === "boolean") &&
    (message.id === undefined || typeof message.id === "string") &&
    (message.to === undefined || typeof message.to === "string")
  );
}

interface Waiter {
  /** The announcement this waiter belongs to; only its Acks count. */
  id: string;
  /** Windows that acknowledged the Hold so far. */
  acked: Set<string>;
  /** Windows whose acknowledgement is required; null until discovery ends. */
  required: Set<string> | null;
  check: () => void;
  done: () => void;
}

/**
 * Tracks unsettled original browser Close operations. A Close is
 * profile-scoped, so the descriptor must survive reconciliation in every
 * Session and every same-origin product window until the owner observes the
 * original proof. Product windows have independent renderer memory, so local
 * holds are announced over a same-origin channel; a window that disappears
 * without releasing stops refreshing its announcement and its hold expires.
 */
export class PendingCloseRegistry {
  private local = new Map<string, Set<object>>();
  private remote = new Map<string, Map<string, number>>();
  private channel: PendingCloseChannel | null = null;
  private timer: ReturnType<typeof setInterval> | undefined;
  private now: () => number;
  private window: string;
  private options: PendingCloseOptions;
  /**
   * False once the window is closing (pagehide) or the registry was disposed.
   * An inactive registry announces nothing, so a Hold that arrives after the
   * Bye can never resurrect this window on a sibling.
   */
  private active = false;
  private onPageHide = () => this.deactivate();
  private onPageShow = (event: Event) => {
    // A page restored from the back/forward cache is alive again.
    if ((event as PageTransitionEvent).persisted && !this.active)
      this.activate();
  };
  private locks: PendingCloseLocks | null = null;
  private unlock: (() => void) | undefined;
  private lockRequest: AbortController | undefined;
  private probing = new Set<string>();
  // Sibling windows seen on the channel, and the Holds still awaiting acks.
  private peers = new Set<string>();
  private syncing = new Map<string, Promise<void>>();
  private announcements = 0;
  private waiters = new Map<string, Waiter>();
  private settledListeners = new Set<(key: string) => void>();

  constructor(options: PendingCloseOptions = {}) {
    this.options = options;
    this.now = options.now ?? Date.now;
    this.window = options.window ?? windowIdentity();
    this.connect();
  }

  /**
   * Opens the cross-window channel. Idempotent while connected; also reopens
   * a registry that was disposed (for example by a development-mode effect
   * replay), so a store can be disposed and reconnected safely.
   */
  connect() {
    if (this.channel) return;
    const { channel } = this.options;
    this.channel = channel === undefined ? defaultChannel() : channel;
    if (!this.channel) return;
    this.channel.onmessage = (event) => this.receive(event.data);
    if (typeof window !== "undefined") {
      window.addEventListener("pagehide", this.onPageHide);
      window.addEventListener("pageshow", this.onPageShow);
    }
    this.activate();
  }

  private activate() {
    this.active = true;
    this.send({ kind: PendingCloseMessageKind.Query });
    this.holdLivenessLock();
  }

  /**
   * Marks the window inactive before anything else, then withdraws every hold,
   * says Bye and gives up the liveness lock (including a request that was not
   * granted yet). Used by both pagehide and disposal.
   */
  private deactivate() {
    this.active = false;
    this.releaseAll();
    this.send({ kind: PendingCloseMessageKind.Bye });
    this.releaseLivenessLock();
  }

  private releaseLivenessLock() {
    this.lockRequest?.abort();
    this.lockRequest = undefined;
    this.unlock?.();
    this.unlock = undefined;
    this.locks = null;
  }

  private lockName(window: string) {
    return `${channelName}:${window}`;
  }

  /** Registers a listener for holds another window proved complete. */
  onSettled(listener: (key: string) => void): () => void {
    this.settledListeners.add(listener);
    return () => this.settledListeners.delete(listener);
  }

  /** Holds this window's liveness lock until disposal or window teardown. */
  private holdLivenessLock() {
    const { locks } = this.options;
    this.locks = locks === undefined ? defaultLocks() : locks;
    if (!this.locks) return;
    this.lockRequest?.abort();
    const controller = new AbortController();
    this.lockRequest = controller;
    try {
      void this.locks
        .request(
          this.lockName(this.window),
          { signal: controller.signal },
          () =>
            new Promise<void>((resolve) => {
              // Granted after cancellation: do not keep the lock.
              if (controller.signal.aborted) resolve();
              else this.unlock = resolve;
            }),
        )
        .catch(() => undefined);
    } catch {
      // Without the lock, siblings fall back to heartbeat expiry for this window.
    }
  }

  /**
   * Checks whether an expired remote holder's window is still alive. A hidden
   * window's throttled heartbeat is not proof of death; only a released
   * liveness lock is.
   */
  private probe(holder: string) {
    if (!this.locks || this.probing.has(holder)) return;
    this.probing.add(holder);
    const name = this.lockName(holder);
    const settle = (alive: boolean) => {
      this.probing.delete(holder);
      for (const [key, holders] of this.remote) {
        if (!holders.has(holder)) continue;
        if (alive) holders.set(holder, this.now() + expiryMs);
        else holders.delete(holder);
        if (!holders.size) this.remote.delete(key);
      }
    };
    void this.locks
      .query()
      .then((state) =>
        settle(lockNames(state).includes(name)),
      )
      .catch(() => settle(false));
  }

  /**
   * Releases every local hold, stops the heartbeat, detaches the page listener
   * and closes the channel, so a discarded registry stays reachable by nothing.
   */
  dispose() {
    if (!this.channel) return;
    this.deactivate();
    if (typeof window !== "undefined") {
      window.removeEventListener("pagehide", this.onPageHide);
      window.removeEventListener("pageshow", this.onPageShow);
    }
    this.channel.onmessage = null;
    try {
      this.channel.close();
    } catch {
      // Closing is best-effort; the handler is already detached.
    }
    this.channel = null;
    this.remote.clear();
    this.probing.clear();
    this.peers.clear();
    for (const waiter of [...this.waiters.values()]) waiter.done();
    this.syncing.clear();
  }

  private send(message: Omit<PendingCloseMessage, "scope" | "window">) {
    // A closing window must never announce a new Hold after its Bye.
    if (!this.active && message.kind === PendingCloseMessageKind.Hold) return;
    try {
      this.channel?.postMessage({
        scope: channelName,
        window: this.window,
        ...message,
      } satisfies PendingCloseMessage);
    } catch {
      // Cross-window sharing is best-effort; this window's holds still apply.
    }
  }

  private receive(data: unknown) {
    if (!isMessage(data) || data.window === this.window) return;
    if (data.kind === PendingCloseMessageKind.Settled) {
      this.peers.add(data.window);
      if (data.key === undefined) return;
      this.remote.delete(data.key);
      for (const listener of [...this.settledListeners]) listener(data.key);
      return;
    }
    if (data.kind === PendingCloseMessageKind.Bye) {
      this.peers.delete(data.window);
      for (const [key, holders] of this.remote) {
        holders.delete(data.window);
        if (!holders.size) this.remote.delete(key);
      }
      // A window that announced its departure can no longer be awaited.
      for (const waiter of this.waiters.values()) {
        waiter.required?.delete(data.window);
        waiter.check();
      }
      return;
    }
    this.peers.add(data.window);
    if (data.kind === PendingCloseMessageKind.Query) {
      this.send({ kind: PendingCloseMessageKind.Presence });
      for (const key of this.local.keys())
        this.send({ kind: PendingCloseMessageKind.Hold, key });
      return;
    }
    if (data.kind === PendingCloseMessageKind.Presence) return;
    if (data.kind === PendingCloseMessageKind.Ack) {
      if (data.to !== this.window || data.key === undefined) return;
      const waiter = this.waiters.get(data.key);
      // Reject an Ack that answers an earlier announcement of this key.
      if (!waiter || waiter.id !== data.id) return;
      waiter.acked.add(data.window);
      waiter.check();
      return;
    }
    if (data.key === undefined) return;
    const holders = this.remote.get(data.key) ?? new Map<string, number>();
    if (data.kind === PendingCloseMessageKind.Hold)
      holders.set(data.window, this.now() + expiryMs);
    else holders.delete(data.window);
    if (holders.size) this.remote.set(data.key, holders);
    else this.remote.delete(data.key);
    // The hold is recorded: tell the announcing window it may proceed.
    if (data.kind === PendingCloseMessageKind.Hold && data.ack)
      this.send({
        kind: PendingCloseMessageKind.Ack,
        key: data.key,
        id: data.id,
        to: data.window,
      });
  }

  /** Windows currently holding a liveness lock, or null when unknowable. */
  private async liveWindows(): Promise<Set<string> | null> {
    if (!this.locks) return null;
    const prefix = this.lockName("");
    try {
      const state = await this.locks.query();
      const live = new Set<string>();
      for (const name of lockNames(state)) {
        if (name.startsWith(prefix) && name !== this.lockName(this.window))
          live.add(name.slice(prefix.length));
      }
      return live;
    } catch {
      return null;
    }
  }

  /**
   * Announces a new hold and resolves once every sibling window has recorded
   * it, so a sibling's reconciliation can never observe the page as unowned
   * after the native Close ran. Each announcement carries a unique id that the
   * siblings echo in their Ack, so only acknowledgements of this exact
   * announcement count. Siblings are the windows already seen on the channel
   * plus, when Web Locks is available, every window currently holding a
   * liveness lock (which covers windows whose handshake has not arrived yet).
   * Without Web Locks, the Close also waits a short discovery period for
   * windows that have not introduced themselves.
   *
   * A silent sibling is not assumed dead merely because time passed: a
   * suspended or throttled window still holds its liveness lock and may resume
   * and reconcile at any moment, so it stays awaited until it acknowledges,
   * says Bye or its lock disappears. Only without a liveness source (no Web
   * Locks, or the lock query failing) does a window that stays silent for the
   * timeout count as gone.
   */
  private announce(key: string): Promise<void> {
    if (!this.channel || !this.active) return Promise.resolve();
    // A superseded announcement of the same key has no owner left to wait.
    this.waiters.get(key)?.done();
    return new Promise<void>((resolve) => {
      let finished = false;
      const timers: ReturnType<typeof setTimeout>[] = [];
      this.announcements += 1;
      const waiter: Waiter = {
        id: `${this.window}:${this.announcements}`,
        acked: new Set(),
        required: null,
        check: () => {
          const { required, acked } = waiter;
          if (required && [...required].every((peer) => acked.has(peer)))
            waiter.done();
        },
        done: () => {
          if (finished) return;
          finished = true;
          for (const timer of timers) clearTimeout(timer);
          if (this.waiters.get(key) === waiter) this.waiters.delete(key);
          resolve();
        },
      };
      const later = (callback: () => void, ms: number) => {
        const timer = setTimeout(callback, ms);
        (timer as unknown as { unref?: () => void }).unref?.();
        timers.push(timer);
      };
      this.waiters.set(key, waiter);
      // Every window listening now receives the Hold and answers it.
      this.send({
        kind: PendingCloseMessageKind.Hold,
        key,
        ack: true,
        id: waiter.id,
      });
      const discovered = (live: Set<string> | null) => {
        if (finished) return;
        waiter.required ??= new Set([...this.peers, ...(live ?? [])]);
        waiter.check();
      };
      void this.liveWindows().then((live) => {
        if (live) discovered(live);
        else later(() => discovered(null), discoveryMs);
      });
      // After each silent period, drop only the windows that are provably gone
      // and keep waiting for the rest.
      const recheck = () => {
        if (finished) return;
        void this.liveWindows().then((live) => {
          if (finished) return;
          discovered(live);
          for (const peer of [...(waiter.required ?? [])]) {
            if (waiter.acked.has(peer) || live?.has(peer)) continue;
            waiter.required?.delete(peer);
            this.peers.delete(peer);
          }
          waiter.check();
          later(recheck, syncTimeoutMs);
        });
      };
      later(recheck, syncTimeoutMs);
    });
  }

  private startHeartbeat() {
    if (this.timer !== undefined || !this.channel || !this.active) return;
    this.timer = setInterval(() => {
      for (const key of this.local.keys())
        this.send({ kind: PendingCloseMessageKind.Hold, key });
    }, heartbeatMs);
  }

  private stopHeartbeat() {
    if (this.timer === undefined || this.local.size) return;
    clearInterval(this.timer);
    this.timer = undefined;
  }

  /**
   * Marks `key` as owned by an unsettled Close held by `owner`. The promise
   * resolves once every known sibling window recorded the hold; await it before
   * the native Close is sent.
   */
  hold(key: string, owner: object): Promise<void> {
    const owners = this.local.get(key) ?? new Set<object>();
    const first = !owners.size;
    owners.add(owner);
    this.local.set(key, owners);
    if (!first) return this.syncing.get(key) ?? Promise.resolve();
    const synced = this.announce(key);
    this.syncing.set(key, synced);
    void synced.finally(() => {
      if (this.syncing.get(key) === synced) this.syncing.delete(key);
    });
    this.startHeartbeat();
    return synced;
  }

  /** Releases one holder once its Close settled, was rejected or abandoned. */
  release(key: string, owner: object) {
    const owners = this.local.get(key);
    if (!owners) return;
    owners.delete(owner);
    if (owners.size) return;
    this.local.delete(key);
    this.waiters.get(key)?.done();
    this.send({ kind: PendingCloseMessageKind.Release, key });
    this.stopHeartbeat();
  }

  /**
   * Records that the original Close was proven complete: this window drops its
   * holds for `key` and tells every sibling window to do the same and to remove
   * the page from its retained Sessions.
   */
  settle(key: string) {
    this.local.delete(key);
    this.syncing.delete(key);
    this.waiters.get(key)?.done();
    this.send({ kind: PendingCloseMessageKind.Settled, key });
    this.stopHeartbeat();
  }

  private releaseAll() {
    const keys = [...this.local.keys()];
    this.local.clear();
    for (const key of keys) this.waiters.get(key)?.done();
    for (const key of keys)
      this.send({ kind: PendingCloseMessageKind.Release, key });
    this.stopHeartbeat();
  }

  /** Whether any Session or product window still owns an unsettled Close. */
  has(key: string): boolean {
    if (this.local.has(key)) return true;
    const holders = this.remote.get(key);
    if (!holders) return false;
    const now = this.now();
    for (const [holder, expiry] of holders) {
      if (expiry > now) continue;
      // Keep counting an expired holder until its liveness lock is gone.
      if (this.locks) this.probe(holder);
      else holders.delete(holder);
    }
    if (!holders.size) this.remote.delete(key);
    return holders.size > 0;
  }
}
