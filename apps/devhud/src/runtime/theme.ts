import { ThemePreference } from "../persistence/contracts";

const THEME_CHANNEL = "devhud.theme";

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
