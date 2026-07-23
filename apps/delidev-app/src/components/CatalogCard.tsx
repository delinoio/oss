import type { CatalogApp } from "@delinoio/delibase-connect";
import { Link } from "react-router-dom";

export function CatalogCard({ app }: { app: CatalogApp }) {
  return (
    <article className="catalog-card">
      <div className="catalog-icon" aria-hidden="true">
        {app.iconUrl ? <img alt="" src={app.iconUrl} /> : app.name.slice(0, 1)}
      </div>
      <div>
        <h3>
          <Link to={`/apps/${encodeURIComponent(app.slug)}`}>{app.name}</Link>
        </h3>
        <p>{app.summary}</p>
      </div>
      <span className="card-arrow" aria-hidden="true">
        →
      </span>
    </article>
  );
}
