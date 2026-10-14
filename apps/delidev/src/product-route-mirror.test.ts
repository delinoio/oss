// SPDX-License-Identifier: Apache-2.0
import { newRequestId } from "@delinoio/delidev-api-client";
import { describe, expect, it } from "vitest";
import {
  parseProductRoute,
  serializeProductRoute,
  type ProductRoute,
} from "./product-route";
import {
  MirrorRestoreState,
  RouteSlot,
  SchedulesMode,
  deriveProductRoute,
  restoreWritesRoute,
  schedulesShowSelection,
  surfaceSlots,
  type MirrorInput,
} from "./product-route-mirror";
import { SettingsCategory } from "./settings-category";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";

// Every screen's state is populated on purpose: the mirror must read only the
// visible surface's own slots, however much hidden or retained state exists.
const ids = {
  session: newRequestId(),
  sidechat: newRequestId(),
  inbox: newRequestId(),
  schedule: newRequestId(),
  repository: newRequestId(),
};
const populated = (overrides: Partial<MirrorInput> = {}): MirrorInput => ({
  surface: Surface.Sessions,
  restore: MirrorRestoreState.Settled,
  schedules: SchedulesMode.Workspace,
  session: ids.session,
  sidechat: ids.sidechat,
  inbox: ids.inbox,
  schedule: ids.schedule,
  repository: ids.repository,
  category: SettingsCategory.Diagnostics,
  tab: UsageTab.History,
  ...overrides,
});

describe("product route mirror table", () => {
  // Surface x retained state: one row per surface, every other screen's
  // selection retained but hidden.
  const surfaceRows: Array<[Surface, ProductRoute]> = [
    [
      Surface.Sessions,
      {
        surface: Surface.Sessions,
        session: ids.session,
        sidechat: ids.sidechat,
      },
    ],
    [Surface.Inbox, { surface: Surface.Inbox, item: ids.inbox }],
    [Surface.Schedules, { surface: Surface.Schedules, item: ids.schedule }],
    [
      Surface.PullRequests,
      { surface: Surface.PullRequests, item: ids.repository },
    ],
    [
      Surface.Settings,
      { surface: Surface.Settings, category: SettingsCategory.Diagnostics },
    ],
    [Surface.Usage, { surface: Surface.Usage, tab: UsageTab.History }],
    [Surface.NewSession, { surface: Surface.NewSession }],
    [Surface.NewGeneralChat, { surface: Surface.NewGeneralChat }],
    [Surface.Search, { surface: Surface.Search }],
  ];

  it("has a row for every surface", () => {
    expect(surfaceRows.map(([surface]) => surface).sort()).toEqual(
      Object.values(Surface).sort(),
    );
    expect(Object.keys(surfaceSlots).sort()).toEqual(
      Object.values(Surface).sort(),
    );
  });

  it.each(surfaceRows)(
    "%s writes only its own slots from the visible state",
    (surface, expected) => {
      expect(deriveProductRoute(populated({ surface }))).toEqual(expected);
    },
  );

  it.each(surfaceRows)(
    "%s never reads a slot it does not own, whatever the others retain",
    (surface) => {
      const route = deriveProductRoute(populated({ surface }))!;
      const owned = new Set<string>(
        surfaceSlots[surface].map((slot) => slot.toString()),
      );
      const written = (
        [
          [RouteSlot.Session, route.session],
          [RouteSlot.Sidechat, route.sidechat],
          [RouteSlot.Item, route.item],
          [RouteSlot.Category, route.category],
          [RouteSlot.Tab, route.tab],
        ] as const
      )
        .filter(([, value]) => value !== undefined)
        .map(([slot]) => slot.toString());
      for (const slot of written) expect(owned.has(slot)).toBe(true);
      // The serialized URL carries nothing else either.
      const query = new URLSearchParams(serializeProductRoute(route, ""));
      const params = [...query.keys()].filter((key) => key !== "screen");
      for (const key of params) expect(owned.has(key)).toBe(true);
    },
  );

  // Restore state x surface: only a pending restore holds the URL; a
  // provisional (transiently unverified, visible) selection is mirrored as
  // requested so a reload retries it.
  describe.each(Object.values(MirrorRestoreState))("restore %s", (restore) => {
    it.each(surfaceRows)("%s", (surface, expected) => {
      const route = deriveProductRoute(populated({ surface, restore }));
      if (restore === MirrorRestoreState.Pending) expect(route).toBeUndefined();
      else expect(route).toEqual(expected);
    });
  });

  it("covers every restore state in the table", () => {
    expect(Object.keys(restoreWritesRoute).sort()).toEqual(
      Object.values(MirrorRestoreState).sort(),
    );
  });

  // Schedules mode x selection x surface: an occurrence review hides the
  // workspace that retains the selected Schedule, so it is never mirrored.
  describe("Schedules destination modes", () => {
    const rows: Array<[SchedulesMode, string, string | undefined]> = [
      [SchedulesMode.Workspace, ids.schedule, ids.schedule],
      [SchedulesMode.Workspace, "", undefined],
      [SchedulesMode.OccurrenceReview, ids.schedule, undefined],
      [SchedulesMode.OccurrenceReview, "", undefined],
    ];
    it.each(rows)("%s with selection %j", (schedules, schedule, item) => {
      const route = deriveProductRoute(
        populated({ surface: Surface.Schedules, schedules, schedule }),
      );
      expect(route).toEqual(
        item === undefined
          ? { surface: Surface.Schedules }
          : { surface: Surface.Schedules, item },
      );
    });

    it("covers every mode in the table", () => {
      expect(Object.keys(schedulesShowSelection).sort()).toEqual(
        Object.values(SchedulesMode).sort(),
      );
    });

    it("leaves other surfaces' items alone while a review retains a Schedule", () => {
      for (const [surface, item] of [
        [Surface.Inbox, ids.inbox],
        [Surface.PullRequests, ids.repository],
      ] as const)
        expect(
          deriveProductRoute(
            populated({
              surface,
              schedules: SchedulesMode.OccurrenceReview,
            }),
          ),
        ).toEqual({ surface, item });
    });

    it("reloads from a review into the selection-free destination", () => {
      const route = deriveProductRoute(
        populated({
          surface: Surface.Schedules,
          schedules: SchedulesMode.OccurrenceReview,
        }),
      )!;
      const parsed = parseProductRoute(serializeProductRoute(route, ""));
      expect(parsed).toEqual({ surface: Surface.Schedules });
    });
  });

  // Session slot x Sidechat slot.
  describe("Sessions selection", () => {
    it.each([
      ["no Session", "", undefined, { surface: Surface.Sessions }],
      [
        "no Session retaining a Sidechat",
        "",
        ids.sidechat,
        { surface: Surface.Sessions },
      ],
      [
        "a Session",
        ids.session,
        undefined,
        { surface: Surface.Sessions, session: ids.session },
      ],
      [
        "a Session with a selected Sidechat",
        ids.session,
        ids.sidechat,
        {
          surface: Surface.Sessions,
          session: ids.session,
          sidechat: ids.sidechat,
        },
      ],
    ] as const)("%s", (_name, session, sidechat, expected) => {
      expect(
        deriveProductRoute(
          populated({ surface: Surface.Sessions, session, sidechat }),
        ),
      ).toEqual(expected);
    });
  });

  it("writes no item or category for a selection-free surface", () => {
    expect(
      deriveProductRoute(
        populated({
          surface: Surface.Inbox,
          inbox: "",
        }),
      ),
    ).toEqual({ surface: Surface.Inbox });
    expect(
      deriveProductRoute(
        populated({ surface: Surface.Settings, category: undefined }),
      ),
    ).toEqual({ surface: Surface.Settings, category: undefined });
    expect(
      serializeProductRoute(
        deriveProductRoute(
          populated({ surface: Surface.Settings, category: undefined }),
        )!,
        "",
      ),
    ).toBe("?screen=settings");
  });

  it("round-trips every row through the URL", () => {
    for (const [surface] of surfaceRows) {
      const route = deriveProductRoute(populated({ surface }))!;
      const parsed = parseProductRoute(serializeProductRoute(route, ""));
      expect(parsed?.surface).toBe(surface);
      expect(parsed?.session).toBe(route.session);
      expect(parsed?.sidechat).toBe(route.sidechat);
      expect(parsed?.item).toBe(route.item);
      expect(parsed?.category).toBe(route.category);
      expect(parsed?.tab).toBe(route.tab);
    }
  });
});
