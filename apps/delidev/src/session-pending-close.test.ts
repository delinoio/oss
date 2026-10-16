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

    describe("liveness query that does not settle", () => {
      const neverSettles = () => new Promise<never>(() => undefined);

      /** Registers a silent peer "b" that introduced itself and never answers. */
      const silentPeer = (
        locks: PendingCloseLocks,
        local?: PendingCloseLocks,
      ) => {
        const bus = new QueuedBus();
        const sent: { kind?: string; ack?: boolean }[] = [];
        const endpoint = bus.endpoint();
        const post = endpoint.postMessage.bind(endpoint);
        endpoint.postMessage = (message: unknown) => {
          sent.push(message as { kind?: string; ack?: boolean });
          post(message);
        };
        const first = new PendingCloseRegistry({
          channel: endpoint,
          locks: local ?? locks,
          window: "a",
        });
        new PendingCloseRegistry({
          channel: bus.endpoint(true),
          locks: null,
          window: "b",
        });
        bus.flush();
        return { bus, first, sent };
      };

      it("treats a query that never settles as inconclusive and gives up after the bounded rechecks", async () => {
        const locks: PendingCloseLocks = {
          request: () => new Promise(() => undefined),
          query: neverSettles,
        };
        const { first, sent } = silentPeer(locks);
        let synced = 0;
        void first.hold("k", {}).then(() => (synced += 1));
        // Five inconclusive rechecks (one deadline plus one interval each).
        await vi.advanceTimersByTimeAsync(11_000);
        expect(synced).toBe(0);
        await vi.advanceTimersByTimeAsync(1_200);
        expect(synced).toBe(1);
        // Exactly one original announcement; nothing is sent after the barrier.
        // (Heartbeat holds carry no acknowledgement request.)
        const announced = () =>
          sent.filter((m) => m.kind === "hold" && m.ack).length;
        expect(announced()).toBe(1);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(synced).toBe(1);
        expect(announced()).toBe(1);
        expect(vi.getTimerCount()).toBe(1); // only the heartbeat of the live hold
      });

      it("keeps awaiting a silent window that a later query proves alive", async () => {
        const { held, locks: backing } = lockSet();
        let calls = 0;
        const locks: PendingCloseLocks = {
          request: backing.request,
          // The first reads never settle; later reads prove "b" alive.
          query: () => (++calls <= 2 ? neverSettles() : backing.query()),
        };
        const bus = new QueuedBus();
        const first = new PendingCloseRegistry({
          channel: bus.endpoint(),
          locks,
          window: "a",
        });
        new PendingCloseRegistry({
          channel: bus.endpoint(),
          locks: backing,
          window: "b",
        });
        bus.flush();
        expect(held.has("delidev.browser.pending-close:b")).toBe(true);
        let synced = false;
        void first.hold("k", {}).then(() => (synced = true));
        // Far beyond the fallback budget of an unknown liveness.
        await vi.advanceTimersByTimeAsync(60_000);
        expect(synced).toBe(false);
        held.delete("delidev.browser.pending-close:b");
        await vi.advanceTimersByTimeAsync(2_100);
        expect(synced).toBe(true);
      });

      it("ignores a timed-out query that resolves after the barrier was released", async () => {
        let resolveQuery: ((state: object) => void) | undefined;
        const locks: PendingCloseLocks = {
          request: () => new Promise(() => undefined),
          query: () =>
            new Promise((resolve) => {
              resolveQuery = resolve;
            }),
        };
        const { first, sent } = silentPeer(locks);
        const owner = {};
        let synced = 0;
        void first.hold("k", owner).then(() => (synced += 1));
        await vi.advanceTimersByTimeAsync(1_500);
        first.release("k", owner);
        await vi.advanceTimersByTimeAsync(0);
        expect(synced).toBe(1);
        const before = sent.length;
        resolveQuery?.({ held: [{ name: "delidev.browser.pending-close:b" }] });
        await vi.advanceTimersByTimeAsync(10_000);
        expect(sent.length).toBe(before);
        expect(vi.getTimerCount()).toBe(0);
      });

      it("swallows a timed-out query that rejects after disposal", async () => {
        const unhandled = vi.fn();
        process.on("unhandledRejection", unhandled);
        try {
          let rejectQuery: ((error: Error) => void) | undefined;
          const locks: PendingCloseLocks = {
            request: () => new Promise(() => undefined),
            query: () =>
              new Promise((_resolve, reject) => {
                rejectQuery = reject;
              }),
          };
          const { first, sent } = silentPeer(locks);
          let synced = 0;
          void first.hold("k", {}).then(() => (synced += 1));
          await vi.advanceTimersByTimeAsync(1_500);
          first.dispose();
          await vi.advanceTimersByTimeAsync(0);
          expect(synced).toBe(1);
          const before = sent.length;
          rejectQuery?.(new Error("late failure"));
          await vi.advanceTimersByTimeAsync(10_000);
          expect(sent.length).toBe(before);
          expect(vi.getTimerCount()).toBe(0);
          expect(unhandled).not.toHaveBeenCalled();
        } finally {
          process.off("unhandledRejection", unhandled);
        }
      });

      it("swallows a rejection that arrives after the deadline already fired", async () => {
        const unhandled = vi.fn();
        process.on("unhandledRejection", unhandled);
        try {
          const rejects: ((error: Error) => void)[] = [];
          const locks: PendingCloseLocks = {
            request: () => new Promise(() => undefined),
            query: () =>
              new Promise((_resolve, reject) => {
                rejects.push(reject);
              }),
          };
          const { first } = silentPeer(locks);
          let synced = 0;
          void first.hold("k", {}).then(() => (synced += 1));
          await vi.advanceTimersByTimeAsync(1_500);
          for (const reject of rejects) reject(new Error("late failure"));
          await vi.advanceTimersByTimeAsync(20_000);
          expect(synced).toBe(1);
          expect(unhandled).not.toHaveBeenCalled();
        } finally {
          process.off("unhandledRejection", unhandled);
        }
      });
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

  describe("bfcache hold restoration", () => {
    /** Product windows have independent lifecycle targets, like real windows. */
    const lifecycle = () => {
      const target = new EventTarget();
      const inside = <T>(run: () => T): T => {
        const previous = globalThis.window;
        vi.stubGlobal("window", target);
        try {
          return run();
        } finally {
          vi.stubGlobal("window", previous);
        }
      };
      const pagehide = () => target.dispatchEvent(new Event("pagehide"));
      const pageshow = (persisted: boolean) => {
        const event = new Event("pageshow");
        Object.defineProperty(event, "persisted", { value: persisted });
        target.dispatchEvent(event);
      };
      return { inside, pagehide, pageshow };
    };
    const pair = () => {
      const bus = new Bus();
      const ownerWindow = lifecycle();
      const siblingWindow = lifecycle();
      const owner = ownerWindow.inside(
        () =>
          new PendingCloseRegistry({ channel: bus.endpoint(), window: "o" }),
      );
      const sibling = siblingWindow.inside(
        () =>
          new PendingCloseRegistry({ channel: bus.endpoint(), window: "s" }),
      );
      return { owner, sibling, ownerWindow, siblingWindow };
    };
    afterEach(() => vi.unstubAllGlobals());

    it("restores an already-sent hold before a sibling can reconcile after a persisted pageshow", () => {
      const { owner, sibling, ownerWindow } = pair();
      const token = {};
      void owner.hold("k", token);
      owner.markSent("k", token);
      expect(sibling.has("k")).toBe(true);
      ownerWindow.pagehide();
      expect(sibling.has("k")).toBe(false);
      expect(owner.holds("k", token)).toBe(false);
      ownerWindow.pageshow(true);
      expect(owner.holds("k", token)).toBe(true);
      expect(owner.has("k")).toBe(true);
      expect(sibling.has("k")).toBe(true);
      // The restored hold is the original owner and settles normally.
      owner.settle("k");
      expect(owner.has("k")).toBe(false);
      expect(sibling.has("k")).toBe(false);
      ownerWindow.pagehide();
      ownerWindow.pageshow(true);
      expect(owner.has("k")).toBe(false);
      owner.dispose();
      sibling.dispose();
    });

    it("keeps a sibling's descriptor through reconciliation after restoration", () => {
      const bus = new Bus();
      const ownerWindow = lifecycle();
      const siblingWindow = lifecycle();
      const owner = new SessionTabsStore(
        ownerWindow.inside(
          () =>
            new PendingCloseRegistry({ channel: bus.endpoint(), window: "o" }),
        ),
      );
      const sibling = new SessionTabsStore(
        siblingWindow.inside(
          () =>
            new PendingCloseRegistry({ channel: bus.endpoint(), window: "s" }),
        ),
      );
      sibling.open("retained", page("p1", "x"));
      const token = {};
      void owner.holdPendingClose("x", "p1", token);
      owner.markPendingCloseSent("x", "p1", token);
      ownerWindow.pagehide();
      ownerWindow.pageshow(true);
      sibling.reconcilePages("retained", "x", []);
      expect(sibling.snapshot("retained").tabs.map(sessionTabKey)).toContain(
        sessionTabKey(page("p1", "x")),
      );
      owner.releasePendingClose("x", "p1", token);
      sibling.reconcilePages("retained", "x", []);
      expect(sibling.snapshot("retained").tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
      ]);
    });

    it("restores only sent holds across several owners and keys", () => {
      const { owner, sibling, ownerWindow } = pair();
      const [a, b, c, d] = [{}, {}, {}, {}];
      void owner.hold("k1", a);
      void owner.hold("k1", b);
      void owner.hold("k2", c);
      void owner.hold("pre-send", d);
      owner.markSent("k1", a);
      owner.markSent("k1", b);
      owner.markSent("k2", c);
      ownerWindow.pagehide();
      ownerWindow.pageshow(true);
      expect(sibling.has("k1")).toBe(true);
      expect(sibling.has("k2")).toBe(true);
      expect(sibling.has("pre-send")).toBe(false);
      expect(owner.holds("pre-send", d)).toBe(false);
      owner.release("k1", a);
      expect(sibling.has("k1")).toBe(true);
      owner.release("k1", b);
      expect(sibling.has("k1")).toBe(false);
      expect(sibling.has("k2")).toBe(true);
      owner.dispose();
      expect(sibling.has("k2")).toBe(false);
      sibling.dispose();
    });

    it("does not revive a hold withdrawn before it was marked sent", () => {
      const { owner, sibling, ownerWindow } = pair();
      const token = {};
      void owner.hold("k", token);
      ownerWindow.pagehide();
      // The barrier was withdrawn; a late mark cannot recreate the hold.
      owner.markSent("k", token);
      ownerWindow.pageshow(true);
      expect(owner.holds("k", token)).toBe(false);
      expect(sibling.has("k")).toBe(false);
      owner.dispose();
      sibling.dispose();
    });

    it("does not restore on a non-persisted pageshow", () => {
      const { owner, sibling, ownerWindow } = pair();
      const token = {};
      void owner.hold("plain", token);
      owner.markSent("plain", token);
      ownerWindow.pagehide();
      ownerWindow.pageshow(false);
      expect(owner.holds("plain", token)).toBe(false);
      expect(sibling.has("plain")).toBe(false);
      owner.dispose();
      sibling.dispose();
    });

    it("does not restore a hold released while suspended, but restores its siblings", () => {
      const { owner, sibling, ownerWindow } = pair();
      const [released, kept] = [{}, {}];
      void owner.hold("released", released);
      owner.markSent("released", released);
      void owner.hold("kept", kept);
      owner.markSent("kept", kept);
      ownerWindow.pagehide();
      owner.release("released", released);
      ownerWindow.pageshow(true);
      expect(sibling.has("released")).toBe(false);
      expect(sibling.has("kept")).toBe(true);
      owner.dispose();
      sibling.dispose();
    });

    it("does not restore holds after permanent disposal", () => {
      const { owner, sibling, ownerWindow } = pair();
      const token = {};
      void owner.hold("k", token);
      owner.markSent("k", token);
      ownerWindow.pagehide();
      owner.dispose();
      ownerWindow.pageshow(true);
      expect(sibling.has("k")).toBe(false);
      expect(owner.holds("k", token)).toBe(false);
      sibling.dispose();
    });

    it("does not resurrect a hold settled while suspended", () => {
      const { owner, sibling, ownerWindow } = pair();
      const token = {};
      void owner.hold("k", token);
      owner.markSent("k", token);
      ownerWindow.pagehide();
      owner.settle("k");
      ownerWindow.pageshow(true);
      expect(owner.has("k")).toBe(false);
      expect(sibling.has("k")).toBe(false);
      owner.dispose();
      sibling.dispose();
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
