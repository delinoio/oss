import { createClient } from "@connectrpc/connect";
import {
  ConnectionState,
  DeckIntegrationService,
  ErrorReason,
  GitHubAccountKind,
  OwnerScope,
  type GitHubConnection,
  type GitHubInstallation,
  type Owner,
  type Revision,
} from "@delinoio/devhud-deck-connect";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { useAuthSession } from "../auth/AuthSession";
import { runtimeConfig } from "../config";
import {
  getDeckError,
  isDeckNotFound,
} from "../deck/errors";
import { handoffDeckGitHubAuthorization } from "../deck/githubHandoff";
import { useOnline } from "../hooks/useOnline";
import { Dialog } from "./Dialog";
import { LoadingState, OfflineActionHint } from "./States";

export type DeckConnectionOwner =
  | {
      accountId: string;
      kind: "personal";
      returnPath: "/account";
    }
  | {
      kind: "organization";
      organizationId: string;
      organizationName: string;
      returnPath: `/o/${string}/settings`;
    };

enum LoadState {
  Idle = "idle",
  Loading = "loading",
  Ready = "ready",
  Error = "error",
}
interface DisconnectInput {
  connectionId: { value: string };
  expectedRevision: Revision;
}

function ownerMessage(scope: DeckConnectionOwner): Owner {
  return scope.kind === "personal"
    ? ({
        $typeName: "devhud.deck.v1.Owner",
        ownerId: {
          case: "accountId",
          value: {
            $typeName: "devhud.deck.v1.UuidV7",
            value: scope.accountId,
          },
        },
        scope: OwnerScope.PERSONAL,
      } satisfies Owner)
    : ({
        $typeName: "devhud.deck.v1.Owner",
        ownerId: {
          case: "organizationId",
          value: {
            $typeName: "devhud.deck.v1.UuidV7",
            value: scope.organizationId,
          },
        },
        scope: OwnerScope.ORGANIZATION,
      } satisfies Owner);
}

function ownerMatches(owner: Owner | undefined, scope: DeckConnectionOwner) {
  if (scope.kind === "personal") {
    return (
      owner?.scope === OwnerScope.PERSONAL &&
      owner.ownerId.case === "accountId" &&
      owner.ownerId.value.value === scope.accountId
    );
  }
  return (
    owner?.scope === OwnerScope.ORGANIZATION &&
    owner.ownerId.case === "organizationId" &&
    owner.ownerId.value.value === scope.organizationId
  );
}

function validateConnection(
  connection: GitHubConnection | undefined,
  scope: DeckConnectionOwner,
): GitHubConnection | undefined {
  if (!connection) return undefined;
  if (!ownerMatches(connection.owner, scope)) {
    throw new Error("Deck returned an invalid owner-scoped response.");
  }
  return connection;
}

function validateInstallations(
  installations: GitHubInstallation[],
  nextCursor: string | undefined,
  scope: DeckConnectionOwner,
): GitHubInstallation[] {
  if (
    installations.length > 1 ||
    nextCursor ||
    installations.some(
      (installation) =>
        !ownerMatches(installation.owner, scope) ||
        !installation.account ||
        installation.account.githubAccountId === 0n ||
        !/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/.test(
          installation.account.login,
        ) ||
        (installation.account.kind !== GitHubAccountKind.USER &&
          installation.account.kind !== GitHubAccountKind.ORGANIZATION),
    )
  ) {
    throw new Error("Deck returned an invalid owner-scoped response.");
  }
  return installations;
}

function stateLabel(state: ConnectionState): string {
  switch (state) {
    case ConnectionState.DISCONNECTED:
      return "Disconnected";
    case ConnectionState.PENDING:
      return "Setup pending";
    case ConnectionState.CONNECTED:
      return "Connected";
    case ConnectionState.REAUTHENTICATION_REQUIRED:
      return "Permission review required";
    default:
      return "Unavailable";
  }
}

function stateDescription(state: ConnectionState): string {
  switch (state) {
    case ConnectionState.PENDING:
      return "Finish GitHub.com authorization to bind one installation to this owner.";
    case ConnectionState.CONNECTED:
      return "Exactly one accessible GitHub.com installation is bound to this owner.";
    case ConnectionState.REAUTHENTICATION_REQUIRED:
      return "GitHub permissions changed or expired. Review them before Deck can use this connection.";
    case ConnectionState.DISCONNECTED:
      return "No GitHub.com installation is connected to this owner.";
    default:
      return "Deck did not return a recognized connection state.";
  }
}

function startLabel(state: ConnectionState): string {
  switch (state) {
    case ConnectionState.CONNECTED:
      return "Change GitHub installation";
    case ConnectionState.PENDING:
      return "Continue GitHub setup";
    case ConnectionState.REAUTHENTICATION_REQUIRED:
      return "Review GitHub permissions";
    default:
      return "Connect GitHub.com";
  }
}

export function DeckConnectionManager({
  ownerScope,
}: {
  ownerScope: DeckConnectionOwner;
}) {
  const { deckTransport } = useAuthSession();
  const online = useOnline();
  const client = useMemo(
    () =>
      deckTransport
        ? createClient(DeckIntegrationService, deckTransport)
        : undefined,
    [deckTransport],
  );
  const owner = useMemo(() => ownerMessage(ownerScope), [ownerScope]);
  const requestGeneration = useRef(0);
  const pendingDisconnect = useRef<DisconnectInput | undefined>(undefined);
  const [connection, setConnection] = useState<GitHubConnection>();
  const [installations, setInstallations] = useState<GitHubInstallation[]>([]);
  const [loadState, setLoadState] = useState(LoadState.Idle);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [starting, setStarting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [disconnectDialogOpen, setDisconnectDialogOpen] = useState(false);

  const load = useCallback(async () => {
    if (!client || !online) return;
    const generation = ++requestGeneration.current;
    setLoadState(LoadState.Loading);
    setError("");
    try {
      const [connectionResult, installationsResult] =
        await Promise.allSettled([
          client.getGitHubConnection({ owner }),
          client.listGitHubInstallations({
            owner,
            page: { cursor: "", pageSize: 2 },
          }),
        ]);
      if (generation !== requestGeneration.current) return;
      let nextConnection: GitHubConnection | undefined;
      if (connectionResult.status === "fulfilled") {
        nextConnection = validateConnection(
          connectionResult.value.connection,
          ownerScope,
        );
      } else if (!isDeckNotFound(connectionResult.reason)) {
        throw connectionResult.reason;
      }
      if (installationsResult.status === "rejected") {
        throw installationsResult.reason;
      }
      const nextInstallations = validateInstallations(
        installationsResult.value.installations,
        installationsResult.value.page?.nextCursor,
        ownerScope,
      );
      setConnection(nextConnection);
      setInstallations(nextInstallations);
      setLoadState(LoadState.Ready);
    } catch (loadError) {
      if (generation !== requestGeneration.current) return;
      setConnection(undefined);
      setInstallations([]);
      setError(getDeckError(loadError).message);
      setLoadState(LoadState.Error);
    }
  }, [client, online, owner, ownerScope]);

  useEffect(() => {
    void load();
    return () => {
      requestGeneration.current += 1;
    };
  }, [load]);

  const state = connection?.state ?? ConnectionState.DISCONNECTED;
  const installation = installations[0];
  const activeConnection =
    state !== ConnectionState.DISCONNECTED &&
    connection?.connectionId?.value &&
    connection.revision
      ? connection
      : undefined;
  const configured =
    runtimeConfig.deck.issues.length === 0 && Boolean(client);

  const startConnection = async () => {
    if (!client || !online || !configured || starting) return;
    setStarting(true);
    setError("");
    setNotice("");
    try {
      const response = await client.startGitHubConnection({ owner });
      if (
        !response.authorizationTarget ||
        (response.expiresAt &&
          Number(response.expiresAt.seconds) * 1000 <= Date.now())
      ) {
        throw new Error("Deck returned an expired authorization handoff.");
      }
      handoffDeckGitHubAuthorization(
        response.authorizationTarget,
        ownerScope.returnPath,
        {
          githubAppClientId: runtimeConfig.deck.githubAppClientId,
          githubAppSlug: runtimeConfig.deck.githubAppSlug,
          githubCallbackUri: runtimeConfig.deck.githubCallbackUri,
        },
      );
    } catch (startError) {
      setError(getDeckError(startError).message);
      setStarting(false);
    }
  };

  const closeDisconnectDialog = () => {
    if (disconnecting) return;
    pendingDisconnect.current = undefined;
    setError("");
    setDisconnectDialogOpen(false);
  };

  const disconnect = async () => {
    if (!client || !online || !activeConnection || disconnecting) return;
    pendingDisconnect.current ??= {
      connectionId: { value: activeConnection.connectionId!.value },
      expectedRevision: activeConnection.revision!,
    };
    setDisconnecting(true);
    setError("");
    setNotice("");
    try {
      const response = await client.disconnectGitHubConnection(
        pendingDisconnect.current,
      );
      const disconnected = validateConnection(
        response.connection,
        ownerScope,
      );
      if (
        disconnected?.state !== ConnectionState.DISCONNECTED
      ) {
        throw new Error("Deck returned an invalid disconnect response.");
      }
      pendingDisconnect.current = undefined;
      setConnection(disconnected);
      setInstallations([]);
      setDisconnectDialogOpen(false);
      setNotice(
        "Disconnected. Deck credentials and cached connection data are being cleaned up. To uninstall the GitHub App too, remove it separately in GitHub.com settings.",
      );
    } catch (disconnectError) {
      const deckError = getDeckError(disconnectError);
      if (deckError.reason === ErrorReason.STALE_REVISION) {
        pendingDisconnect.current = undefined;
        setDisconnectDialogOpen(false);
        setNotice(
          "The connection revision changed. Its current state was refreshed; review it before disconnecting again.",
        );
        await load();
      } else {
        setError(deckError.message);
      }
    } finally {
      setDisconnecting(false);
    }
  };

  const title =
    ownerScope.kind === "personal"
      ? "GitHub connection"
      : `${ownerScope.organizationName} GitHub connection`;

  return (
    <section
      aria-labelledby="deck-connection-title"
      className="content-card deck-connection"
    >
      <div className="deck-connection-heading">
        <div>
          <span className="eyebrow">Deck integration</span>
          <h2 id="deck-connection-title">{title}</h2>
        </div>
        {configured && loadState === LoadState.Ready ? (
          <span className={`badge deck-state-${state}`}>
            {stateLabel(state)}
          </span>
        ) : null}
      </div>
      <p>
        Deck uses a user-attributed GitHub.com authorization. Tokens and
        connection details stay network-only and are never available offline.
      </p>
      {!configured ? (
        <div className="outage-state" role="status">
          Deck connection management is not configured for this build.
        </div>
      ) : null}
      {configured && loadState === LoadState.Loading ? (
        <LoadingState label="Loading GitHub connection" />
      ) : null}
      {configured && loadState === LoadState.Error ? (
        <div className="outage-state" role="alert">
          <p>{error}</p>
          <button
            className="button secondary"
            disabled={!online}
            onClick={() => void load()}
            type="button"
          >
            Retry connection check
          </button>
        </div>
      ) : null}
      {configured && loadState === LoadState.Ready ? (
        <>
          <div className="deck-state-summary">
            <strong>{stateLabel(state)}</strong>
            <p>{stateDescription(state)}</p>
            {connection?.revision?.value ? (
              <small>
                Connection revision {connection.revision.value.toString()}
              </small>
            ) : null}
          </div>
          {installation?.account ? (
            <ul
              aria-label="Accessible GitHub installations"
              className="deck-installation-list"
            >
              <li>
                <span aria-hidden="true" className="avatar">
                  {installation.account.login.slice(0, 1).toUpperCase()}
                </span>
                <div>
                  <strong>@{installation.account.login}</strong>
                  <span>
                    {installation.account.kind === GitHubAccountKind.USER
                      ? "Personal GitHub account"
                      : "GitHub organization"}
                  </span>
                </div>
              </li>
            </ul>
          ) : state === ConnectionState.CONNECTED ||
            state === ConnectionState.REAUTHENTICATION_REQUIRED ? (
            <p className="outage-state" role="status">
              Installation identity is unavailable. Refresh before making a
              connection change.
            </p>
          ) : null}
          {notice ? (
            <p className="inline-success" role="status">
              {notice}
            </p>
          ) : null}
          {error ? (
            <p className="inline-error" role="alert">
              {error}
            </p>
          ) : null}
          <div className="deck-connection-actions">
            <button
              className="button primary"
              disabled={!online || starting || disconnecting}
              onClick={() => void startConnection()}
              type="button"
            >
              {starting ? "Opening GitHub.com…" : startLabel(state)}
            </button>
            {activeConnection ? (
              <button
                className="button danger"
                disabled={!online || starting || disconnecting}
                onClick={() => {
                  setError("");
                  setDisconnectDialogOpen(true);
                }}
                type="button"
              >
                Disconnect
              </button>
            ) : null}
          </div>
          {!online ? <OfflineActionHint /> : null}
        </>
      ) : null}
      {disconnectDialogOpen && activeConnection ? (
        <Dialog
          descriptionId="deck-disconnect-description"
          onClose={closeDisconnectDialog}
          titleId="deck-disconnect-title"
        >
          <h2 id="deck-disconnect-title">Disconnect GitHub from Deck?</h2>
          <p id="deck-disconnect-description">
            Deck will stop using this installation and clean up its credentials
            and cached connection data. This does not uninstall the GitHub App;
            remove it separately in GitHub.com settings if needed.
          </p>
          {error ? (
            <p className="inline-error" role="alert">
              {error}
            </p>
          ) : null}
          {!online ? <OfflineActionHint /> : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              data-dialog-autofocus
              disabled={disconnecting}
              onClick={closeDisconnectDialog}
              type="button"
            >
              Keep connection
            </button>
            <button
              className="button danger"
              disabled={!online || disconnecting}
              onClick={() => void disconnect()}
              type="button"
            >
              {disconnecting ? "Disconnecting…" : "Disconnect GitHub"}
            </button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}
