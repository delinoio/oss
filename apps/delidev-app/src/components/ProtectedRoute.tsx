import { useQuery } from "@connectrpc/connect-query";
import {
  AccountService,
  type GetAccountStateResponse,
} from "@delinoio/delibase-connect";
import {
  createContext,
  use,
  type ReactNode,
} from "react";
import { Navigate, useLocation } from "react-router-dom";

import {
  AuthStatus,
  safeReturnPath,
  useAuthSession,
} from "../auth/AuthSession";
import { useOnline } from "../hooks/useOnline";
import { ErrorState, LoadingState } from "./States";

interface AccountStateValue {
  accountState: GetAccountStateResponse;
  refreshAccountState: () => Promise<void>;
}

const AccountStateContext = createContext<AccountStateValue | undefined>(
  undefined,
);

export function AccountStateProvider({
  accountState,
  children,
  refreshAccountState,
}: AccountStateValue & { children: ReactNode }) {
  return (
    <AccountStateContext value={{ accountState, refreshAccountState }}>
      {children}
    </AccountStateContext>
  );
}

export function useAccountState(): AccountStateValue {
  const value = use(AccountStateContext);
  if (!value) {
    throw new Error("Account state is only available inside a protected route.");
  }
  return value;
}

export function ProtectedRoute({
  children,
  checkOnboarding = true,
}: {
  children: ReactNode;
  checkOnboarding?: boolean;
}) {
  const auth = useAuthSession();
  const location = useLocation();
  const online = useOnline();
  const returnTo = safeReturnPath(
    `${location.pathname}${location.search}`,
  );

  if (auth.status === AuthStatus.Loading) {
    return (
      <div className="page narrow">
        <LoadingState label="Checking your session" />
      </div>
    );
  }
  if (
    auth.status === AuthStatus.SignedOut ||
    auth.status === AuthStatus.Unavailable
  ) {
    return (
      <div className="page narrow">
        <section className="signed-out-card">
          <span className="eyebrow">Private area</span>
          <h1>Sign in to continue</h1>
          <p>
            Organization, billing, usage, invitation, and account pages require
            a secure DeliDev session.
          </p>
          {auth.error ? <p className="inline-error">{auth.error}</p> : null}
          <button
            className="button primary"
            disabled={!online || auth.status === AuthStatus.Unavailable}
            type="button"
            onClick={() => void auth.signIn(returnTo)}
          >
            Sign in with Logto
          </button>
        </section>
      </div>
    );
  }
  if (!auth.transport) {
    return (
      <div className="page narrow">
        <ErrorState title="Secure connection unavailable" />
      </div>
    );
  }

  return checkOnboarding ? (
    <OnboardingGate transport={auth.transport}>{children}</OnboardingGate>
  ) : (
    children
  );
}

function OnboardingGate({
  children,
  transport,
}: {
  children: ReactNode;
  transport: NonNullable<ReturnType<typeof useAuthSession>["transport"]>;
}) {
  const location = useLocation();
  const online = useOnline();
  const account = useQuery(
    AccountService.method.getAccountState,
    {},
    {
      enabled: online,
      gcTime: 0,
      retry: false,
      staleTime: 0,
      transport,
    },
  );

  if (!online) {
    return (
      <div className="page narrow">
        <ErrorState
          title="This page needs a connection"
          error={new Error(
            "Protected account data is never stored for offline use.",
          )}
        />
      </div>
    );
  }
  if (account.isPending) {
    return (
      <div className="page narrow">
        <LoadingState label="Loading your account" />
      </div>
    );
  }
  if (account.isError) {
    return (
      <div className="page narrow">
        <ErrorState
          error={account.error}
          onRetry={() => void account.refetch()}
          title="We couldn’t load your account"
        />
      </div>
    );
  }
  if (
    account.data.onboardingRequired &&
    location.pathname !== "/onboarding"
  ) {
    return <Navigate replace to="/onboarding" />;
  }
  if (
    !account.data.onboardingRequired &&
    location.pathname === "/onboarding"
  ) {
    return <Navigate replace to="/account" />;
  }
  return (
    <AccountStateProvider
      accountState={account.data}
      refreshAccountState={async () => {
        await account.refetch();
      }}
    >
      {children}
    </AccountStateProvider>
  );
}
