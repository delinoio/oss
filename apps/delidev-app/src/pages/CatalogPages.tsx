import { useQuery } from "@connectrpc/connect-query";
import { CatalogService } from "@delinoio/delibase-connect";
import { Link, useParams } from "react-router-dom";

import { usePublicTransport } from "../api/ApiContext";
import { CatalogCard } from "../components/CatalogCard";
import { EmptyState, ErrorState, LoadingState } from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { formatUsdMicros } from "../utils/format";

export function CatalogPage() {
  useDocumentMetadata(
    "App catalog",
    "Browse DeliDev apps and transparent usage-based pricing.",
  );
  const transport = usePublicTransport();
  const catalog = useQuery(
    CatalogService.method.listCatalogApps,
    { page: { pageSize: 50 } },
    {
      gcTime: 15 * 60 * 1000,
      networkMode: "always",
      retry: 1,
      staleTime: 5 * 60 * 1000,
      transport,
    },
  );

  return (
    <div className="page">
      <header className="page-heading">
        <span className="eyebrow">Public catalog</span>
        <h1>Developer tools that stay out of your way</h1>
        <p>Browse every app and its unit price. No account is required.</p>
      </header>
      {catalog.isPending ? <LoadingState label="Loading apps" /> : null}
      {catalog.isError ? (
        <ErrorState
          error={catalog.error}
          onRetry={() => void catalog.refetch()}
          title="The catalog isn’t available"
        />
      ) : null}
      {catalog.data && catalog.data.apps.length === 0 ? (
        <EmptyState
          description="There are no published apps yet. Check back soon."
          title="The catalog is empty"
        />
      ) : null}
      {catalog.data?.apps.length ? (
        <div className="catalog-grid">
          {catalog.data.apps.map((app) => (
            <CatalogCard app={app} key={app.slug} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function CatalogDetailPage() {
  const { appSlug = "" } = useParams();
  const transport = usePublicTransport();
  const app = useQuery(
    CatalogService.method.getCatalogApp,
    { appSlug },
    {
      gcTime: 15 * 60 * 1000,
      networkMode: "always",
      retry: false,
      staleTime: 5 * 60 * 1000,
      transport,
    },
  );
  useDocumentMetadata(
    app.data?.app?.name ?? "App details",
    app.data?.app?.summary ?? "DeliDev app details and pricing.",
  );

  return (
    <div className="page">
      <Link className="back-link" to="/apps">
        <span aria-hidden="true">←</span> All apps
      </Link>
      {app.isPending ? <LoadingState label="Loading app details" /> : null}
      {app.isError ? (
        <ErrorState
          error={app.error}
          onRetry={() => void app.refetch()}
          title="We couldn’t find that app"
        />
      ) : null}
      {app.data?.app ? (
        <>
          <header className="detail-hero">
            <div className="detail-icon" aria-hidden="true">
              {app.data.app.iconUrl ? (
                <img alt="" src={app.data.app.iconUrl} />
              ) : (
                app.data.app.name.slice(0, 1)
              )}
            </div>
            <div>
              <span className="eyebrow">DeliDev app</span>
              <h1>{app.data.app.name}</h1>
              <p>{app.data.app.summary}</p>
            </div>
          </header>
          <div className="detail-layout">
            <section className="content-card">
              <h2>About this app</h2>
              <p>{app.data.app.description}</p>
            </section>
            <section className="content-card" aria-labelledby="pricing-title">
              <div className="card-heading">
                <div>
                  <span className="eyebrow">Transparent pricing</span>
                  <h2 id="pricing-title">Meters</h2>
                </div>
              </div>
              {app.data.meters.length ? (
                <div className="meter-list">
                  {app.data.meters.map((meter) => (
                    <article className="meter-row" key={meter.key}>
                      <div>
                        <h3>{meter.name}</h3>
                        <p>{meter.description}</p>
                      </div>
                      <p className="meter-price">
                        <strong>
                          {formatUsdMicros(
                            meter.currentPrice?.usdMicrosPerUnit?.value,
                          )}
                        </strong>
                        <span>per {meter.unitName}</span>
                      </p>
                    </article>
                  ))}
                </div>
              ) : (
                <p className="muted">No billable meters are published.</p>
              )}
            </section>
          </div>
        </>
      ) : null}
    </div>
  );
}
