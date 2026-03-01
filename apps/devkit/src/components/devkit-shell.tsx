import { ReactNode } from "react";

import {
  type AuthGuard,
  AuthGuardDecision,
  evaluateAuthGuard,
} from "@/lib/auth-guard";
import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";
import { LogEvent, logError, logInfo } from "@/lib/logger";

import { DevkitShellLayout } from "./devkit-shell-layout";

export interface DevkitShellProps {
  title: string;
  currentRoute: DevkitRoute;
  children: ReactNode;
  miniAppId?: DevkitMiniAppId;
  guard?: AuthGuard;
}

export function DevkitShell({
  title,
  currentRoute,
  miniAppId,
  guard,
  children,
}: DevkitShellProps) {
  const guardResult = evaluateAuthGuard(
    { route: currentRoute, miniAppId },
    guard,
  );

  if (guardResult.decision === AuthGuardDecision.Deny) {
    logError({
      event: LogEvent.RouteLoadError,
      route: currentRoute,
      miniAppId,
      message: guardResult.reason ?? "Route access denied.",
    });

    return (
      <main className="dk-main">
        <section className="dk-card" aria-label="access denied">
          <p className="dk-eyebrow">Guard Policy</p>
          <h1 className="dk-section-title">Access denied</h1>
          <p className="dk-paragraph">
            This route is blocked by the current auth guard policy.
          </p>
        </section>
      </main>
    );
  }

  logInfo({
    event: LogEvent.RouteRender,
    route: currentRoute,
    miniAppId,
    message: "Devkit route rendered.",
  });

  return (
    <DevkitShellLayout title={title} currentRoute={currentRoute}>
      {children}
    </DevkitShellLayout>
  );
}
