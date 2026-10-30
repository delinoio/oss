// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { pageHref, operatorPaths, operatorSection } from "../routes";
import { useState } from "react";
import { navigate, useBrowserLocation } from "../navigation";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountService,
  OrganizationService,
  type GetSessionResponse,
} from "@tairouter/client";
import { DirtyContext, useDirtyForm, type PageProps } from "../api";
import { PageHeader } from "../ui";
import {
  AdminAuthority,
  ErrorNotice,
  Loading,
  isAdmin,
  permissionMessage,
} from "./common";
import { Invitations, Members, Settings } from "./organization";
import { Audit, OrganizationUsage } from "./reporting";
import { Health, InstanceConfiguration, Prices } from "./operator";
import "./admin.css";

export function AdminPage(props: PageProps & { section: string }) {
  useLocale();
  return (
    <OrganizationBoundary
      key={`${props.session.userId}:${props.organization.id}`}
      {...props}
    />
  );
}
function OrganizationBoundary({
  organization,
  section,
}: PageProps & { section: string }) {
  useLocale();
  const [denied, setDenied] = useState(false);
  const query = useQuery(
    OrganizationService.method.getOrganization,
    { organizationId: organization.id },
    {
      staleTime: 0,
      refetchOnMount: "always",
      refetchInterval: 15000,
      retry: false,
    },
  );
  const current = query.data?.organization;
  if (query.isPending || !query.isFetchedAfterMount) return <Loading />;
  if (query.error)
    return (
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
    );
  if (denied || !current || !isAdmin(current.role))
    return (
      <div className="admin-workspace">
        <p role="alert">{permissionMessage()}</p>
        <a href={pageHref("overview", organization.id)}>
          {translate("backToYourWorkspace")}
        </a>
      </div>
    );
  return (
    <AdminAuthority.Provider
      value={{
        organizationId: current.id,
        role: current.role,
        organizationName: current.name,
        onDenied: () => setDenied(true),
      }}
    >
      <div className="admin-workspace" key={`${section}:${current.role}`}>
        <p className="admin-scope">
          {translate("organization_dbeb95", { value0: current.name })}
        </p>
        {section === "members" ? (
          <Members />
        ) : section === "invitations" ? (
          <Invitations />
        ) : section === "organization-usage" ? (
          <OrganizationUsage />
        ) : section === "audit" ? (
          <Audit />
        ) : section === "settings" ? (
          <Settings organization={current} />
        ) : (
          <PageHeader title={translate("organizationPageUnavailable")} />
        )}
      </div>
    </AdminAuthority.Provider>
  );
}
const operatorSections = () =>
  [
    ["health", translate("health")],
    ["limits", translate("requestLimits")],
    ["retention", translate("retention")],
    ["destinations", translate("destinations")],
    ["prices", translate("priceOverrides")],
    ["audit", translate("audit")],
  ] as const;
export function OperatorPage({ session }: { session: GetSessionResponse }) {
  useLocale();
  return (
    <div className="admin-operator-frame">
      <OperatorBoundary key={session.userId} />
    </div>
  );
}
function OperatorBoundary() {
  useLocale();
  const href = useBrowserLocation();
  const section = operatorSection(new URL(href).pathname);
  const [denied, setDenied] = useState(false);
  const reportDirty = useDirtyForm();
  const session = useQuery(
    AccountService.method.getSession,
    {},
    {
      staleTime: 0,
      refetchOnMount: "always",
      refetchInterval: 15000,
      retry: false,
    },
  );
  if (session.isPending || !session.isFetchedAfterMount) return <Loading />;
  if (session.error)
    return (
      <ErrorNotice error={session.error} retry={() => void session.refetch()} />
    );
  if (denied || !session.data?.isInstanceOperator)
    return (
      <div className="admin-workspace">
        <p role="alert">{permissionMessage()}</p>
        <a href="/overview">{translate("backToOrganizations")}</a>
      </div>
    );
  return (
    <AdminAuthority.Provider
      value={{ organizationId: "", role: 0, onDenied: () => setDenied(true) }}
    >
      <div className="admin-workspace">
        <header className="admin-instance-heading">
          <strong>{translate("instanceConsole")}</strong>
          <a href="/overview">{translate("backToOrganizations")}</a>
        </header>
        <p className="admin-banner">
          {translate("wholeInstanceHealthAndAggregatesOnly")}
        </p>
        <nav className="admin-tabs" aria-label={translate("instanceConsole")}>
          {operatorSections().map(([key, title]) => (
            <button
              key={key}
              aria-current={section === key ? "page" : undefined}
              onClick={() => navigate(operatorPaths[key])}
            >
              {title}
            </button>
          ))}
        </nav>
        <DirtyContext.Provider value={reportDirty}>
          <div key={section}>
            {section === "health" ? (
              <Health />
            ) : section === "audit" ? (
              <Audit operator />
            ) : section === "prices" ? (
              <Prices />
            ) : (
              <InstanceConfiguration section={section} />
            )}
          </div>
        </DirtyContext.Provider>
      </div>
    </AdminAuthority.Provider>
  );
}
