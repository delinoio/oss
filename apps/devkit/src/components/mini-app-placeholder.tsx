import { MiniAppRegistration } from "@/lib/mini-app-registry";
import { LogEvent, logInfo } from "@/lib/logger";

export interface MiniAppPlaceholderProps {
  app: MiniAppRegistration;
}

export function MiniAppPlaceholder({ app }: MiniAppPlaceholderProps) {
  logInfo({
    event: LogEvent.Navigation,
    route: app.route,
    miniAppId: app.id,
    message: "Rendered mini app placeholder.",
  });

  return (
    <section aria-label={`${app.title} placeholder`} className="dk-stack">
      <div className="dk-card">
        <p className="dk-eyebrow">Reserved Route</p>
        <h2 className="dk-section-title">{app.title} Placeholder</h2>
        <p className="dk-paragraph">
          This route is reserved by the Devkit shell bootstrap. Business features
          for this mini app are intentionally not implemented yet.
        </p>

        <div className="dk-meta-grid">
          <div className="dk-meta-item">
            <p className="dk-meta-key">Status</p>
            <p className="dk-meta-value">{app.status}</p>
          </div>
          <div className="dk-meta-item">
            <p className="dk-meta-key">Integration Mode</p>
            <p className="dk-meta-value">{app.integrationMode}</p>
          </div>
          <div className="dk-meta-item">
            <p className="dk-meta-key">Contract Document</p>
            <p className="dk-meta-value">
              <code className="dk-mono">{app.docsPath}</code>
            </p>
          </div>
        </div>
      </div>
    </section>
  );
}
