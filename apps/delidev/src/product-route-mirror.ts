// SPDX-License-Identifier: Apache-2.0
import type { ProductRoute } from "./product-route";
import type { SettingsCategory } from "./settings-category";
import { Surface } from "./surface";
import type { UsageTab } from "./usage-tab";

/**
 * Where the Shell is in restoring the document's route selection. It decides
 * only whether the URL may be written, never what is written.
 */
export enum MirrorRestoreState {
  /** The route selection waits for its verification read: the URL is the user's. */
  Pending = "pending",
  /**
   * A transient failure opened the requested selection without verification. It
   * is visible, so it is mirrored exactly as requested (a reload retries it);
   * the Shell ends it as soon as its verification proves it invalid.
   */
  Provisional = "provisional",
  /** Nothing waits for verification. */
  Settled = "settled",
}

/** What the visible Schedules destination currently shows. */
export enum SchedulesMode {
  /** The list and, when one is selected, the Schedule detail or editor. */
  Workspace = "workspace",
  /**
   * An operational occurrence review. The workspace, and the Schedule it
   * retains selected, is hidden and inert behind it.
   */
  OccurrenceReview = "occurrence-review",
}

/** Closed set of route slots a surface can own. */
export enum RouteSlot {
  Session = "session",
  Sidechat = "sidechat",
  Item = "item",
  Category = "category",
  Tab = "tab",
}

/** The one selection-bearing state a screen reports to the Shell. */
export enum RouteItemSource {
  Inbox = "inbox",
  Schedule = "schedule",
  Repository = "repository",
}

/**
 * What the Shell knows when it mirrors the route. Selections of screens that
 * are not the visible surface are retained by those screens and are inputs only
 * so the table can prove they are never read.
 */
export interface MirrorInput {
  surface: Surface;
  restore: MirrorRestoreState;
  schedules: SchedulesMode;
  /** Selected top-level Session ID ("" when none). */
  session: string;
  /** Selected Sidechat tab ID under `session`, when that tab is selected. */
  sidechat?: string;
  /** Selection reported by each screen ("" when none). */
  inbox: string;
  schedule: string;
  repository: string;
  category?: SettingsCategory;
  tab?: UsageTab;
}

/**
 * Slots each surface owns. A surface writes only its own slots, so state that a
 * hidden workspace retains (a Session selected before Inbox opened, a
 * repository chosen before Settings opened) can never reach another screen's URL.
 *
 * | Surface                              | Slots                |
 * | ------------------------------------ | -------------------- |
 * | Sessions                             | session, sidechat    |
 * | Inbox / Schedules / Pull requests    | item                 |
 * | Settings                             | category             |
 * | Usage                                | tab                  |
 * | New session / New general chat / Search | none (screen only) |
 */
export const surfaceSlots: Readonly<Record<Surface, readonly RouteSlot[]>> = {
  [Surface.Sessions]: [RouteSlot.Session, RouteSlot.Sidechat],
  [Surface.Inbox]: [RouteSlot.Item],
  [Surface.Schedules]: [RouteSlot.Item],
  [Surface.PullRequests]: [RouteSlot.Item],
  [Surface.Settings]: [RouteSlot.Category],
  [Surface.Usage]: [RouteSlot.Tab],
  [Surface.NewSession]: [],
  [Surface.NewGeneralChat]: [],
  [Surface.Search]: [],
};

/** Which screen reports the item of each item-bearing surface. */
export const itemSources: Readonly<Partial<Record<Surface, RouteItemSource>>> =
  {
    [Surface.Inbox]: RouteItemSource.Inbox,
    [Surface.Schedules]: RouteItemSource.Schedule,
    [Surface.PullRequests]: RouteItemSource.Repository,
  };

/**
 * Whether the Schedules selection is visible. The occurrence review hides the
 * workspace that retains the previously selected Schedule, so that selection
 * must not describe the review; a reload from the review opens the
 * selection-free Schedules destination.
 */
export const schedulesShowSelection: Readonly<Record<SchedulesMode, boolean>> =
  {
    [SchedulesMode.Workspace]: true,
    [SchedulesMode.OccurrenceReview]: false,
  };

/**
 * Whether the URL may be written in each restore state. `Pending` holds, so the
 * original route stays until its selection is verified; `Provisional` and
 * `Settled` write the visible selection.
 */
export const restoreWritesRoute: Readonly<Record<MirrorRestoreState, boolean>> =
  {
    [MirrorRestoreState.Pending]: false,
    [MirrorRestoreState.Provisional]: true,
    [MirrorRestoreState.Settled]: true,
  };

function itemOf(input: MirrorInput): string {
  const source = itemSources[input.surface];
  if (source === RouteItemSource.Inbox) return input.inbox;
  if (source === RouteItemSource.Repository) return input.repository;
  if (
    source === RouteItemSource.Schedule &&
    schedulesShowSelection[input.schedules]
  )
    return input.schedule;
  return "";
}

/**
 * Derives the product route to mirror into the URL, or undefined to leave the
 * URL untouched. It reads only the slots of the visible surface, only from
 * state that surface shows, and nothing at all while a restore is pending.
 */
export function deriveProductRoute(
  input: MirrorInput,
): ProductRoute | undefined {
  if (!restoreWritesRoute[input.restore]) return undefined;
  const route: ProductRoute = { surface: input.surface };
  for (const slot of surfaceSlots[input.surface]) {
    switch (slot) {
      case RouteSlot.Session:
        if (input.session) route.session = input.session;
        break;
      case RouteSlot.Sidechat:
        // A Sidechat is only ever a child of a selected Session.
        if (input.session && input.sidechat) route.sidechat = input.sidechat;
        break;
      case RouteSlot.Item: {
        const item = itemOf(input);
        if (item) route.item = item;
        break;
      }
      case RouteSlot.Category:
        route.category = input.category;
        break;
      case RouteSlot.Tab:
        route.tab = input.tab;
        break;
    }
  }
  return route;
}
