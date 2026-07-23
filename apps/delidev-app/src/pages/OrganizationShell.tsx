import { useQuery } from "@connectrpc/connect-query";
import {
  OrganizationService,
  type Organization,
} from "@delinoio/delibase-connect";
import { createContext, use, type ReactNode } from "react";
import { Navigate, NavLink, useLocation, useParams } from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { ErrorState, LoadingState } from "../components/States";

interface OrganizationContextValue {
  organization: Organization;
  transport: NonNullable<ReturnType<typeof useAuthSession>["transport"]>;
}

const OrganizationContext = createContext<OrganizationContextValue | undefined>(
  undefined,
);

export function useOrganization(): OrganizationContextValue {
  const organization = use(OrganizationContext);
  if (!organization) {
    throw new Error("OrganizationShell is missing.");
  }
  return organization;
}

const organizationNavigation = [
  ["Apps", "apps"],
  ["Members", "members"],
  ["Teams", "teams"],
  ["Billing", "billing"],
  ["Usage", "usage"],
  ["Settings", "settings"],
] as const;

export function OrganizationShell({ children }: { children: ReactNode }) {
  const { orgSlug = "" } = useParams();
  const { transport } = useAuthSession();
  const location = useLocation();
  const resolved = useQuery(
    OrganizationService.method.resolveOrganizationSlug,
    { slug: orgSlug },
    {
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport,
    },
  );

  if (resolved.isPending) {
    return (
      <div className="page">
        <LoadingState label="Loading organization" />
      </div>
    );
  }
  if (resolved.isError || !resolved.data.organization || !transport) {
    return (
      <div className="page">
        <ErrorState
          error={resolved.error}
          onRetry={() => void resolved.refetch()}
          title="Organization unavailable"
        />
      </div>
    );
  }
  if (
    resolved.data.matchedAlias &&
    resolved.data.organization.slug !== orgSlug
  ) {
    const suffix = location.pathname.slice(`/o/${orgSlug}`.length);
    return (
      <Navigate
        replace
        to={`/o/${resolved.data.organization.slug}${suffix}${location.search}`}
      />
    );
  }

  return (
    <OrganizationContext
      value={{ organization: resolved.data.organization, transport }}
    >
      <div className="organization-layout">
        <aside className="organization-sidebar">
          <div className="organization-name">
            <span aria-hidden="true">
              {resolved.data.organization.name.slice(0, 1)}
            </span>
            <div>
              <strong>{resolved.data.organization.name}</strong>
              <small>Organization</small>
            </div>
          </div>
          <nav aria-label="Organization navigation">
            {organizationNavigation.map(([label, path]) => (
              <NavLink key={path} to={`/o/${orgSlug}/${path}`}>
                {label}
              </NavLink>
            ))}
          </nav>
        </aside>
        <div className="organization-content">{children}</div>
      </div>
    </OrganizationContext>
  );
}
