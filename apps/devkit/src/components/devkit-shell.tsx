import Link from "next/link";
import { ReactNode } from "react";

import {
  type AuthGuard,
  AuthGuardDecision,
  evaluateAuthGuard,
} from "@/lib/auth-guard";
import {
  DevkitMiniAppId,
  DevkitRoute,
  MINI_APP_REGISTRATIONS,
} from "@/lib/mini-app-registry";
import { LogEvent, logError, logInfo } from "@/lib/logger";

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
      <main style={{ padding: "2rem" }}>
        <h1>Access denied</h1>
        <p>This route is blocked by the current auth guard policy.</p>
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
    <div
      style={{
        minHeight: "100vh",
        display: "grid",
        gridTemplateRows: "auto 1fr",
      }}
    >
      <header
        style={{
          borderBottom: "1px solid #d7e2ea",
          backgroundColor: "#ffffff",
          padding: "1rem 2rem",
        }}
      >
        <div
          style={{
            maxWidth: "1100px",
            margin: "0 auto",
          }}
        >
          <p
            style={{
              margin: "0 0 0.5rem",
              fontSize: "0.75rem",
              letterSpacing: "0.08em",
              textTransform: "uppercase",
              color: "#3f4f63",
            }}
          >
            Devkit Shell
          </p>
          <h1 style={{ margin: "0 0 1rem" }}>{title}</h1>
          <nav aria-label="Mini app navigation">
            <ul
              style={{
                margin: 0,
                padding: 0,
                listStyle: "none",
                display: "flex",
                gap: "1rem",
                flexWrap: "wrap",
              }}
            >
              {MINI_APP_REGISTRATIONS.map((registration) => (
                <li key={registration.id}>
                  <Link
                    href={registration.route}
                    aria-current={
                      registration.route === currentRoute ? "page" : undefined
                    }
                    style={{
                      padding: "0.4rem 0.75rem",
                      borderRadius: "999px",
                      border:
                        registration.route === currentRoute
                          ? "1px solid #0c5fca"
                          : "1px solid #d7e2ea",
                      backgroundColor:
                        registration.route === currentRoute
                          ? "#e9f3ff"
                          : "#ffffff",
                      color: "#17324f",
                      display: "inline-block",
                    }}
                  >
                    {registration.title}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        </div>
      </header>
      <main
        style={{
          maxWidth: "1100px",
          width: "100%",
          margin: "0 auto",
          padding: "2rem",
        }}
      >
        {children}
      </main>
    </div>
  );
}
