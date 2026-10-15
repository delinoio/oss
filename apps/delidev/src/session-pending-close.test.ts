import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
      request: (name, _options, callback) => {
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
    const registry = new PendingCloseRegistry({
      channel: endpoint,
      window: "w",
    });
    const sibling = bus.endpoint();
    const seen: unknown[] = [];
    sibling.onmessage = (event) => seen.push(event.data);
    registry.hold("k", {});
    registry.dispose();
    expect(endpoint.closed).toBe(1);
    expect(endpoint.onmessage).toBeNull();
    expect(registry.has("k")).toBe(false);
    expect(seen).toContainEqual(
      expect.objectContaining({ kind: "release", key: "k" }),
    );
    expect(seen.at(-1)).toMatchObject({ kind: "bye" });
    // Disposal is idempotent and a disposed registry can reconnect.
    registry.dispose();
    expect(endpoint.closed).toBe(1);
    registry.connect();
    expect(endpoint.onmessage).not.toBeNull();
    expect(observer.has("k")).toBe(false);
  });

  it("does not touch the channel or lifecycle until connected", () => {
    const bus = new Bus();
    const endpoint = bus.endpoint();
    const seen: unknown[] = [];
    bus.endpoint().onmessage = (event) => seen.push(event.data);
    const registry = new PendingCloseRegistry({
      channel: endpoint,
      locks: null,
      deferConnect: true,
      window: "w",
    });
    expect(endpoint.onmessage).toBeNull();
    expect(seen).toEqual([]);
    registry.connect();
    expect(endpoint.onmessage).not.toBeNull();
    expect(seen).toContainEqual(expect.objectContaining({ kind: "query" }));
    registry.dispose();
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

    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("resolves only after every sibling recorded the hold", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "a",
      });
      const second = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "b",
      });
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await Promise.resolve();
      expect(synced).toBe(false);
      expect(second.has("k")).toBe(false);
      bus.flush();
      await Promise.resolve();
      expect(second.has("k")).toBe(true);
      // Without Web Locks the Close still waits out the discovery period.
      expect(synced).toBe(false);
      await vi.advanceTimersByTimeAsync(101);
      expect(synced).toBe(true);
    });

    it("waits for a live window whose handshake has not arrived", async () => {
      const held = new Set<string>();
      const locks: PendingCloseLocks = {
        request: (name, _options, callback) => {
          held.add(name);
          return callback().finally(() => held.delete(name));
        },
        query: async () => ({ held: [...held].map((name) => ({ name })) }),
      };
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "a",
      });
      // The sibling exists and holds its liveness lock, but its Query has not
      // been delivered, so "a" has never seen it on the channel.
      const second = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "b",
      });
      bus.queue.length = 0;
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await vi.advanceTimersByTimeAsync(50);
      expect(synced).toBe(false);
      bus.flush();
      await vi.advanceTimersByTimeAsync(0);
      expect(second.has("k")).toBe(true);
      expect(synced).toBe(true);
    });

    it("re-announces the hold to a live window that missed the first one", async () => {
      const held = new Set<string>();
      const locks: PendingCloseLocks = {
        request: (name, _options, callback) => {
          held.add(name);
          return callback().finally(() => held.delete(name));
        },
        query: async () => ({ held: [...held].map((name) => ({ name })) }),
      };
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "a",
      });
      const second = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "b",
      });
      bus.queue.length = 0;
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      // The sibling registered its lock but never received the first Hold.
      bus.queue.length = 0;
      await vi.advanceTimersByTimeAsync(0);
      expect(synced).toBe(false);
      expect(second.has("k")).toBe(false);
      // Discovering it re-sends an acknowledgement-requesting Hold.
      bus.flush();
      await vi.advanceTimersByTimeAsync(0);
      bus.flush();
      await vi.advanceTimersByTimeAsync(0);
      expect(second.has("k")).toBe(true);
      expect(synced).toBe(true);
    });

    it("asks a window that joins mid-barrier for an acknowledgement", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "a",
      });
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      bus.queue.length = 0;
      const late = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "c",
      });
      // The newcomer's Query is answered with a Hold that requires an Ack.
      bus.flush();
      bus.flush();
      bus.flush();
      expect(late.has("k")).toBe(true);
      await vi.advanceTimersByTimeAsync(150);
      expect(synced).toBe(true);
    });

    it("requires an acknowledgement from a window joining an open barrier", async () => {
      const bus = new QueuedBus();
      const make = (window: string, drop = false) =>
        new PendingCloseRegistry({
          channel: bus.endpoint(drop),
          locks: null,
          window,
        });
      const first = make("a");
      make("b");
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      // Discovery ends with only "b" required.
      await vi.advanceTimersByTimeAsync(101);
      // "c" joins while the barrier is open but never receives the Hold.
      make("c", true);
      bus.flush();
      await vi.advanceTimersByTimeAsync(500);
      expect(synced).toBe(false);
    });

    it("keeps a remote hold when the liveness query fails", async () => {
      const locks: PendingCloseLocks = {
        request: () => new Promise(() => undefined),
        query: async () => {
          throw new Error("query failed");
        },
      };
      const bus = new Bus();
      let time = 0;
      const make = (window: string) =>
        new PendingCloseRegistry({
          channel: bus.endpoint(),
          locks,
          now: () => time,
          window,
        });
      const holder = make("holder");
      const observer = make("observer");
      holder.hold("k", {});
      time += 600_000;
      expect(observer.has("k")).toBe(true);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      expect(observer.has("k")).toBe(true);
    });

    it("keeps awaiting a silent window for a while when liveness is unknown", async () => {
      const locks: PendingCloseLocks = {
        request: () => new Promise(() => undefined),
        query: async () => {
          throw new Error("query failed");
        },
      };
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "a",
      });
      // "b" introduces itself but never acknowledges.
      new PendingCloseRegistry({
        channel: bus.endpoint(true),
        locks: null,
        window: "b",
      });
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await vi.advanceTimersByTimeAsync(3_000);
      expect(synced).toBe(false);
      await vi.advanceTimersByTimeAsync(5_000);
      expect(synced).toBe(true);
    });

    it("waits out discovery for an unintroduced window without Web Locks", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "a",
      });
      new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "b",
      });
      bus.queue.length = 0;
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await vi.advanceTimersByTimeAsync(99);
      expect(synced).toBe(false);
      bus.flush();
      await vi.advanceTimersByTimeAsync(2);
      expect(synced).toBe(true);
    });

    it("stops waiting for a window that never answers", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "a",
      });
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
      // The unresponsive window is no longer awaited (only discovery remains).
      let again = false;
      void first.hold("k2", {}).then(() => (again = true));
      await vi.advanceTimersByTimeAsync(101);
      expect(again).toBe(true);
    });

    const lockSet = () => {
      const held = new Set<string>();
      const locks: PendingCloseLocks = {
        request: (name, _options, callback) => {
          held.add(name);
          return callback().finally(() => held.delete(name));
        },
        query: async () => ({ held: [...held].map((name) => ({ name })) }),
      };
      return { held, locks };
    };

    it("keeps waiting for a suspended sibling that still holds its lock", async () => {
      const { held, locks } = lockSet();
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "a",
      });
      const second = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "b",
      });
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      // The sibling is suspended: nothing it should process is delivered.
      await vi.advanceTimersByTimeAsync(5000);
      expect(synced).toBe(false);
      expect(second.has("k")).toBe(false);
      bus.flush();
      await vi.advanceTimersByTimeAsync(0);
      expect(second.has("k")).toBe(true);
      expect(synced).toBe(true);
      expect(held.has("delidev.browser.pending-close:b")).toBe(true);
    });

    it("stops waiting once a silent sibling's lock disappears", async () => {
      const { held, locks } = lockSet();
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "a",
      });
      new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks,
        window: "b",
      });
      bus.flush();
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      await vi.advanceTimersByTimeAsync(3000);
      expect(synced).toBe(false);
      held.delete("delidev.browser.pending-close:b");
      await vi.advanceTimersByTimeAsync(1001);
      expect(synced).toBe(true);
    });

    it("does not credit a delayed Ack to a later announcement of the same key", async () => {
      const bus = new QueuedBus();
      const first = new PendingCloseRegistry({
        channel: bus.endpoint(),
        locks: null,
        window: "a",
      });
      const sibling = bus.endpoint();
      const seen: { id?: string }[] = [];
      sibling.onmessage = (event) => {
        const data = event.data as { kind: string; id?: string };
        if (data.kind === "hold") seen.push(data);
      };
      const ack = (id?: string) =>
        first["receive"]({
          scope: "delidev.browser.pending-close",
          kind: "ack",
          window: "z",
          key: "k",
          id,
          to: "a",
        });
      sibling.postMessage({
        scope: "delidev.browser.pending-close",
        kind: "presence",
        window: "z",
      });
      bus.flush();
      const owner = {};
      void first.hold("k", owner);
      bus.flush();
      const staleId = seen[0].id;
      expect(staleId).toBeTruthy();
      // The first Close is abandoned and a replacement starts for the page.
      first.release("k", owner);
      let synced = false;
      void first.hold("k", {}).then(() => (synced = true));
      bus.flush();
      expect(seen[1].id).toBeTruthy();
      expect(seen[1].id).not.toBe(staleId);
      // The delayed Ack of the first announcement must not satisfy the second.
      ack(staleId);
      await vi.advanceTimersByTimeAsync(101);
      expect(synced).toBe(false);
      ack(seen[1].id);
      await vi.advanceTimersByTimeAsync(101);
      expect(synced).toBe(true);
    });
  });

  describe("window lifecycle", () => {
    /** A lock manager whose requests can stay pending, honoring abort. */
    const deferredLocks = () => {
      const held = new Set<string>();
      const pending = new Set<string>();
      const grants: (() => void)[] = [];
      const locks: PendingCloseLocks = {
        request: (name, { signal }, callback) => {
          pending.add(name);
          return new Promise<void>((resolve) => {
            const grant = () => {
              if (!pending.delete(name)) return resolve();
              held.add(name);
              void callback().finally(() => {
                held.delete(name);
                resolve();
              });
            };
            signal.addEventListener("abort", () => {
              pending.delete(name);
              resolve();
            });
            grants.push(grant);
          });
        },
        query: async () => ({
          held: [...held].map((name) => ({ name })),
          pending: [...pending].map((name) => ({ name })),
        }),
      };
      return { held, pending, grants, locks };
    };

    it("withdraws a lock request that was not granted before dispose", () => {
      const { held, pending, grants, locks } = deferredLocks();
      const registry = new PendingCloseRegistry({
        channel: new Bus().endpoint(),
        locks,
        window: "w",
      });
      expect(pending.size).toBe(1);
      registry.dispose();
      expect(pending.size).toBe(0);
      // A late grant must not leave the lock held forever.
      for (const grant of grants) grant();
      expect(held.size).toBe(0);
    });

    it("does not keep a lock granted after cancellation", async () => {
      const held = new Set<string>();
      let granted: (() => Promise<unknown>) | undefined;
      const locks: PendingCloseLocks = {
        request: async (name, _options, callback) => {
          granted = callback;
          held.add(name);
        },
        query: async () => ({}),
      };
      const registry = new PendingCloseRegistry({
        channel: new Bus().endpoint(),
        locks,
        window: "w",
      });
      registry.dispose();
      // The browser grants the already-cancelled request anyway.
      await expect(granted?.()).resolves.toBeUndefined();
    });

    it("counts a sibling's pending lock request as a live window", async () => {
      vi.useFakeTimers();
      try {
        const { locks, pending } = deferredLocks();
        const a = new PendingCloseRegistry({
          channel: new Bus().endpoint(),
          locks,
          window: "a",
        });
        // A silent sibling requested its liveness lock but is not granted yet.
        const name = "delidev.browser.pending-close:ghost";
        pending.add(name);
        let synced = false;
        void a.hold("k", {}).then(() => (synced = true));
        await vi.advanceTimersByTimeAsync(5_000);
        expect(synced).toBe(false);
        pending.delete(name);
        await vi.advanceTimersByTimeAsync(1_100);
        expect(synced).toBe(true);
      } finally {
        vi.useRealTimers();
      }
    });

    it("stops announcing holds once the page is hiding", () => {
      const bus = new Bus();
      const endpoint = bus.endpoint();
      const registry = new PendingCloseRegistry({
        channel: endpoint,
        window: "w",
      });
      const sibling = bus.endpoint();
      const seen: { kind: string }[] = [];
      sibling.onmessage = (event) => seen.push(event.data as { kind: string });
      registry.hold("before", {});
      window.dispatchEvent(new Event("pagehide"));
      expect(seen.at(-1)).toMatchObject({ kind: "bye" });
      seen.length = 0;
      // The native Close is sent after pagehide: the Hold must stay silent.
      void registry.hold("late", {});
      expect(seen.filter((m) => m.kind === "hold")).toEqual([]);
      expect(registry.has("late")).toBe(true);
      registry.dispose();
    });

    it("stops vouching for a hold once the page is hiding or disposed", async () => {
      const registry = new PendingCloseRegistry({
        channel: new Bus().endpoint(),
        window: "w",
      });
      const owner = {};
      void registry.hold("k", owner);
      expect(registry.holds("k", owner)).toBe(true);
      window.dispatchEvent(new Event("pagehide"));
      expect(registry.holds("k", owner)).toBe(false);
      // A hold taken after pagehide is never announced, so it never counts.
      const late = {};
      void registry.hold("late", late);
      expect(registry.holds("late", late)).toBe(false);
      registry.dispose();
      expect(registry.holds("late", late)).toBe(false);
    });

    it("keeps a hold valid in a registry without a channel", () => {
      const registry = new PendingCloseRegistry({ channel: null });
      const owner = {};
      void registry.hold("k", owner);
      expect(registry.holds("k", owner)).toBe(true);
      registry.release("k", owner);
      expect(registry.holds("k", owner)).toBe(false);
    });

    it("resumes announcing after a page restored from the cache", () => {
      const bus = new Bus();
      const registry = new PendingCloseRegistry({
        channel: bus.endpoint(),
        window: "w",
      });
      const sibling = bus.endpoint();
      const seen: { kind: string }[] = [];
      sibling.onmessage = (event) => seen.push(event.data as { kind: string });
      window.dispatchEvent(new Event("pagehide"));
      const restored = new Event("pageshow");
      Object.defineProperty(restored, "persisted", { value: true });
      window.dispatchEvent(restored);
      expect(seen.at(-1)).toMatchObject({ kind: "query" });
      void registry.hold("k", {});
      expect(seen.some((m) => m.kind === "hold")).toBe(true);
      registry.dispose();
    });
  });

  describe("restoring holds after the back/forward cache", () => {
    // Several registries share one jsdom window, so lifecycle events are
    // delivered to the owner alone by calling its handlers directly.
    const lifecycle = (registry: PendingCloseRegistry) => {
      const handlers = registry as unknown as {
        onPageHide: () => void;
        onPageShow: (event: Event) => void;
      };
      return {
        hide: () => handlers.onPageHide(),
        show: (persisted: boolean) => {
          const event = new Event("pageshow");
          Object.defineProperty(event, "persisted", { value: persisted });
          handlers.onPageShow(event);
        },
      };
    };
    const setup = () => {
      const bus = new Bus();
      const owner = new PendingCloseRegistry({
        channel: bus.endpoint(),
        window: "owner",
        locks: null,
      });
      const siblingRegistry = new PendingCloseRegistry({
        channel: bus.endpoint(),
        window: "sibling",
        locks: null,
      });
      // A raw endpoint records what the owner announces.
      const spy = bus.endpoint();
      const seen: { kind: string; key?: string; ack?: boolean; window: string }[] =
        [];
      spy.onmessage = (event) => seen.push(event.data as (typeof seen)[number]);
      return { bus, owner, siblingRegistry, seen };
    };
    const dispatched = async (registry: PendingCloseRegistry, key: string) => {
      const token = {};
      void registry.hold(key, token);
      // The barrier finishes once the sibling acknowledged and discovery ended.
      await vi.advanceTimersByTimeAsync(200);
      return token;
    };

    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("re-announces a dispatched Close so the sibling keeps the descriptor", async () => {
      const { owner, siblingRegistry, seen } = setup();
      const sibling = new SessionTabsStore(siblingRegistry);
      sibling.open("retained", page("p1", "x"));
      const key = JSON.stringify(["x", "p1"]);
      const token = await dispatched(owner, key);
      const { hide, show } = lifecycle(owner);
      hide();
      expect(owner.holds(key, token)).toBe(false);
      expect(siblingRegistry.has(key)).toBe(false);
      seen.length = 0;
      show(true);
      // The restored hold is acknowledged, so the sibling already records it.
      expect(seen.some((m) => m.kind === "hold" && m.key === key && m.ack)).toBe(
        true,
      );
      expect(owner.holds(key, token)).toBe(true);
      expect(owner.has(key)).toBe(true);
      expect(siblingRegistry.has(key)).toBe(true);
      sibling.reconcilePages("retained", "x", []);
      expect(sibling.snapshot("retained").tabs.map(sessionTabKey)).toContain(
        sessionTabKey(page("p1", "x")),
      );
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("removes a restored hold everywhere once the original Close settles", async () => {
      const { owner, siblingRegistry } = setup();
      const key = "k";
      const token = await dispatched(owner, key);
      const { hide, show } = lifecycle(owner);
      hide();
      show(true);
      owner.settle(key);
      expect(owner.has(key)).toBe(false);
      expect(siblingRegistry.has(key)).toBe(false);
      // Settlement also ends the restored heartbeat.
      expect(owner.holds(key, token)).toBe(false);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("keeps a restored hold until its owner releases it", async () => {
      const { owner, siblingRegistry } = setup();
      const token = await dispatched(owner, "k");
      const { hide, show } = lifecycle(owner);
      hide();
      show(true);
      owner.release("k", token);
      expect(owner.has("k")).toBe(false);
      expect(siblingRegistry.has("k")).toBe(false);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("refreshes a restored hold with the heartbeat", async () => {
      const { owner, siblingRegistry } = setup();
      await dispatched(owner, "k");
      const { hide, show } = lifecycle(owner);
      hide();
      show(true);
      await vi.advanceTimersByTimeAsync(60_000);
      expect(siblingRegistry.has("k")).toBe(true);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("never revives a Close withdrawn during its pre-send barrier", async () => {
      const { bus, owner, siblingRegistry, seen } = setup();
      // A silent window keeps the barrier open.
      const ghost = bus.endpoint();
      ghost.postMessage({
        scope: "delidev.browser.pending-close",
        kind: "presence",
        window: "ghost",
      });
      const token = {};
      void owner.hold("pending", token);
      await vi.advanceTimersByTimeAsync(200);
      const { hide, show } = lifecycle(owner);
      hide();
      seen.length = 0;
      show(true);
      expect(owner.holds("pending", token)).toBe(false);
      expect(owner.has("pending")).toBe(false);
      expect(siblingRegistry.has("pending")).toBe(false);
      expect(seen.filter((m) => m.kind === "hold")).toEqual([]);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("restores only the dispatched operations of several keys", async () => {
      const { bus, owner, siblingRegistry } = setup();
      const sent = await dispatched(owner, "sent");
      const ghost = bus.endpoint();
      ghost.postMessage({
        scope: "delidev.browser.pending-close",
        kind: "presence",
        window: "ghost",
      });
      const waiting = {};
      void owner.hold("waiting", waiting);
      await vi.advanceTimersByTimeAsync(200);
      const { hide, show } = lifecycle(owner);
      hide();
      show(true);
      expect(owner.holds("sent", sent)).toBe(true);
      expect(siblingRegistry.has("sent")).toBe(true);
      expect(owner.holds("waiting", waiting)).toBe(false);
      expect(siblingRegistry.has("waiting")).toBe(false);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("does not resurrect holds that settled, were released or belong to a replaced owner", async () => {
      const { owner, siblingRegistry } = setup();
      const settled = await dispatched(owner, "settled");
      const released = await dispatched(owner, "released");
      const { hide, show } = lifecycle(owner);
      hide();
      owner.settle("settled");
      owner.release("released", released);
      show(true);
      expect(owner.holds("settled", settled)).toBe(false);
      expect(owner.has("settled")).toBe(false);
      expect(owner.has("released")).toBe(false);
      expect(siblingRegistry.has("settled")).toBe(false);
      expect(siblingRegistry.has("released")).toBe(false);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("drops a suspended hold another window proved settled", async () => {
      const { owner, siblingRegistry } = setup();
      const token = await dispatched(owner, "k");
      const { hide, show } = lifecycle(owner);
      hide();
      siblingRegistry.settle("k");
      show(true);
      expect(owner.holds("k", token)).toBe(false);
      expect(siblingRegistry.has("k")).toBe(false);
      owner.dispose();
      siblingRegistry.dispose();
    });

    it("restores nothing after disposal or a non-persisted pageshow", async () => {
      const { owner, siblingRegistry } = setup();
      const token = await dispatched(owner, "k");
      const { hide, show } = lifecycle(owner);
      hide();
      show(false);
      expect(owner.holds("k", token)).toBe(false);
      expect(siblingRegistry.has("k")).toBe(false);
      owner.dispose();
      // A permanently disposed registry forgets its suspended holds.
      show(true);
      expect(owner.holds("k", token)).toBe(false);
      expect(siblingRegistry.has("k")).toBe(false);
      siblingRegistry.dispose();
    });
  });

  describe("settled Close propagation", () => {
    it("removes the page from a sibling's retained sessions once", () => {
      const { make } = windows();
      const initiator = new SessionTabsStore(make("initiator"));
      const sibling = new SessionTabsStore(make("sibling"));
      sibling.open("retained", page("p1", "x"));
      const owner = {};
      initiator.holdPendingClose("x", "p1", owner);
      // A rejected or abandoned Close only releases the hold.
      initiator.releasePendingClose("x", "p1", owner);
      expect(sibling.snapshot("retained").tabs.map(sessionTabKey)).toContain(
        sessionTabKey(page("p1", "x")),
      );
      initiator.holdPendingClose("x", "p1", owner);
      initiator.closePageEverywhere("x", "p1");
      expect(sibling.snapshot("retained").tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
      ]);
      // The hold is gone everywhere, including for the settling window.
      sibling.reconcilePages("retained", "x", []);
      initiator.releasePendingClose("x", "p1", owner);
    });
  });
});
