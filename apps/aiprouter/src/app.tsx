// SPDX-License-Identifier: Apache-2.0
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
} from "@aiprouter/client";
import { ApiProvider, DirtyContext, useApi, pageSize } from "./api";
import { pageHref, resolveRoute, type Page } from "./routes";
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
const personalLinks = [
  ["overview", "Overview"],
  ["providers", "Providers"],
  ["routing", "Routing"],
  ["keys", "API keys"],
  ["usage", "Usage"],
  ["connect", "Connect a client"],
] as const;
const adminLinks = [
  ["members", "Members"],
  ["invitations", "Invitations"],
  ["organization-usage", "Organization usage"],
  ["audit", "Audit"],
  ["settings", "Settings"],
] as const;
function roleLabel(role: Role) {
  return role === Role.OWNER
    ? "Owner"
    : role === Role.ADMINISTRATOR
      ? "Administrator"
      : role === Role.MEMBER
        ? "Member"
        : "Unknown role";
}
export function App() {
  const route = resolveRoute(new URL(window.location.href));
  useEffect(() => {
    if (route && route.url.href !== window.location.href)
      window.history.replaceState(null, "", route.url);
  }, [route?.url.href]);
  if (!route)
    return (
      <main className="standalone">
        <h1>Page not found</h1>
        <a href={pageHref("overview")}>Back to organizations</a>
      </main>
    );
  const page = route.page;
  return (
    <Suspense fallback={<Loading />}>
      {["device", "invitation", "ownership", "sign-in"].includes(page) ? (
        <main className="standalone">
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
        <AuthenticatedApp page={page} />
      )}
    </Suspense>
  );
}
function AuthenticatedApp({ page }: { page: Page }) {
  const api = useApi();
  const cache = useQueryClient();
  const transport = useTransport();
  const [scope, setScope] = useState(
    () =>
      new URLSearchParams(window.location.search).get("organization") ??
      new URLSearchParams(window.location.search).get("organization_id") ??
      new URLSearchParams(window.location.search).get("org") ??
      "",
  );
  const [switcher, setSwitcher] = useState(false);
  const [menu, setMenu] = useState(false);
  const [search, setSearch] = useState("");
  const [dirty, setDirty] = useState(false);
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
      setAuthorityMessage(
        "Your permissions changed. This action is no longer available.",
      );
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
    setScope(id);
    setSwitcher(false);
    setMenu(false);
    setDirty(false);
    setPendingScope(undefined);
    setAuthorityMessage("");
    setScopeBlocked(false);
    const url = new URL(window.location.href);
    url.searchParams.set("organization", id);
    url.searchParams.delete("org");
    url.searchParams.delete("organization_id");
    window.history.replaceState(null, "", url);
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
        <Button onClick={() => void session.refetch()}>Try again</Button>
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
          Skip to content
        </a>
        <main id="main-content">
          <DirtyContext value={setDirty}>
            <OperatorPage session={session.data} />
          </DirtyContext>
        </main>
      </>
    ) : (
      <main className="standalone">
        <Notice tone="danger">Instance operator access is required.</Notice>
        <a href="/">Back to organizations</a>
      </main>
    );
  const links = (organization: Organization | undefined) => (
    <nav aria-label="Main navigation">
      <p className="nav-label">Personal</p>
      {personalLinks.map(([id, label]) => (
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
            <p className="nav-label">Organization</p>
            {adminLinks.map(([id, label]) => (
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
          {session.data?.displayName || "Account"}
        </a>
        {session.data?.isInstanceOperator && (
          <a href={pageHref("instance")}>Instance console</a>
        )}
        <Button variant="ghost" onClick={() => void logout()}>
          Sign out
        </Button>
      </div>
    </nav>
  );
  const switchButton = (
    <Button
      className="org-switch"
      onClick={() => setSwitcher(true)}
      aria-label="Switch organization"
    >
      <span>
        {selected?.name ?? "Choose organization"}
        <small>{selected ? roleLabel(selected.role) : ""}</small>
      </span>
      <span aria-hidden="true">⌄</span>
    </Button>
  );
  return (
    <>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="mobile-header">
        <Button aria-label="Open navigation menu" onClick={() => setMenu(true)}>
          Menu
        </Button>
        <strong className="brand">AIPRouter</strong>
        {switchButton}
      </header>
      <aside className="sidebar">
        <a href={pageHref("overview", selected?.id)} className="brand">
          AIPRouter
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
            You are offline. Reconnect to confirm the current state before
            making changes.
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
                window.location.assign(pageHref("overview", o.id));
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
            <h1>Choose your organization</h1>
            <p>Your previous organization is no longer available.</p>
            {switchButton}
          </Card>
        ) : scopeBlocked ? (
          <Card>
            <h1>Refresh your permissions</h1>
            <p>Previously scoped data and editors have been cleared.</p>
            <a
              className="button primary"
              href={pageHref("overview", selected.id)}
            >
              Return to your workspace
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
        <Dialog title="Switch organization" onClose={() => setSwitcher(false)}>
          <Field label="Find an organization">
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
            ) && <p>No memberships match your search.</p>}
          </div>
          <a className="button" href={pageHref("create")}>
            Create organization
          </a>
          <p>Each organization has separate personal settings.</p>
        </Dialog>
      )}
      {menu && (
        <Dialog title="Navigation" onClose={() => setMenu(false)}>
          {links(selected)}
        </Dialog>
      )}
      {pendingScope && (
        <ConfirmDialog
          title="Discard unsaved changes?"
          confirmLabel="Discard and switch"
          onClose={() => setPendingScope(undefined)}
          onConfirm={() => changeScope(pendingScope)}
        >
          Your unsaved form changes will be discarded. Providers, pools,
          routing, and keys are not copied to the next organization.
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
        <Button onClick={() => void current.refetch()}>Try again</Button>
      </>
    );
  if (current.isPending) return <Loading />;
  const org = current.data?.organization;
  if (!org)
    return <Notice tone="danger">This organization is unavailable.</Notice>;
  const props = { organization: org, session };
  if (adminLinks.some(([id]) => id === page)) {
    if (org.role !== Role.OWNER && org.role !== Role.ADMINISTRATOR)
      return (
        <Notice tone="danger">
          Your permissions changed. This action is no longer available.
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
