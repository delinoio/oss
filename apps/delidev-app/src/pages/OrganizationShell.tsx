import { useQuery } from "@connectrpc/connect-query";
import {
  OrganizationRole,
  OrganizationService,
  type Organization,
} from "@delinoio/delibase-connect";
import { createContext, use, type ReactNode } from "react";
import {
  Navigate,
  NavLink,
  useLocation,
  useNavigate,
  useParams,
} from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { useAccountState } from "../components/ProtectedRoute";
import { ErrorState, LoadingState } from "../components/States";
import { formatEnumLabel } from "../utils/format";

interface OrganizationContextValue {
  callerRole: OrganizationRole;
  organization: Organization;
  realqaTrackerClient?: ReturnType<
    typeof useAuthSession
  >["realqaTrackerClient"];
  refreshOrganization: () => Promise<void>;
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
  const { realqaTrackerClient, transport } = useAuthSession();
  const { accountState } = useAccountState();
  const location = useLocation();
  const navigate = useNavigate();
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
  const details = useQuery(
    OrganizationService.method.getOrganization,
    { organizationId: resolved.data?.organization?.organizationId },
    {
      enabled: Boolean(resolved.data?.organization?.organizationId),
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport,
    },
  );

  if (resolved.isPending || (resolved.data?.organization && details.isPending)) {
    return (
      <div className="page">
        <LoadingState label="Loading organization" />
      </div>
    );
  }
  if (
    !resolved.data?.organization ||
    !details.data?.organization ||
    !transport
  ) {
    return (
      <div className="page">
        <ErrorState
          error={resolved.error ?? details.error}
          onRetry={() => {
            void resolved.refetch();
            void details.refetch();
          }}
          title="Organization unavailable"
        />
      </div>
    );
  }
  if (resolved.data.organization.slug !== orgSlug) {
    const suffix = location.pathname.slice(`/o/${orgSlug}`.length);
    return (
      <Navigate
        replace
        to={`/o/${resolved.data.organization.slug}${suffix}${location.search}${location.hash}`}
      />
    );
  }
  const canonicalSlug = details.data.organization.slug;

  return (
    <OrganizationContext
      value={{
        callerRole: details.data.callerRole,
        organization: details.data.organization,
        realqaTrackerClient,
        refreshOrganization: async () => {
          await Promise.all([
            resolved.refetch({ throwOnError: true }),
            details.refetch({ throwOnError: true }),
          ]);
        },
        transport,
      }}
    >
      <div className="organization-layout">
        <aside className="organization-sidebar">
          <div className="organization-name">
            <span aria-hidden="true">
              {details.data.organization.name.slice(0, 1)}
            </span>
            <div>
              <strong>{details.data.organization.name}</strong>
              <small>
                {formatEnumLabel(
                  OrganizationRole[details.data.callerRole] ??
                    details.data.callerRole,
                )}
              </small>
            </div>
          </div>
          <label className="organization-switcher">
            <span>Switch organization</span>
            <select
              onChange={(event) =>
                navigate(`/o/${event.target.value}/apps`)
              }
              value={canonicalSlug}
            >
              {accountState.organizations.map((item) => (
                <option key={item.organizationId?.value} value={item.slug}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          <nav aria-label="Organization navigation">
            {organizationNavigation.map(([label, path]) => (
              <NavLink
                key={path}
                to={`/o/${canonicalSlug}/${path}`}
              >
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
