import { useQuery } from "@connectrpc/connect-query";
import { CatalogService } from "@delinoio/delibase-connect";
import { Link } from "react-router-dom";

import { usePublicTransport } from "../api/ApiContext";
import { CatalogCard } from "../components/CatalogCard";
import { ErrorState } from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";

export function HomePage() {
  useDocumentMetadata(
    "Developer tools, simply delivered",
    "Browse focused developer tools with clear, usage-based pricing.",
  );
  const transport = usePublicTransport();
  const catalog = useQuery(
    CatalogService.method.listCatalogApps,
    { page: { pageSize: 3 } },
    {
      gcTime: 15 * 60 * 1000,
      networkMode: "always",
      retry: 1,
      staleTime: 5 * 60 * 1000,
      transport,
    },
  );

  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <span className="eyebrow">Tools for people who build</span>
          <h1>Small tools.<br />Less friction.</h1>
          <p>
            Focused developer utilities with transparent pricing, shared team
            access, and one simple balance.
          </p>
          <div className="hero-actions">
            <Link className="button primary large" to="/apps">
              Browse apps
            </Link>
            <a className="text-link" href="#how-it-works">
              See how it works <span aria-hidden="true">↓</span>
            </a>
          </div>
        </div>
        <div className="hero-visual" aria-hidden="true">
          <div className="visual-orbit orbit-one" />
          <div className="visual-orbit orbit-two" />
          <div className="visual-card card-one">
            <span>JSON</span>
            <strong>{`{ }`}</strong>
          </div>
          <div className="visual-card card-two">
            <span>Tokens</span>
            <strong>ABC</strong>
          </div>
          <div className="visual-card card-three">
            <span>Time</span>
            <strong>UTC</strong>
          </div>
          <div className="visual-center">D</div>
        </div>
      </section>

      <section className="section" aria-labelledby="featured-title">
        <div className="section-heading">
          <div>
            <span className="eyebrow">Catalog</span>
            <h2 id="featured-title">Useful from the first click</h2>
          </div>
          <Link className="text-link" to="/apps">
            View all apps <span aria-hidden="true">→</span>
          </Link>
        </div>
        {catalog.data?.apps.length ? (
          <div className="catalog-grid">
            {catalog.data.apps.map((app) => (
              <CatalogCard app={app} key={app.slug} />
            ))}
          </div>
        ) : catalog.isError ? (
          <ErrorState
            error={catalog.error}
            onRetry={() => void catalog.refetch()}
            title="Catalog unavailable"
          />
        ) : (
          <div className="catalog-placeholder" role="status">
            <p>
              {catalog.isPending
                ? "Loading the public catalog…"
                : "The first DeliDev apps are being prepared."}
            </p>
            <Link to="/apps">Open the catalog</Link>
          </div>
        )}
      </section>

      <section className="steps-section" id="how-it-works" aria-labelledby="steps-title">
        <span className="eyebrow">Simple by design</span>
        <h2 id="steps-title">Start building in three steps</h2>
        <ol className="steps">
          <li>
            <span>1</span>
            <div>
              <h3>Choose a tool</h3>
              <p>Explore metadata and exact unit pricing before you sign in.</p>
            </div>
          </li>
          <li>
            <span>2</span>
            <div>
              <h3>Bring your team</h3>
              <p>Organize access with nested teams and clear member roles.</p>
            </div>
          </li>
          <li>
            <span>3</span>
            <div>
              <h3>Pay for what you use</h3>
              <p>Monthly credits roll forward. You control any overage limit.</p>
            </div>
          </li>
        </ol>
      </section>
    </>
  );
}
