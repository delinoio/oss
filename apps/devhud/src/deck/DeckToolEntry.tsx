import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RefreshClientKind } from "@delinoio/devhud-deck-connect";
import { createContext, use, useEffect, useMemo, useState, type ReactNode } from "react";

import { AuthFeature, useSession } from "../auth/SessionProvider";
import { detectApplicationPlatform } from "../runtime/platform";
import { DeckProvider } from "./DeckProvider";
import { DeckWorkspace } from "./DeckWorkspace";
import {
  unavailableDeckGateway,
  type DeckGateway,
} from "./contracts";
import { NativeDeckGateway } from "./nativeGateway";

const DeckGatewayContext = createContext<DeckGateway>(unavailableDeckGateway);

export function DeckGatewayProvider({
  children,
  gateway = unavailableDeckGateway,
}: {
  readonly children: ReactNode;
  readonly gateway?: DeckGateway;
}) {
  return <DeckGatewayContext value={gateway}>{children}</DeckGatewayContext>;
}

function createDeckQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        gcTime: 0,
        retry: false,
        staleTime: 0,
        refetchOnWindowFocus: false,
      },
      mutations: { retry: false },
    },
  });
}

function DeckAuthenticatedSurface({ gateway }: { readonly gateway: DeckGateway }) {
  const [queryClient] = useState(createDeckQueryClient);
  useEffect(() => () => queryClient.clear(), [queryClient]);
  return (
    <QueryClientProvider client={queryClient}>
      <DeckProvider gateway={gateway}>
        <DeckWorkspace />
      </DeckProvider>
    </QueryClientProvider>
  );
}

export function DeckToolEntry() {
  const gateway = use(DeckGatewayContext);
  const { failure, logout, ready, session, signIn } = useSession();
  const [authRequested, setAuthRequested] = useState(false);
  const authenticated = session.status === "signed-in";
  const effectiveGateway = useMemo(
    () => authenticated && gateway === unavailableDeckGateway
      ? new NativeDeckGateway(
          session.subject,
          detectApplicationPlatform(navigator.userAgent) === "mobile"
            ? RefreshClientKind.MOBILE
            : RefreshClientKind.DESKTOP,
        )
      : gateway,
    [authenticated, gateway, session],
  );
  if (authenticated) {
    return (
      <section aria-label="Deck authenticated tool" className="deck-tool-surface">
        <div className="deck-account-actions">
          <button className="text-button" onClick={() => void logout()} type="button">Log out</button>
        </div>
        <DeckAuthenticatedSurface gateway={effectiveGateway} />
      </section>
    );
  }
  return (
    <article aria-labelledby="deck-tool-title" className="tool-card">
      <div>
        <p className="eyebrow">Desktop · iOS · Android · Internal</p>
        <h3 id="deck-tool-title">Deck</h3>
        <p>Monitor and act on permission-filtered GitHub pull requests.</p>
      </div>
      {!ready ? <p role="status">Checking the local account binding…</p> : null}
      {session.status === "prior-session-offline" ? (
        <p className="error" role="alert">
          Connect and sign in again. Deck never displays a cached regular pull request list offline.
        </p>
      ) : null}
      {ready ? (
        <button
          className="primary-button"
          disabled={session.status === "authenticating" || session.status === "cleanup-required"}
          onClick={() => {
            setAuthRequested(true);
            void signIn(AuthFeature.Deck);
          }}
          type="button"
        >
          {session.status === "authenticating" ? "Signing in…" : "Sign in to Deck"}
        </button>
      ) : null}
      {failure && authRequested ? <p className="error" role="alert">{failure.guidance}</p> : null}
    </article>
  );
}
