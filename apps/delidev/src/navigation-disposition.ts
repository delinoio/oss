// SPDX-License-Identifier: Apache-2.0
import { invoke, isTauri } from "@tauri-apps/api/core";
import { validRouteId, type ProductRoute } from "./product-route";
import { ShortcutPlatform, shortcutPlatform } from "./shortcuts";
import { Surface } from "./surface";

/**
 * Closed set of ways an internal destination control can open its target.
 * `NewWindow` is selected only by a platform-primary modified mouse click.
 */
export enum NavigationDisposition {
  CurrentWindow = "current-window",
  NewWindow = "new-window",
}

/** The click fields that decide the disposition. */
export interface NavigationClick {
  button: number;
  /** Zero for keyboard and assistive activations, positive for pointer clicks. */
  detail: number;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

/**
 * Classifies one click. Only the primary mouse button with exactly the
 * platform primary modifier (Command on macOS, Control elsewhere), with or
 * without Shift, selects a new window. Alt, the other platform's modifier, a
 * Control plus Meta combination, other mouse buttons and keyboard activation
 * keep the current window.
 */
export function navigationDisposition(
  event: NavigationClick,
  platform: ShortcutPlatform = shortcutPlatform(),
): NavigationDisposition {
  const mac = platform === ShortcutPlatform.Mac;
  const primary = mac
    ? event.metaKey && !event.ctrlKey
    : event.ctrlKey && !event.metaKey;
  return event.button === 0 && event.detail > 0 && primary && !event.altKey
    ? NavigationDisposition.NewWindow
    : NavigationDisposition.CurrentWindow;
}

/** Native failure codes that a destination window operation can surface. */
export enum DestinationWindowFailure {
  Busy = "busy",
  Stopped = "stopped",
  PermissionDenied = "permission-denied",
  InvalidEvidence = "invalid-evidence",
  InvalidInput = "invalid-input",
  SidecarFailed = "sidecar-failed",
  TimedOut = "timed-out",
  Incompatible = "incompatible",
  Other = "other",
}

export function destinationWindowFailure(
  error: unknown,
): DestinationWindowFailure {
  const code = typeof error === "string" ? error : undefined;
  return (
    Object.values(DestinationWindowFailure).find((value) => value === code) ??
    DestinationWindowFailure.Other
  );
}

/** The typed destination the native host validates again and serializes. */
export interface DestinationRequest {
  screen: Surface;
  session?: string;
  sidechat?: string;
  item?: string;
  category?: ProductRoute["category"];
  tab?: ProductRoute["tab"];
}

const creationScreens: ReadonlySet<Surface> = new Set([
  Surface.NewSession,
  Surface.NewGeneralChat,
]);

/**
 * Builds the native request from a product route: closed enum values and
 * canonical opaque IDs only, never titles, search text or drafts. Creation
 * screens are action controls, not destinations, and have no request.
 */
export function destinationRequest(
  route: ProductRoute,
): DestinationRequest | undefined {
  if (creationScreens.has(route.surface)) return undefined;
  const request: DestinationRequest = { screen: route.surface };
  if (route.surface === Surface.Sessions) {
    const session = validRouteId(route.session);
    if (session) {
      request.session = session;
      const sidechat = validRouteId(route.sidechat);
      if (sidechat && sidechat !== session) request.sidechat = sidechat;
    }
  } else if (
    route.surface === Surface.Inbox ||
    route.surface === Surface.Schedules ||
    route.surface === Surface.PullRequests
  ) {
    const item = validRouteId(route.item);
    if (item) request.item = item;
  } else if (route.surface === Surface.Settings) {
    if (route.category) request.category = route.category;
  } else if (route.surface === Surface.Usage) {
    if (route.tab) request.tab = route.tab;
  }
  return request;
}

/** Web previews and mobile have no native window host and keep current-window navigation. */
export const destinationWindowAvailable = () => isTauri();

/**
 * Asks the native host to open one more product window for the invoking
 * window. The host binds the operation to that window, rechecks its original
 * connection authority and validates the destination; this function never
 * retries or falls back to current-window navigation.
 */
export async function openDestinationWindow(
  route: ProductRoute,
): Promise<void> {
  const destination = destinationRequest(route);
  if (!destination) throw DestinationWindowFailure.InvalidInput;
  await invoke("open_destination_window", { destination });
}
