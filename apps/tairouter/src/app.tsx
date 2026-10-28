// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { useEffect, useRef, useState, lazy, Suspense } from "react";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  useQuery as useAsyncQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  AccountService,
  OrganizationService,
  Role,
  type Organization,
  type GetSessionResponse,
} from "@tairouter/client";
import { ApiProvider, DirtyContext, useApi, pageSize } from "./api";
import { pageHref, resolveRoute, type Page } from "./routes";
import {
  navigate,
  useBrowserLocation,
  useConsoleNavigation,
  useNavigationGuard,
} from "./navigation";
import {
  Button,
  Badge,
  Card,
  Dialog,
  Field,
  ErrorNotice,
  Loading,
  Notice,
  ConfirmDialog,
} from "./ui";
import { OverviewPage, ConnectPage } from "./overview";
const ProvidersPage = lazy(() =>
  import("./providers").then((m) => ({ default: m.ProvidersPage })),
);
const PoolsPage = lazy(() =>
  import("./routing").then((m) => ({ default: m.PoolsPage })),
);
const RoutingPage = lazy(() =>
  import("./routing").then((m) => ({ default: m.RoutingPage })),
);
const KeysPage = lazy(() =>
  import("./keys").then((m) => ({ default: m.KeysPage })),
);
const UsagePage = lazy(() =>
  import("./usage").then((m) => ({ default: m.UsagePage })),
);
const AdminPage = lazy(() =>
  import("./admin").then((m) => ({ default: m.AdminPage })),
);
const OperatorPage = lazy(() =>
  import("./admin").then((m) => ({ default: m.OperatorPage })),
);
const AccountPage = lazy(() =>
  import("./account").then((m) => ({ default: m.AccountPage })),
);
const DevicePage = lazy(() =>
  import("./account").then((m) => ({ default: m.DevicePage })),
);
const OnboardingPage = lazy(() =>
  import("./account").then((m) => ({ default: m.OnboardingPage })),
);
const SignInPage = lazy(() =>
  import("./account").then((m) => ({ default: m.SignInPage })),
);
const InvitationPage = lazy(() =>
  import("./account").then((m) => ({ default: m.InvitationPage })),
);
const OwnershipAcceptancePage = lazy(() =>
  import("./account").then((m) => ({ default: m.OwnershipAcceptancePage })),
);
const personalLinks = () =>
  [
    ["overview", translate("overview")],
    ["providers", translate("providers")],
    ["routing", translate("routing")],
    ["keys", translate("apiKeys")],
    ["usage", translate("usage")],
    ["connect", translate("connectAClient")],
  ] as const;
const adminLinks = () =>
  [
    ["members", translate("members")],
    ["invitations", translate("invitations")],
    ["organization-usage", translate("organizationUsage")],
    ["audit", translate("audit")],
    ["settings", translate("settings")],
  ] as const;
function roleLabel(role: Role) {
  return role === Role.OWNER
    ? translate("owner")
    : role === Role.ADMINISTRATOR
      ? translate("administrator")
      : role === Role.MEMBER
        ? translate("member")
        : translate("unknownRole");
}
/**
 * Static recovery surface for unresolved routes. It renders nothing derived
 * from the unknown URL and links only to the unscoped overview destination.
 */
function NotFound() {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  return (
    <main className="not-found" aria-labelledby="not-found-title">
      <p className="not-found-label">{translate("pageUnavailable")}</p>
      <h1 id="not-found-title" ref={heading} tabIndex={-1}>
        {translate("pageNotFound")}
      </h1>
      <div
        role="group"
        aria-labelledby="not-found-title"
        aria-describedby="not-found-description"
      >
        <p id="not-found-description" className="not-found-description">
          {translate("pageNotFoundExplanation")}
        </p>
        <a className="not-found-action" href={pageHref("overview")}>
          {translate("backToOrganizations")}
        </a>
      </div>
    </main>
  );
}
export function App() {
  useLocale();
  useConsoleNavigation();
  const href = useBrowserLocation();
  const route = resolveRoute(new URL(href));
  useEffect(() => {
    if (route && route.url.href !== window.location.href)
      navigate(route.url.href, { replace: true, confirmed: true });
  }, [route?.url.href]);
  if (!route) return <NotFound />;
  const page = route.page;
  return (
    <Suspense fallback={<Loading />}>
      {["device", "invitation", "ownership", "sign-in"].includes(page) ? (
        <main className="standalone" key={href}>
          {page === "device" ? (
            <DevicePage />
          ) : page === "invitation" ? (
            <InvitationPage />
          ) : page === "ownership" ? (
            <OwnershipAcceptancePage />
          ) : page === "sign-in" ? (
            <SignInPage
              error={
                new URLSearchParams(window.location.search).get("error") ??
                undefined
              }
            />
          ) : null}
        </main>
      ) : (
        <AuthenticatedApp page={page} href={route.url.href} />
      )}
    </Suspense>
  );
}
function AuthenticatedApp({ page, href }: { page: Page; href: string }) {
  useLocale();
  const api = useApi();
  const cache = useQueryClient();
  const transport = useTransport();
  const scope = resolveRoute(new URL(href))?.organization ?? "";
  const [switcher, setSwitcher] = useState(false);
  const [menu, setMenu] = useState(false);
  const [search, setSearch] = useState("");
  const [dirty, setDirty] = useState(false);
  useNavigationGuard(dirty);
  useEffect(() => {
    setMenu(false);
    setSwitcher(false);
    setDirty(false);
    setScopeBlocked(false);
    setAuthorityMessage("");
  }, [href]);
  const [pendingScope, setPendingScope] = useState<string>();
  const [authorityVersion, setAuthorityVersion] = useState(0);
  const [authorityMessage, setAuthorityMessage] = useState("");
  const [scopeBlocked, setScopeBlocked] = useState(false);
  const [signedOut, setSignedOut] = useState(false);
  const [logoutError, setLogoutError] = useState<unknown>();
  const session = useQuery(
    AccountService.method.getSession,
    {},
    { refetchInterval: 30_000, enabled: !signedOut },
  );
  const organizations = useAsyncQuery({
    queryKey: ["organizations", session.data?.userId, authorityVersion],
    enabled: !!session.data && !signedOut,
    queryFn: async ({ signal }) => {
      const all: Organization[] = [];
      let pageToken = "";
      do {
        const r = await api.organization.listOrganizations(
          { page: { pageSize, pageToken } },
          { signal },
        );
        all.push(...r.organizations);
        pageToken = r.page?.nextPageToken ?? "";
      } while (pageToken);
      return all;
    },
    refetchInterval: 30_000,
  });
  const selected =
    organizations.data?.find((o) => o.id === scope) ??
    (!scope ? organizations.data?.[0] : undefined);
  const [online, setOnline] = useState(navigator.onLine);
  useEffect(() => {
    const on = () => setOnline(true);
    const off = () => setOnline(false);
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
    };
  }, []);
  useEffect(() => {
    const handler = (event: Event) => {
      const code = (event as CustomEvent<Code>).detail;
      setSwitcher(false);
      setMenu(false);
      setDirty(false);
      setScopeBlocked(true);
      setAuthorityVersion((v) => v + 1);
      setAuthorityMessage(translate("yourPermissionsChangedThisActionIsNo"));
      void cache.invalidateQueries();
      if (code === Code.Unauthenticated) {
        setSignedOut(true);
        cache.clear();
      }
    };
    window.addEventListener("authority-changed", handler);
    return () => window.removeEventListener("authority-changed", handler);
  }, [cache]);
  useEffect(() => {
    const before = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", before);
    return () => window.removeEventListener("beforeunload", before);
  }, [dirty]);
  const changeScope = (id: string) => {
    setSwitcher(false);
    setMenu(false);
    setDirty(false);
    setPendingScope(undefined);
    setAuthorityMessage("");
    setScopeBlocked(false);
    const url = new URL(window.location.href);
    url.pathname = pageHref(page, id);
    url.searchParams.delete("organization");
    url.searchParams.delete("org");
    url.searchParams.delete("organization_id");
    navigate(url.href, { confirmed: true });
  };
  const selectScope = (id: string) => {
    if (dirty && id !== selected?.id) setPendingScope(id);
    else changeScope(id);
  };
  const logout = async () => {
    try {
      await api.account.logout({});
      setSignedOut(true);
      setDirty(false);
      cache.clear();
    } catch (e) {
      setLogoutError(e);
    }
  };
  if (
    signedOut ||
    (session.error &&
      ConnectError.from(session.error).code === Code.Unauthenticated)
  )
    return (
      <main className="standalone">
        <SignInPage />
      </main>
    );
  if (session.isPending) return <Loading />;
  if (session.error)
    return (
      <main className="standalone">
        <ErrorNotice error={session.error} />
        <Button onClick={() => void session.refetch()}>
          {translate("tryAgain")}
        </Button>
      </main>
    );
  if (!session.data) return null;
  const account = (
    <AccountPage
      section="account"
      session={session.data}
      onSessionChange={() => {
        setSignedOut(true);
        cache.clear();
      }}
    />
  );
  if (page === "instance")
    return session.data.isInstanceOperator ? (
      <>
        <a className="skip-link" href="#main-content">
          {translate("skipToContent")}
        </a>
        <main id="main-content">
          <DirtyContext value={setDirty}>
            <OperatorPage session={session.data} />
          </DirtyContext>
        </main>
      </>
    ) : (
      <main className="standalone">
        <Notice tone="danger">
          {translate("instanceOperatorAccessIsRequired")}
        </Notice>
        <a href="/overview">{translate("backToOrganizations")}</a>
      </main>
    );
  const links = (organization: Organization | undefined) => (
    <nav aria-label={translate("mainNavigation")}>
      <p className="nav-label">{translate("personal")}</p>
      {personalLinks().map(([id, label]) => (
        <a
          key={id}
          aria-current={page === id ? "page" : undefined}
          href={pageHref(id, organization?.id)}
        >
          {label}
        </a>
      ))}
      {organization &&
        !scopeBlocked &&
        (organization.role === Role.OWNER ||
          organization.role === Role.ADMINISTRATOR) && (
          <>
            <p className="nav-label">{translate("organization")}</p>
            {adminLinks().map(([id, label]) => (
              <a
                key={id}
                aria-current={page === id ? "page" : undefined}
                href={pageHref(id, organization.id)}
              >
                {label}
              </a>
            ))}
          </>
        )}
      <div className="account-nav">
        <a href={pageHref("account", organization?.id)}>
          {session.data?.displayName || translate("account")}
        </a>
        {session.data?.isInstanceOperator && (
          <a href={pageHref("instance")}>{translate("instanceConsole")}</a>
        )}
        <Button variant="ghost" onClick={() => void logout()}>
          {translate("signOut")}
        </Button>
      </div>
    </nav>
  );
  const switchButton = (
    <Button
      className="org-switch"
      onClick={() => setSwitcher(true)}
      aria-label={translate("switchOrganization")}
    >
      <span>
        {selected?.name ?? translate("chooseOrganization")}
        <small>{selected ? roleLabel(selected.role) : ""}</small>
      </span>
      <span aria-hidden="true">⌄</span>
    </Button>
  );
  return (
    <>
      <a className="skip-link" href="#main">
        {translate("skipToContent")}
      </a>
      <header className="mobile-header">
        <Button
          aria-label={translate("openNavigationMenu")}
          onClick={() => setMenu(true)}
        >
          {translate("menu")}
        </Button>
        <strong className="brand">{translate("tairouter")}</strong>
        {switchButton}
      </header>
      <aside className="sidebar">
        <a href={pageHref("overview", selected?.id)} className="brand">
          {translate("tairouter")}
        </a>
        {switchButton}
        {links(selected)}
      </aside>
      <main
        id="main"
        className="main"
        // Let controlled fields update before dirty tracking rerenders the shell.
        onChange={(e) => {
          if ((e.target as HTMLElement).closest("form")) setDirty(true);
        }}
      >
        <ErrorNotice error={logoutError} />
        {!online && (
          <Notice tone="warning">
            {translate("youAreOfflineReconnectToConfirmThe")}
          </Notice>
        )}
        {authorityMessage && <Notice tone="warning">{authorityMessage}</Notice>}
        <ErrorNotice error={organizations.error} />
        {page === "account" ? (
          account
        ) : page === "create" ? (
          <DirtyContext value={setDirty}>
            <OnboardingPage
              onOrganizationCreated={(o) => {
                void organizations.refetch();
                navigate(pageHref("overview", o.id));
              }}
            />
          </DirtyContext>
        ) : organizations.isPending ? (
          <Loading />
        ) : !organizations.data?.length ? (
          <DirtyContext value={setDirty}>
            <OnboardingPage
              onOrganizationCreated={(o) => {
                changeScope(o.id);
                void organizations.refetch();
              }}
            />
          </DirtyContext>
        ) : !selected ? (
          <Card>
            <h1>{translate("chooseYourOrganization")}</h1>
            <p>{translate("yourPreviousOrganizationIsNoLongerAvailable")}</p>
            {switchButton}
          </Card>
        ) : scopeBlocked ? (
          <Card>
            <h1>{translate("refreshYourPermissions")}</h1>
            <p>{translate("previouslyScopedDataAndEditorsHaveBeen")}</p>
            <a
              className="button primary"
              href={pageHref("overview", selected.id)}
            >
              {translate("returnToYourWorkspace")}
            </a>
          </Card>
        ) : (
          <DirtyContext value={setDirty}>
            <ApiProvider
              key={`${session.data.userId}:${selected.id}:${selected.role}:${authorityVersion}`}
              rpcTransport={transport}
            >
              <OrganizationContent
                organization={selected}
                session={session.data}
                page={page}
              />
            </ApiProvider>
          </DirtyContext>
        )}
      </main>
      {switcher && (
        <Dialog
          title={translate("switchOrganization")}
          onClose={() => setSwitcher(false)}
        >
          <Field label={translate("findAnOrganization")}>
            <input
              type="search"
              autoFocus
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </Field>
          <div className="org-options">
            {organizations.data
              ?.filter((o) =>
                o.name.toLowerCase().includes(search.toLowerCase()),
              )
              .map((o) => (
                <Button
                  key={o.id}
                  aria-label={`${o.name} ${roleLabel(o.role)}`}
                  aria-pressed={selected?.id === o.id}
                  onClick={() => selectScope(o.id)}
                >
                  <span>
                    {o.name}
                    <small>{roleLabel(o.role)}</small>
                  </span>
                  {selected?.id === o.id && <span aria-hidden="true">✓</span>}
                </Button>
              ))}
            {organizations.data?.every(
              (o) => !o.name.toLowerCase().includes(search.toLowerCase()),
            ) && <p>{translate("noMembershipsMatchYourSearch")}</p>}
          </div>
          <a className="button" href={pageHref("create")}>
            {translate("createOrganization")}
          </a>
          <p>{translate("eachOrganizationHasSeparatePersonalSettings")}</p>
        </Dialog>
      )}
      {menu && (
        <Dialog title={translate("navigation")} onClose={() => setMenu(false)}>
          {links(selected)}
        </Dialog>
      )}
      {pendingScope && (
        <ConfirmDialog
          title={translate("discardUnsavedChanges")}
          confirmLabel={translate("discardAndSwitch")}
          onClose={() => setPendingScope(undefined)}
          onConfirm={() => changeScope(pendingScope)}
        >
          {translate("yourUnsavedFormChangesWillBeDiscarded")}
        </ConfirmDialog>
      )}
    </>
  );
}
function OrganizationContent({
  organization,
  session,
  page,
}: {
  organization: Organization;
  session: GetSessionResponse;
  page: Page;
}) {
  useLocale();
  const cache = useQueryClient();
  const current = useQuery(
    OrganizationService.method.getOrganization,
    { organizationId: organization.id },
    { refetchInterval: 15_000 },
  );
  const previousRole = useRef(organization.role);
  useEffect(
    () =>
      cache.getQueryCache().subscribe((event) => {
        if (event.type !== "updated" || event.action.type !== "error") return;
        const code = ConnectError.from(event.query.state.error).code;
        if (code === Code.Unauthenticated || code === Code.PermissionDenied) {
          window.dispatchEvent(
            new CustomEvent("authority-changed", { detail: code }),
          );
        }
      }),
    [cache],
  );
  useEffect(() => {
    if (
      current.data?.organization &&
      current.data.organization.role !== previousRole.current
    ) {
      previousRole.current = current.data.organization.role;
      window.dispatchEvent(
        new CustomEvent("authority-changed", { detail: Code.PermissionDenied }),
      );
    }
  }, [current.data]);
  useEffect(
    () => () => {
      void cache.cancelQueries();
      cache.clear();
    },
    [cache],
  );
  if (current.error)
    return (
      <>
        <ErrorNotice error={current.error} />
        <Button onClick={() => void current.refetch()}>
          {translate("tryAgain")}
        </Button>
      </>
    );
  if (current.isPending) return <Loading />;
  const org = current.data?.organization;
  if (!org)
    return (
      <Notice tone="danger">
        {translate("thisOrganizationIsUnavailable")}
      </Notice>
    );
  const props = { organization: org, session };
  if (adminLinks().some(([id]) => id === page)) {
    if (org.role !== Role.OWNER && org.role !== Role.ADMINISTRATOR)
      return (
        <Notice tone="danger">
          {translate("yourPermissionsChangedThisActionIsNo")}
        </Notice>
      );
    return <AdminPage section={page} {...props} />;
  }
  switch (page) {
    case "providers":
      return <ProvidersPage {...props} />;
    case "pools":
      return <PoolsPage {...props} />;
    case "routing":
      return <RoutingPage {...props} />;
    case "keys":
      return <KeysPage {...props} />;
    case "usage":
      return <UsagePage {...props} />;
    case "connect":
      return <ConnectPage {...props} />;
    default:
      return <OverviewPage {...props} />;
  }
}
