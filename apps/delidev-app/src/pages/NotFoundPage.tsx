import { Link } from "react-router-dom";

import { useDocumentMetadata } from "../hooks/useDocumentMetadata";

export function NotFoundPage() {
  useDocumentMetadata("Page not found", "The requested DeliDev page was not found.");
  return (
    <div className="page narrow">
      <section className="signed-out-card">
        <span className="eyebrow">404</span>
        <h1>That page isn’t here</h1>
        <p>The link may have changed, or the page may no longer exist.</p>
        <Link className="button primary" to="/">
          Go home
        </Link>
      </section>
    </div>
  );
}
