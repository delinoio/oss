// SPDX-License-Identifier: Apache-2.0
import { isEntityId } from "@delinoio/delidev-api-client";
import { SettingsCategory } from "./settings-category";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";

/**
 * Closed set of product route query parameters. The route stores only enum
 * values and validated opaque IDs, never titles, prompts, credentials, search
 * text or other user content.
 */
export enum ProductRouteParam {
  Screen = "screen",
  Session = "session",
  Sidechat = "sidechat",
  Item = "item",
  Category = "category",
  Tab = "tab",
}

/** Route state of the current top-level screen and its selected item. */
export interface ProductRoute {
  surface: Surface;
  /** Sessions: the selected top-level Session ID. */
  session?: string;
  /** Sessions: the selected Sidechat child ID under `session`. */
  sidechat?: string;
  /** Inbox entry, Schedule or Pull requests repository ID. */
  item?: string;
  /** Settings: the selected category. */
  category?: SettingsCategory;
  /** Usage: the selected tab. */
  tab?: UsageTab;
}

/** Returns the ID when it is a canonical entity ID (bounded, opaque), otherwise undefined. */
export function validRouteId(value: string | null | undefined) {
  return value && isEntityId(value) ? value : undefined;
}

const surfaces: ReadonlySet<string> = new Set(Object.values(Surface));
const categories: ReadonlySet<string> = new Set(
  Object.values(SettingsCategory),
);
const usageTabs: ReadonlySet<string> = new Set(Object.values(UsageTab));

const routeParams: readonly string[] = Object.values(ProductRouteParam);

/**
 * Parses the product route from a URL query string. Returns undefined when the
 * query carries no `screen` parameter. An unknown `screen` value falls back to
 * Sessions Home. Malformed or out-of-scope selections are dropped.
 */
export function parseProductRoute(search: string): ProductRoute | undefined {
  const params = new URLSearchParams(search);
  const screen = params.get(ProductRouteParam.Screen);
  if (screen === null) return undefined;
  if (!surfaces.has(screen)) return { surface: Surface.Sessions };
  const surface = screen as Surface;
  const route: ProductRoute = { surface };
  const read = (name: ProductRouteParam) => validRouteId(params.get(name));
  switch (surface) {
    case Surface.Sessions: {
      const session = read(ProductRouteParam.Session);
      if (session) {
        route.session = session;
        const sidechat = read(ProductRouteParam.Sidechat);
        if (sidechat && sidechat !== session) route.sidechat = sidechat;
      }
      break;
    }
    case Surface.Inbox:
    case Surface.Schedules:
    case Surface.PullRequests: {
      const item = read(ProductRouteParam.Item);
      if (item) route.item = item;
      break;
    }
    case Surface.Settings: {
      const category = params.get(ProductRouteParam.Category);
      if (category !== null && categories.has(category))
        route.category = category as SettingsCategory;
      break;
    }
    case Surface.Usage: {
      const tab = params.get(ProductRouteParam.Tab);
      if (tab !== null && usageTabs.has(tab)) route.tab = tab as UsageTab;
      break;
    }
    default:
      break;
  }
  return route;
}

/**
 * Serializes the route into a query string ("?..." or "") on top of the current
 * one. Unrelated parameters keep their order and values; only the route
 * parameters are replaced.
 */
export function serializeProductRoute(
  route: ProductRoute,
  currentSearch: string,
): string {
  const params = new URLSearchParams(currentSearch);
  for (const name of routeParams) params.delete(name);
  params.set(ProductRouteParam.Screen, route.surface);
  const set = (name: ProductRouteParam, value: string | undefined) => {
    if (value) params.set(name, value);
  };
  if (route.surface === Surface.Sessions) {
    set(ProductRouteParam.Session, validRouteId(route.session));
    if (route.session)
      set(ProductRouteParam.Sidechat, validRouteId(route.sidechat));
  } else if (
    route.surface === Surface.Inbox ||
    route.surface === Surface.Schedules ||
    route.surface === Surface.PullRequests
  )
    set(ProductRouteParam.Item, validRouteId(route.item));
  else if (route.surface === Surface.Settings)
    set(ProductRouteParam.Category, route.category);
  else if (route.surface === Surface.Usage)
    set(ProductRouteParam.Tab, route.tab);
  const query = params.toString();
  return query ? `?${query}` : "";
}

/**
 * Replaces the current history entry with the route, keeping the path, hash
 * (including the `#main` skip link) and unrelated parameters. Never pushes an
 * entry. Returns whether the URL changed.
 */
export function writeProductRoute(route: ProductRoute): boolean {
  try {
    const { pathname, search, hash } = window.location;
    const next = serializeProductRoute(route, search);
    if (next === search) return false;
    window.history.replaceState(
      window.history.state,
      "",
      pathname + next + hash,
    );
    return true;
  } catch (error) {
    // Some webviews throttle history writes; navigation itself is unaffected.
    console.warn("delidev.product_route.write_failed", {
      surface: route.surface,
      error: error instanceof Error ? error.name : "unknown",
    });
    return false;
  }
}

let claimedScope: string | undefined;

/**
 * Claims the document's initial route for one connection identity scope.
 * The first Shell of a document, and a remount of the same identity, read the
 * current URL. A different identity never receives the previous identity's
 * route, so a connection change cannot open another account's selection.
 */
export function claimInitialRoute(scope: string): ProductRoute | undefined {
  const previous = claimedScope;
  claimedScope = scope;
  if (previous !== undefined && previous !== scope) return undefined;
  try {
    return parseProductRoute(window.location.search);
  } catch {
    return undefined;
  }
}

/** Test hook: forget the claimed identity, as a fresh document would. */
export function resetProductRouteClaim() {
  claimedScope = undefined;
}
