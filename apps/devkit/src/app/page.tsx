import Link from "next/link";

import { DevkitShell } from "@/components/devkit-shell";
import {
  DevkitRoute,
  MiniAppIntegrationMode,
  MINI_APP_REGISTRATIONS,
  MiniAppStatus,
} from "@/lib/mini-app-registry";

export default function HomePage() {
  return (
    <DevkitShell title="Devkit Home" currentRoute={DevkitRoute.Home}>
      <section className="dk-stack" aria-label="devkit home">
        <div className="dk-card">
          <p className="dk-eyebrow">Platform Status</p>
          <h2 className="dk-section-title">Shell-only bootstrap is active</h2>
          <p className="dk-paragraph">
            Devkit routes are now reserved with enum-based registration and static
            pages for each canonical mini app.
          </p>
        </div>

        <div className="dk-card">
          <div className="dk-stack">
            <h3 className="dk-subsection-title">Mini App Directory</h3>
            <p className="dk-paragraph">
              Each app follows the shared shell contract while owning its own
              domain feature set.
            </p>
            <div className="dk-app-grid">
              {MINI_APP_REGISTRATIONS.map((app) => (
                <Link key={app.id} href={app.route} className="dk-app-link">
                  <div className="dk-app-title-row">
                    <p className="dk-app-title">{app.title}</p>
                    <span
                      className={`dk-badge ${
                        app.status === MiniAppStatus.Live
                          ? "dk-badge-live"
                          : "dk-badge-placeholder"
                      }`}
                    >
                      {app.status === MiniAppStatus.Live ? "Live" : "Placeholder"}
                    </span>
                  </div>
                  <p className="dk-app-route">{app.route}</p>
                  <p className="dk-subtle">
                    Integration:{" "}
                    <span
                      className={`dk-badge ${
                        app.integrationMode === MiniAppIntegrationMode.BackendCoupled
                          ? "dk-badge-backend"
                          : "dk-badge-shell"
                      }`}
                    >
                      {app.integrationMode}
                    </span>
                  </p>
                </Link>
              ))}
            </div>
          </div>
        </div>

        <div className="dk-card dk-card-muted">
          <h3 className="dk-subsection-title">Routing Contract</h3>
          <p className="dk-paragraph">
            All mini apps are exposed under <code className="dk-mono">/apps/&lt;id&gt;</code>.
          </p>
        </div>
      </section>
    </DevkitShell>
  );
}
