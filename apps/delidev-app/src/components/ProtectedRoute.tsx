import { useQuery } from "@connectrpc/connect-query";
import { AccountService } from "@delinoio/delibase-connect";
import { useEffect, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { AuthStatus, useAuthSession } from "../auth/AuthSession";
import { useOnline } from "../hooks/useOnline";
import { ErrorState, LoadingState } from "./States";

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

  useEffect(() => {
    if (auth.status === AuthStatus.SignedOut) {
      sessionStorage.setItem(
        "delidev:return-to",
        `${location.pathname}${location.search}`,
      );
    }
  }, [auth.status, location.pathname, location.search]);

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
            onClick={() =>
              void auth.signIn(`${location.pathname}${location.search}`)
            }
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
  return children;
}
