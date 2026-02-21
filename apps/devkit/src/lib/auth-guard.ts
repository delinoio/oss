import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";
import { LogEvent, logError } from "@/lib/logger";

export enum AuthGuardDecision {
  Allow = "allow",
  Deny = "deny",
}

export interface AuthGuardContext {
  route: DevkitRoute;
  miniAppId?: DevkitMiniAppId;
}

export interface AuthGuardResult {
  decision: AuthGuardDecision;
  reason?: string;
}

export type AuthGuard = (context: AuthGuardContext) => AuthGuardResult;

export const allowAllAuthGuard: AuthGuard = () => ({
  decision: AuthGuardDecision.Allow,
});

export function evaluateAuthGuard(
  context: AuthGuardContext,
  guard: AuthGuard = allowAllAuthGuard,
): AuthGuardResult {
  try {
    return guard(context);
  } catch (error) {
    logError({
      event: LogEvent.RouteLoadError,
      route: context.route,
      miniAppId: context.miniAppId,
      message: "Auth guard failed. Falling back to allow.",
      error,
    });
    return {
      decision: AuthGuardDecision.Allow,
      reason: "fallback-allow",
    };
  }
}
