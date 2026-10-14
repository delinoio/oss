// SPDX-License-Identifier: Apache-2.0

/** Same-origin broadcast name shared by every product window of one origin. */
const channelName = "delidev.browser.pending-close";
const heartbeatMs = 5000;
const expiryMs = heartbeatMs * 3;
/** Longest a Close waits for sibling windows to acknowledge a new hold. */
const syncTimeoutMs = 1000;

enum PendingCloseMessageKind {
  Hold = "hold",
  Release = "release",
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
  request(name: string, callback: () => Promise<unknown>): Promise<unknown>;
  query(): Promise<{ held?: { name?: string }[] }>;
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
    (message.to === undefined || typeof message.to === "string")
  );
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
  private onPageHide = () => {
    this.releaseAll();
    this.send({ kind: PendingCloseMessageKind.Bye });
  };
  private locks: PendingCloseLocks | null = null;
  private unlock: (() => void) | undefined;
  private probing = new Set<string>();
  // Sibling windows seen on the channel, and the Holds still awaiting acks.
  private peers = new Set<string>();
  private syncing = new Map<string, Promise<void>>();
  private waiters = new Map<string, { awaiting: Set<string>; done: () => void }>();

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
    this.send({ kind: PendingCloseMessageKind.Query });
    if (typeof window !== "undefined")
      window.addEventListener("pagehide", this.onPageHide);
    this.holdLivenessLock();
  }

  private lockName(window: string) {
    return `${channelName}:${window}`;
  }

  /** Holds this window's liveness lock until disposal or window teardown. */
  private holdLivenessLock() {
    const { locks } = this.options;
    this.locks = locks === undefined ? defaultLocks() : locks;
    if (!this.locks) return;
    try {
      void this.locks
        .request(
          this.lockName(this.window),
          () => new Promise<void>((resolve) => (this.unlock = resolve)),
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
      .then((state) => settle(Boolean(state.held?.some((l) => l.name === name))))
      .catch(() => settle(false));
  }

  /**
   * Releases every local hold, stops the heartbeat, detaches the page listener
   * and closes the channel, so a discarded registry stays reachable by nothing.
   */
  dispose() {
    if (!this.channel) return;
    this.releaseAll();
    this.send({ kind: PendingCloseMessageKind.Bye });
    if (typeof window !== "undefined")
      window.removeEventListener("pagehide", this.onPageHide);
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
    this.unlock?.();
    this.unlock = undefined;
    this.locks = null;
  }

  private send(message: Omit<PendingCloseMessage, "scope" | "window">) {
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
    if (data.kind === PendingCloseMessageKind.Bye) {
      this.peers.delete(data.window);
      for (const [key, holders] of this.remote) {
        holders.delete(data.window);
        if (!holders.size) this.remote.delete(key);
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
      waiter?.awaiting.delete(data.window);
      if (waiter && !waiter.awaiting.size) waiter.done();
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
        to: data.window,
      });
  }

  /**
   * Announces a new hold and resolves once every known sibling window has
   * recorded it, so a sibling's reconciliation can never observe the page as
   * unowned after the native Close ran. A window that does not answer within
   * the timeout is presumed gone and no longer awaited.
   */
  private announce(key: string): Promise<void> {
    const awaiting = new Set(this.peers);
    if (!this.channel || !awaiting.size) {
      this.send({ kind: PendingCloseMessageKind.Hold, key });
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      const done = () => {
        if (timer !== undefined) clearTimeout(timer);
        if (this.waiters.get(key) === waiter) this.waiters.delete(key);
        for (const peer of awaiting) this.peers.delete(peer);
        resolve();
      };
      const waiter = { awaiting, done };
      this.waiters.set(key, waiter);
      timer = setTimeout(done, syncTimeoutMs);
      (timer as unknown as { unref?: () => void }).unref?.();
      this.send({ kind: PendingCloseMessageKind.Hold, key, ack: true });
    });
  }

  private startHeartbeat() {
    if (this.timer !== undefined || !this.channel) return;
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
    this.send({ kind: PendingCloseMessageKind.Release, key });
    this.stopHeartbeat();
  }

  private releaseAll() {
    const keys = [...this.local.keys()];
    this.local.clear();
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
