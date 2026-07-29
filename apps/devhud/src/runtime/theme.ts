import {
  ThemePreference,
  isStructuredShortcut,
} from "../persistence/contracts";
import type {
  PersistenceResetOutcome,
} from "../persistence/storage";

const THEME_CHANNEL = "devhud.theme";
const RESET_CHANNEL = "devhud.reset";
const SESSION_CHANNEL = "devhud.session";

enum SessionSignal {
  Invalidated = "invalidated",
}

function isThemePreference(value: unknown): value is ThemePreference {
  return Object.values(ThemePreference).includes(value as ThemePreference);
}

export function publishThemePreference(theme: ThemePreference): void {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(THEME_CHANNEL);
  channel.postMessage(theme);
  channel.close();
}

export function subscribeToThemePreference(
  listener: (theme: ThemePreference) => void,
): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  const channel = new BroadcastChannel(THEME_CHANNEL);
  channel.addEventListener("message", (event: MessageEvent<unknown>) => {
    if (isThemePreference(event.data)) listener(event.data);
  });
  return () => channel.close();
}

function isPersistenceResetOutcome(
  value: unknown,
): value is PersistenceResetOutcome {
  if (typeof value !== "object" || value === null || !("status" in value)) {
    return false;
  }
  if (
    value.status === "complete" ||
    value.status === "partially-retained" ||
    value.status === "cleanup-failed"
  ) {
    return true;
  }
  if (
    value.status !== "integration-rollback-failed" ||
    !("shortcut" in value) ||
    !("launchAtLogin" in value) ||
    (value.shortcut !== null && !isStructuredShortcut(value.shortcut)) ||
    (value.launchAtLogin !== null &&
      typeof value.launchAtLogin !== "boolean")
  ) {
    return false;
  }
  return true;
}

export function publishPersistenceReset(
  outcome: PersistenceResetOutcome,
): void {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(RESET_CHANNEL);
  channel.postMessage(outcome);
  channel.close();
}

export function subscribeToPersistenceReset(
  listener: (outcome: PersistenceResetOutcome) => void,
): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  const channel = new BroadcastChannel(RESET_CHANNEL);
  channel.addEventListener("message", (event: MessageEvent<unknown>) => {
    if (isPersistenceResetOutcome(event.data)) listener(event.data);
  });
  return () => channel.close();
}

export function publishSessionInvalidation(): void {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(SESSION_CHANNEL);
  channel.postMessage(SessionSignal.Invalidated);
  channel.close();
}

export function subscribeToSessionInvalidation(
  listener: () => void,
): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  const channel = new BroadcastChannel(SESSION_CHANNEL);
  channel.addEventListener("message", (event: MessageEvent<unknown>) => {
    if (event.data === SessionSignal.Invalidated) listener();
  });
  return () => channel.close();
}
