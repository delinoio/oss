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
    <section aria-label={`${app.title} placeholder`}>
      <h2 style={{ marginTop: 0 }}>{app.title} Placeholder</h2>
      <p>
        This route is reserved by the Devkit shell bootstrap. Business features
        for this mini app are intentionally not implemented yet.
      </p>
      <p>
        Status: <strong>{app.status}</strong>
      </p>
      <p>
        Integration mode: <strong>{app.integrationMode}</strong>
      </p>
      <p>
        Contract document: <code>{app.docsPath}</code>
      </p>
    </section>
  );
}
