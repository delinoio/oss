export type ApplicationPlatform = "desktop" | "mobile";

const MOBILE_USER_AGENT = /Android|iPhone|iPad|iPod/u;

/**
 * Select the shell before React's first render. The native runtime command is
 * asynchronous, so it cannot choose the initial mobile shell during startup.
 */
export function detectApplicationPlatform(userAgent: string): ApplicationPlatform {
  return MOBILE_USER_AGENT.test(userAgent) ? "mobile" : "desktop";
}

/** The native runtime is authoritative when a system webview reports after startup. */
export function platformForRuntime(
  runtime: "cef" | "system-webview",
): ApplicationPlatform {
  return runtime === "system-webview" ? "mobile" : "desktop";
}
