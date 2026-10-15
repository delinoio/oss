// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import {
  commitInitialRouteScope,
  readInitialRoute,
  parseProductRoute,
  ProductRouteParam,
  resetProductRouteClaim,
  serializeProductRoute,
  writeProductRoute,
  type ProductRoute,
} from "./product-route";
import { SettingsCategory } from "./settings-category";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";

const id = () => newRequestId();

afterEach(() => {
  resetProductRouteClaim();
  window.history.replaceState(null, "", "/");
});

describe("product route parse and serialize", () => {
  it("round-trips every Surface with only its own selection", () => {
    const session = id(),
      sidechat = id(),
      item = id();
    const routes: ProductRoute[] = [
      { surface: Surface.Sessions },
      { surface: Surface.Sessions, session },
      { surface: Surface.Sessions, session, sidechat },
      { surface: Surface.NewSession },
      { surface: Surface.NewGeneralChat },
      { surface: Surface.PullRequests, item },
      { surface: Surface.Usage, tab: UsageTab.Prices },
      { surface: Surface.Schedules, item },
      { surface: Surface.Inbox, item },
      { surface: Surface.Search },
      { surface: Surface.Settings, category: SettingsCategory.Backups },
    ];
    for (const route of routes)
      expect(parseProductRoute(serializeProductRoute(route, ""))).toEqual(
        route,
      );
  });

  it("serializes selection-free screens as the screen alone", () => {
    expect(serializeProductRoute({ surface: Surface.Search }, "")).toBe(
      "?screen=search",
    );
    expect(
      serializeProductRoute(
        { surface: Surface.NewSession, item: id(), tab: UsageTab.History },
        "",
      ),
    ).toBe("?screen=new-session");
  });

  it("does not carry a stale selection across screens or Sessions Home", () => {
    const query = serializeProductRoute(
      { surface: Surface.Sessions, session: undefined, sidechat: id() },
      "?item=stale",
    );
    expect(query).toBe("?screen=sessions");
    expect(
      parseProductRoute(
        `?screen=usage&session=${id()}&item=${id()}&category=backups&tab=history`,
      ),
    ).toEqual({ surface: Surface.Usage, tab: UsageTab.History });
  });

  it("preserves unrelated parameters while replacing only route parameters", () => {
    const next = serializeProductRoute(
      { surface: Surface.Usage, tab: UsageTab.Prices },
      "?native=1&screen=inbox&item=old&flag",
    );
    const params = new URLSearchParams(next);
    expect(params.get("native")).toBe("1");
    expect(params.get("flag")).toBe("");
    expect(params.get("screen")).toBe("usage");
    expect(params.get("tab")).toBe("prices");
    expect(params.has("item")).toBe(false);
  });

  it("falls back safely for unknown, malformed and oversized values", () => {
    expect(parseProductRoute("")).toBeUndefined();
    expect(parseProductRoute(`?session=${id()}`)).toBeUndefined();
    expect(parseProductRoute("?screen=bogus&session=x")).toEqual({
      surface: Surface.Sessions,
    });
    expect(parseProductRoute("?screen=sessions&session=not-an-id")).toEqual({
      surface: Surface.Sessions,
    });
    expect(parseProductRoute(`?screen=inbox&item=${"a".repeat(4096)}`)).toEqual(
      { surface: Surface.Inbox },
    );
    expect(parseProductRoute("?screen=settings&category=nope")).toEqual({
      surface: Surface.Settings,
    });
    expect(parseProductRoute("?screen=usage&tab=nope")).toEqual({
      surface: Surface.Usage,
    });
    const session = id();
    expect(
      parseProductRoute(`?screen=sessions&sidechat=${id()}&session=${session}`),
    ).toEqual({
      surface: Surface.Sessions,
      session,
      sidechat: expect.any(String),
    });
    expect(
      parseProductRoute(
        `?screen=sessions&session=${session}&sidechat=${session}`,
      ),
    ).toEqual({ surface: Surface.Sessions, session });
    expect(parseProductRoute(`?screen=sessions&sidechat=${id()}`)).toEqual({
      surface: Surface.Sessions,
    });
  });

  it("stores only enum values and opaque IDs", () => {
    const names = new Set(Object.values(ProductRouteParam));
    expect([...names].sort()).toEqual(
      ["category", "item", "screen", "session", "sidechat", "tab"].sort(),
    );
    const query = serializeProductRoute(
      { surface: Surface.Sessions, session: "Secret prompt text" },
      "",
    );
    expect(query).toBe("?screen=sessions");
  });
});

describe("product route URL writes", () => {
  it("replaces the current entry, keeps the #main hash and adds no history", () => {
    window.history.replaceState(null, "", "/index.html?native=1#main");
    const length = window.history.length;
    const push = vi.spyOn(window.history, "pushState");
    expect(writeProductRoute({ surface: Surface.Search })).toBe(true);
    expect(window.location.pathname).toBe("/index.html");
    expect(window.location.search).toBe("?native=1&screen=search");
    expect(window.location.hash).toBe("#main");
    expect(window.history.length).toBe(length);
    expect(push).not.toHaveBeenCalled();
    expect(writeProductRoute({ surface: Surface.Search })).toBe(false);
  });

  it("reports a throttled history write without throwing", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const replace = vi
      .spyOn(window.history, "replaceState")
      .mockImplementation(() => {
        throw new DOMException("throttled", "SecurityError");
      });
    expect(writeProductRoute({ surface: Surface.Search })).toBe(false);
    replace.mockRestore();
    expect(warn).toHaveBeenCalledWith(
      "delidev.product_route.write_failed",
      expect.objectContaining({ surface: Surface.Search }),
    );
  });
});

describe("initial route claim", () => {
  it("gives the route only to the connection identity that first commits it", () => {
    window.history.replaceState(null, "", `/?screen=inbox&item=${id()}`);
    expect(readInitialRoute("server-a")?.surface).toBe(Surface.Inbox);
    commitInitialRouteScope("server-a");
    expect(readInitialRoute("server-a")?.surface).toBe(Surface.Inbox);
    expect(readInitialRoute("server-b")).toBeUndefined();
    commitInitialRouteScope("server-b");
    expect(readInitialRoute("server-a")).toBeUndefined();
  });

  it("answers every uncommitted read the same, however many times it runs", () => {
    window.history.replaceState(null, "", `/?screen=inbox&item=${id()}`);
    commitInitialRouteScope("server-a");
    // A render-time read of a rejected identity changes nothing, so a second
    // (StrictMode) invocation cannot receive what the first one rejected.
    expect(readInitialRoute("server-b")).toBeUndefined();
    expect(readInitialRoute("server-b")).toBeUndefined();
    expect(readInitialRoute("server-a")?.surface).toBe(Surface.Inbox);
  });
});

describe("destination window initial route", () => {
  const session = "018f0000-0000-7000-8000-000000000001";
  const sidechat = "018f0000-0000-7000-8000-000000000002";

  // The native host serializes these exact queries for a new product window
  // (see destination_route.rs); the new renderer reads them before its first
  // route read and restores them through its own authenticated reads.
  const nativeQueries: [string, ProductRoute][] = [
    ["?screen=sessions", { surface: Surface.Sessions }],
    [
      `?screen=sessions&session=${session}`,
      { surface: Surface.Sessions, session },
    ],
    [
      `?screen=sessions&session=${session}&sidechat=${sidechat}`,
      { surface: Surface.Sessions, session, sidechat },
    ],
    [
      `?screen=inbox&item=${session}`,
      { surface: Surface.Inbox, item: session },
    ],
    [
      `?screen=schedules&item=${session}`,
      { surface: Surface.Schedules, item: session },
    ],
    [
      `?screen=pull-requests&item=${session}`,
      { surface: Surface.PullRequests, item: session },
    ],
    [
      "?screen=settings&category=app-information",
      { surface: Surface.Settings, category: SettingsCategory.AppInformation },
    ],
    [
      "?screen=usage&tab=prices",
      { surface: Surface.Usage, tab: UsageTab.Prices },
    ],
    ["?screen=search", { surface: Surface.Search }],
  ];

  for (const [query, route] of nativeQueries) {
    it(`reads ${query.replace(/[0-9a-f-]{36}/g, "<id>")} as its typed route`, () => {
      window.history.replaceState(null, "", `/index.html${query}`);
      expect(readInitialRoute("scope")).toEqual(route);
      // The renderer's serializer produces the same query the host supplies.
      expect(serializeProductRoute(route, "")).toBe(query);
    });
  }

  it("carries no state besides the route: unrelated parameters are absent", () => {
    for (const [query] of nativeQueries) {
      const keys = [...new URLSearchParams(query).keys()];
      for (const key of keys)
        expect(Object.values(ProductRouteParam)).toContain(key);
    }
  });
});
