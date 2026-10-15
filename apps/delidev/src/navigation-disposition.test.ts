import { describe, expect, it } from "vitest";
import {
  DestinationWindowFailure,
  NavigationDisposition,
  destinationRequest,
  destinationWindowFailure,
  navigationDisposition,
  type NavigationClick,
} from "./navigation-disposition";
import { SettingsCategory } from "./settings-category";
import { ShortcutPlatform } from "./shortcuts";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";

const click = (overrides: Partial<NavigationClick> = {}): NavigationClick => ({
  button: 0,
  detail: 1,
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...overrides,
});

const { NewWindow, CurrentWindow } = NavigationDisposition;

// One row per modifier state. Columns: macOS, Windows/Linux.
const matrix: {
  name: string;
  event: Partial<NavigationClick>;
  mac: NavigationDisposition;
  other: NavigationDisposition;
}[] = [
  { name: "plain", event: {}, mac: CurrentWindow, other: CurrentWindow },
  {
    name: "shift only",
    event: { shiftKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "meta",
    event: { metaKey: true },
    mac: NewWindow,
    other: CurrentWindow,
  },
  {
    name: "meta+shift",
    event: { metaKey: true, shiftKey: true },
    mac: NewWindow,
    other: CurrentWindow,
  },
  {
    name: "ctrl",
    event: { ctrlKey: true },
    mac: CurrentWindow,
    other: NewWindow,
  },
  {
    name: "ctrl+shift",
    event: { ctrlKey: true, shiftKey: true },
    mac: CurrentWindow,
    other: NewWindow,
  },
  {
    name: "ctrl+meta",
    event: { ctrlKey: true, metaKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "ctrl+meta+shift",
    event: { ctrlKey: true, metaKey: true, shiftKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "alt",
    event: { altKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "meta+alt",
    event: { metaKey: true, altKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "ctrl+alt",
    event: { ctrlKey: true, altKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
  {
    name: "meta+shift+alt",
    event: { metaKey: true, shiftKey: true, altKey: true },
    mac: CurrentWindow,
    other: CurrentWindow,
  },
];

describe("navigation disposition", () => {
  for (const row of matrix) {
    it(`${row.name}: macOS ${row.mac}, Windows/Linux ${row.other}`, () => {
      expect(
        navigationDisposition(click(row.event), ShortcutPlatform.Mac),
      ).toBe(row.mac);
      expect(
        navigationDisposition(click(row.event), ShortcutPlatform.Other),
      ).toBe(row.other);
    });
  }

  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other]) {
    const primary =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    it(`${platform}: middle and right buttons never select a new window`, () => {
      for (const button of [1, 2, 3, 4]) {
        expect(
          navigationDisposition(click({ ...primary, button }), platform),
        ).toBe(CurrentWindow);
      }
    });
    it(`${platform}: keyboard and assistive activation (detail 0) keeps the current window`, () => {
      expect(
        navigationDisposition(click({ ...primary, detail: 0 }), platform),
      ).toBe(CurrentWindow);
    });
    it(`${platform}: a primary pointer click selects a new window`, () => {
      expect(navigationDisposition(click(primary), platform)).toBe(NewWindow);
    });
  }
});

describe("destination request", () => {
  const session = "018f0000-0000-7000-8000-000000000001";
  const other = "018f0000-0000-7000-8000-000000000002";

  it("carries only the slots of the destination screen", () => {
    expect(destinationRequest({ surface: Surface.Sessions })).toEqual({
      screen: Surface.Sessions,
    });
    expect(
      destinationRequest({
        surface: Surface.Sessions,
        session,
        sidechat: other,
      }),
    ).toEqual({ screen: Surface.Sessions, session, sidechat: other });
    expect(
      destinationRequest({ surface: Surface.Inbox, item: session }),
    ).toEqual({ screen: Surface.Inbox, item: session });
    expect(
      destinationRequest({ surface: Surface.Schedules, item: session }),
    ).toEqual({ screen: Surface.Schedules, item: session });
    expect(
      destinationRequest({ surface: Surface.PullRequests, item: session }),
    ).toEqual({ screen: Surface.PullRequests, item: session });
    expect(
      destinationRequest({
        surface: Surface.Settings,
        category: SettingsCategory.Browser,
      }),
    ).toEqual({ screen: Surface.Settings, category: SettingsCategory.Browser });
    expect(
      destinationRequest({ surface: Surface.Usage, tab: UsageTab.History }),
    ).toEqual({ screen: Surface.Usage, tab: UsageTab.History });
    expect(destinationRequest({ surface: Surface.Search })).toEqual({
      screen: Surface.Search,
    });
    // Slots owned by another screen never leak into the request.
    expect(
      destinationRequest({
        surface: Surface.Usage,
        tab: UsageTab.Prices,
        session,
        item: other,
        category: SettingsCategory.Browser,
      }),
    ).toEqual({ screen: Surface.Usage, tab: UsageTab.Prices });
  });

  it("drops malformed IDs and a Sidechat equal to its Session", () => {
    expect(
      destinationRequest({ surface: Surface.Sessions, session: "not-an-id" }),
    ).toEqual({ screen: Surface.Sessions });
    expect(
      destinationRequest({
        surface: Surface.Sessions,
        session,
        sidechat: session,
      }),
    ).toEqual({ screen: Surface.Sessions, session });
    expect(
      destinationRequest({
        surface: Surface.Inbox,
        item: "https://x.invalid/",
      }),
    ).toEqual({ screen: Surface.Inbox });
  });

  it("has no request for creation screens", () => {
    expect(destinationRequest({ surface: Surface.NewSession })).toBeUndefined();
    expect(
      destinationRequest({ surface: Surface.NewGeneralChat }),
    ).toBeUndefined();
  });
});

describe("destination window failure", () => {
  it("accepts only closed native codes", () => {
    for (const code of Object.values(DestinationWindowFailure))
      expect(destinationWindowFailure(code)).toBe(code);
    expect(destinationWindowFailure("https://example.invalid/")).toBe(
      DestinationWindowFailure.Other,
    );
    expect(destinationWindowFailure(new Error("x"))).toBe(
      DestinationWindowFailure.Other,
    );
  });
});
