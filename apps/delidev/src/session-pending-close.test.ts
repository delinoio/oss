import { describe, expect, it, vi } from "vitest";
import {
  PendingCloseRegistry,
  type PendingCloseChannel,
  type PendingCloseLocks,
} from "./session-pending-close";
import {
  SessionTabsStore,
  SessionTabKind,
  sessionTabKey,
} from "./session-tabs";

/** Delivers each posted message to every other endpoint, like a BroadcastChannel. */
class Bus {
  endpoints: Endpoint[] = [];
  endpoint(): Endpoint {
    const endpoint = new Endpoint(this);
    this.endpoints.push(endpoint);
    return endpoint;
  }
}
class Endpoint implements PendingCloseChannel {
  onmessage: ((event: { data: unknown }) => void) | null = null;
  constructor(private bus: Bus) {}
  postMessage(message: unknown) {
    for (const other of this.bus.endpoints)
      if (other !== this) other.onmessage?.({ data: message });
  }
  closed = 0;
  close() {
    this.closed += 1;
  }
}

const page = (id: string, profile: string) => ({
  kind: SessionTabKind.Page as const,
  profile,
  id,
  title: id,
});

describe("pending Close ownership across product windows", () => {
  const windows = () => {
    const bus = new Bus();
    let time = 0;
    const clock = { advance: (ms: number) => (time += ms) };
    const make = (name: string) =>
      new PendingCloseRegistry({
        channel: bus.endpoint(),
        now: () => time,
        window: name,
      });
    return { make, clock };
  };

  it("keeps another window's descriptor until the holder releases", () => {
    const { make } = windows();
    const first = make("first");
    const second = make("second");
    const owner = {};
    first.hold("k", owner);
    expect(second.has("k")).toBe(true);
    first.release("k", owner);
    expect(second.has("k")).toBe(false);
  });

  it("keeps holds from several windows independent", () => {
    const { make } = windows();
    const first = make("first");
    const second = make("second");
    const observer = make("observer");
    const a = {};
    const b = {};
    first.hold("k", a);
    second.hold("k", b);
    first.release("k", a);
    expect(observer.has("k")).toBe(true);
    second.release("k", b);
    expect(observer.has("k")).toBe(false);
  });

  it("expires a hold whose window stopped announcing it", () => {
    const { make, clock } = windows();
    const first = make("first");
    const second = make("second");
    first.hold("k", {});
    expect(second.has("k")).toBe(true);
    clock.advance(60_000);
    expect(second.has("k")).toBe(false);
  });

  it("keeps an expired hold while its window's liveness lock is held", async () => {
    const held = new Set<string>();
    const locks: PendingCloseLocks = {
      request: (name, callback) => {
        held.add(name);
        return callback().finally(() => held.delete(name));
      },
      query: async () => ({ held: [...held].map((name) => ({ name })) }),
    };
    const bus = new Bus();
    let time = 0;
    const make = (name: string) =>
      new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        now: () => time,
        window: name,
      });
    const hidden = make("hidden");
    const active = make("active");
    hidden.hold("k", {});
    time += 600_000;
    // The first read triggers the liveness probe and still counts the hold.
    expect(active.has("k")).toBe(true);
    await Promise.resolve();
    await Promise.resolve();
    expect(active.has("k")).toBe(true);
    // The window crashes without a Release: the browser frees its lock, so the hold finally lapses.
    held.clear();
    time += 600_000;
    expect(active.has("k")).toBe(true);
    await Promise.resolve();
    await Promise.resolve();
    expect(active.has("k")).toBe(false);
  });

  it("answers a newly opened window's query with current holds", () => {
    const { make } = windows();
    const first = make("first");
    first.hold("k", {});
    expect(make("late").has("k")).toBe(true);
  });

  it("ignores unrelated or malformed broadcasts", () => {
    const bus = new Bus();
    const endpoint = bus.endpoint();
    const registry = new PendingCloseRegistry({
      channel: endpoint,
      window: "self",
    });
    for (const data of [
      null,
      "hold",
      { scope: "other", kind: "hold", window: "x", key: "k" },
      { scope: "delidev.browser.pending-close", kind: "bad", window: "x" },
      { scope: "delidev.browser.pending-close", kind: "hold", window: 7 },
      {
        scope: "delidev.browser.pending-close",
        kind: "hold",
        window: "self",
        key: "k",
      },
    ])
      endpoint.onmessage?.({ data });
    expect(registry.has("k")).toBe(false);
  });

  it("works locally without a channel", () => {
    const registry = new PendingCloseRegistry({ channel: null });
    const owner = {};
    registry.hold("k", owner);
    expect(registry.has("k")).toBe(true);
    registry.release("k", owner);
    expect(registry.has("k")).toBe(false);
  });

  it("makes a sibling window's reconciliation keep the closing page", () => {
    const { make } = windows();
    const initiator = new SessionTabsStore(make("initiator"));
    const sibling = new SessionTabsStore(make("sibling"));
    sibling.open("s", page("p1", "x"));
    sibling.open("s", page("p2", "x"));
    const owner = {};
    initiator.holdPendingClose("x", "p1", owner);
    sibling.reconcilePages("s", "x", []);
    expect(sibling.snapshot("s").tabs.map(sessionTabKey)).toEqual([
      SessionTabKind.Conversation,
      sessionTabKey(page("p1", "x")),
    ]);
    initiator.releasePendingClose("x", "p1", owner);
    sibling.reconcilePages("s", "x", []);
    expect(sibling.snapshot("s").tabs.map(sessionTabKey)).toEqual([
      SessionTabKind.Conversation,
    ]);
  });

  it("disposes the channel, handler and holds with its store", () => {
    const { make } = windows();
    const observer = make("observer");
    const bus = new Bus();
    const endpoint = bus.endpoint();
    const registry = new PendingCloseRegistry({ channel: endpoint, window: "w" });
    const sibling = bus.endpoint();
    const seen: unknown[] = [];
    sibling.onmessage = (event) => seen.push(event.data);
    registry.hold("k", {});
    registry.dispose();
    expect(endpoint.closed).toBe(1);
    expect(endpoint.onmessage).toBeNull();
    expect(registry.has("k")).toBe(false);
    expect(seen).toContainEqual(expect.objectContaining({ kind: "release", key: "k" }));
    expect(seen.at(-1)).toMatchObject({ kind: "bye" });
    // Disposal is idempotent and a disposed registry can reconnect.
    registry.dispose();
    expect(endpoint.closed).toBe(1);
    registry.connect();
    expect(endpoint.onmessage).not.toBeNull();
    expect(observer.has("k")).toBe(false);
  });

  describe("hold synchronization barrier", () => {
    /** Queues posts so delivery order is controlled by the test. */
    class QueuedBus {
      queue: (() => void)[] = [];
      endpoints: QueuedEndpoint[] = [];
      endpoint(drop = false) {
        const endpoint = new QueuedEndpoint(this, drop);
        this.endpoints.push(endpoint);
        return endpoint;
      }
      flush() {
        while (this.queue.length) this.queue.shift()?.();
      }
    }
    class QueuedEndpoint implements PendingCloseChannel {
      onmessage: ((event: { data: unknown }) => void) | null = null;
      constructor(
        private bus: QueuedBus,
        private drop: boolean,
      ) {}
      postMessage(message: unknown) {
        for (const other of this.bus.endpoints)
          if (other !== this && !other.drop)
            this.bus.queue.push(() => other.onmessage?.({ data: message }));
      }
      close() {}
    }

    it("resolves only after every sibling recorded the hold", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({ channel: bus.endpoint(), locks: null, window: "a" });
      const second = new PendingCloseRegistry({ channel: bus.endpoint(), locks: null, window: "b" });
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await Promise.resolve();
      expect(synced).toBe(false);
      expect(second.has("k")).toBe(false);
      bus.flush();
      await Promise.resolve();
      await Promise.resolve();
      expect(second.has("k")).toBe(true);
      expect(synced).toBe(true);
    });

    it("stops waiting for a window that never answers", async () => {
      vi.useFakeTimers();
      try {
        const bus = new QueuedBus();
        const first = new PendingCloseRegistry({ channel: bus.endpoint(), locks: null, window: "a" });
        const silent = bus.endpoint();
        // The silent window announces itself once, then never acknowledges.
        silent.postMessage({
          scope: "delidev.browser.pending-close",
          kind: "presence",
          window: "z",
        });
        bus.flush();
        (silent as unknown as { drop: boolean }).drop = true;
        let synced = false;
        void first.hold("k", {}).then(() => (synced = true));
        await vi.advanceTimersByTimeAsync(999);
        expect(synced).toBe(false);
        await vi.advanceTimersByTimeAsync(2);
        expect(synced).toBe(true);
        // The unresponsive window is no longer awaited.
        let again = false;
        void first.hold("k2", {}).then(() => (again = true));
        await Promise.resolve();
        expect(again).toBe(true);
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
