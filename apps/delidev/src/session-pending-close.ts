// SPDX-License-Identifier: Apache-2.0

/** Same-origin broadcast name shared by every product window of one origin. */
const channelName = "delidev.browser.pending-close";
const heartbeatMs = 5000;
const expiryMs = heartbeatMs * 3;

enum PendingCloseMessageKind {
  Hold = "hold",
  Release = "release",
  Query = "query",
}

interface PendingCloseMessage {
  scope: typeof channelName;
  kind: PendingCloseMessageKind;
  window: string;
  key?: string;
}

/** The slice of BroadcastChannel the registry needs (injectable in tests). */
export interface PendingCloseChannel {
  postMessage(message: unknown): void;
  onmessage: ((event: { data: unknown }) => void) | null;
  close(): void;
}

export interface PendingCloseOptions {
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
    (message.key === undefined || typeof message.key === "string")
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
  private channel: PendingCloseChannel | null;
  private timer: ReturnType<typeof setInterval> | undefined;
  private now: () => number;
  private window: string;

  constructor(options: PendingCloseOptions = {}) {
    this.now = options.now ?? Date.now;
    this.window = options.window ?? windowIdentity();
    this.channel =
      options.channel === undefined ? defaultChannel() : options.channel;
    if (!this.channel) return;
    this.channel.onmessage = (event) => this.receive(event.data);
    this.send({ kind: PendingCloseMessageKind.Query });
    if (typeof window !== "undefined")
      window.addEventListener("pagehide", () => this.releaseAll());
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
    if (data.kind === PendingCloseMessageKind.Query) {
      for (const key of this.local.keys())
        this.send({ kind: PendingCloseMessageKind.Hold, key });
      return;
    }
    if (data.key === undefined) return;
    const holders = this.remote.get(data.key) ?? new Map<string, number>();
    if (data.kind === PendingCloseMessageKind.Hold)
      holders.set(data.window, this.now() + expiryMs);
    else holders.delete(data.window);
    if (holders.size) this.remote.set(data.key, holders);
    else this.remote.delete(data.key);
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

  /** Marks `key` as owned by an unsettled Close held by `owner`. */
  hold(key: string, owner: object) {
    const owners = this.local.get(key) ?? new Set<object>();
    const first = !owners.size;
    owners.add(owner);
    this.local.set(key, owners);
    if (!first) return;
    this.send({ kind: PendingCloseMessageKind.Hold, key });
    this.startHeartbeat();
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
    for (const [holder, expiry] of holders)
      if (expiry <= now) holders.delete(holder);
    if (!holders.size) this.remote.delete(key);
    return holders.size > 0;
  }
}
