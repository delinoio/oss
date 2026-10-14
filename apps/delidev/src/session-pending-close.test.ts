import { describe, expect, it } from "vitest";
import {
  PendingCloseRegistry,
  type PendingCloseChannel,
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
  close() {}
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
});
