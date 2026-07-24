export type ApplicationPlatform = "desktop" | "mobile";

const MOBILE_USER_AGENT = /Android|iPhone|iPad|iPod/u;

/**
 * Select the shell before React's first render. The native runtime command is
 * asynchronous, so it cannot choose the initial mobile shell during startup.
 */
export function detectApplicationPlatform(userAgent: string): ApplicationPlatform {
  return MOBILE_USER_AGENT.test(userAgent) ? "mobile" : "desktop";
}
