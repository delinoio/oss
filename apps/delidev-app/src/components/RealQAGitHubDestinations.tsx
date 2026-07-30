import {
  ErrorReason,
  OwnerScopeKind,
  type OwnerScope,
} from "@delinoio/devhud-realqa-connect/devhud-realqa/v1/common_pb";
import {
  GitHubConnectionState,
  IssueFormFieldKind,
  type GetGitHubConnectionResponse,
  type GitHubInstallation,
  type GetRepositoryIssueSchemaResponse,
  type ListGitHubInstallationsResponse,
  type ListRepositoriesResponse,
  type Repository,
} from "@delinoio/devhud-realqa-connect/devhud-realqa/v1/tracker_pb";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
  type UseInfiniteQueryResult,
  type UseQueryResult,
} from "@tanstack/react-query";
import {
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";

import {
  describeRealQAError,
  getRealQAError,
} from "../api/realqaErrors";
import type { RealQATrackerClient } from "../api/transports";
import {
  runtimeConfig,
  type RealQAConfig,
} from "../config";
import { useOnline } from "../hooks/useOnline";
import { createUuidV7, formatEnumLabel } from "../utils/format";
import { navigateToRealQAGitHubAuthorization } from "../utils/realqaGitHub";
import { Dialog } from "./Dialog";
import {
  EmptyState,
  LoadingState,
  OfflineActionHint,
} from "./States";

export type RealQAOwner =
  | {
      kind: "personal";
      accountId: string;
    }
  | {
      kind: "organization";
      canManage: boolean;
      organizationId: string;
      organizationName: string;
    };

type DisconnectGitHubConnectionInput = Parameters<
  RealQATrackerClient["disconnectGitHubConnection"]
>[0];
interface PageInput {
  cursor: string;
  pageSize: number;
}

interface PendingDisconnect {
  idempotency: NonNullable<
    DisconnectGitHubConnectionInput["idempotency"]
  >;
  revision: string;
}

type InstallationQuery = UseInfiniteQueryResult<
  InfiniteData<ListGitHubInstallationsResponse>,
  Error
>;
type RepositoryQuery = UseInfiniteQueryResult<
  InfiniteData<ListRepositoriesResponse>,
  Error
>;
type IssueSchemaQuery = UseQueryResult<
  GetRepositoryIssueSchemaResponse,
  Error
>;

const pendingDisconnects = new Map<string, PendingDisconnect>();

function ownerKey(owner: RealQAOwner): string {
  return owner.kind === "personal"
    ? `personal:${owner.accountId}`
    : `organization:${owner.organizationId}`;
}

function ownerScope(owner: RealQAOwner): OwnerScope {
  if (owner.kind === "personal") {
    return {
      $typeName: "devhud.realqa.v1.OwnerScope",
      kind: OwnerScopeKind.PERSONAL,
      owner: {
        case: "personalAccountId",
        value: {
          $typeName: "devhud.realqa.v1.UuidV7",
          value: owner.accountId,
        },
      },
    };
  }
  return {
    $typeName: "devhud.realqa.v1.OwnerScope",
    kind: OwnerScopeKind.ORGANIZATION,
    owner: {
      case: "organizationId",
      value: {
        $typeName: "devhud.realqa.v1.UuidV7",
        value: owner.organizationId,
      },
    },
  };
}

function connectionStateLabel(state: GitHubConnectionState): string {
  switch (state) {
    case GitHubConnectionState.CONNECTED:
      return "Connected";
    case GitHubConnectionState.PENDING:
      return "Authorization pending";
    case GitHubConnectionState.DISCONNECTED:
      return "Disconnected";
    default:
      return "Status unavailable";
  }
}

function definitionLabel(
  definition: { name: string; path: string } | undefined,
): string {
  return definition?.name || definition?.path || "Unnamed definition";
}

function RealQAUnavailable({
  config,
}: {
  config: RealQAConfig;
}) {
  return (
    <section className="content-card integration-card">
      <div>
        <span className="eyebrow">RealQA destination</span>
        <h2>GitHub.com</h2>
        <p>
          RealQA GitHub connection settings are unavailable because the
          browser-safe integration configuration is incomplete.
        </p>
      </div>
      {config.issues.length ? (
        <p className="inline-error" role="status">
          Ask an administrator to finish the RealQA GitHub.com configuration.
        </p>
      ) : (
        <p className="inline-error" role="status">
          Sign in again to load the RealQA connection.
        </p>
      )}
    </section>
  );
}

export function RealQAGitHubDestinations({
  authorizationNavigator,
  client,
  config = runtimeConfig.realqa,
  owner,
}: {
  authorizationNavigator?: (target: string) => void;
  client?: RealQATrackerClient;
  config?: RealQAConfig;
  owner: RealQAOwner;
}) {
  if (!client || config.issues.length > 0) {
    return <RealQAUnavailable config={config} />;
  }
  return (
    <RealQAGitHubDestinationsConnected
      authorizationNavigator={authorizationNavigator}
      client={client}
      config={config}
      owner={owner}
    />
  );
}

function RealQAGitHubDestinationsConnected({
  authorizationNavigator,
  client,
  config,
  owner,
}: {
  authorizationNavigator?: (target: string) => void;
  client: RealQATrackerClient;
  config: RealQAConfig;
  owner: RealQAOwner;
}) {
  const online = useOnline();
  const queryClient = useQueryClient();
  const scope = useMemo(() => ownerScope(owner), [owner]);
  const scopeKey = ownerKey(owner);
  const canManage =
    owner.kind === "personal" || owner.canManage;
  const [actionError, setActionError] = useState("");
  const [message, setMessage] = useState("");
  const [disconnectOpen, setDisconnectOpen] = useState(false);
  const [disconnectAttempted, setDisconnectAttempted] = useState(false);
  const [startPending, setStartPending] = useState(false);
  const [selectedInstallationId, setSelectedInstallationId] = useState("");
  const [repositoryQueryInput, setRepositoryQueryInput] = useState("");
  const [repositoryQuery, setRepositoryQuery] = useState("");
  const [selectedRepositoryId, setSelectedRepositoryId] = useState("");
  const messageRef = useRef<HTMLParagraphElement>(null);

  const connectionInput = { owner: scope };
  const connectionQueryKey = [
    "realqa-tracker",
    "connection",
    scopeKey,
  ] as const;
  const connection = useQuery({
    gcTime: 0,
    queryFn: () => client.getGitHubConnection(connectionInput),
    queryKey: connectionQueryKey,
    refetchOnWindowFocus: true,
    retry: false,
    staleTime: 0,
  });
  const connectionValue = connection.isError
    ? undefined
    : connection.data?.connection;
  const connectionIdentityKey = [
    connectionValue?.connectionId?.value ?? "",
    connectionValue?.revision?.value.toString() ?? "",
  ] as const;
  const connected =
    connectionValue?.state === GitHubConnectionState.CONNECTED;
  const memberNeedsInstallation =
    owner.kind === "organization" && !owner.canManage && !connected;

  const installations = useInfiniteQuery<
    ListGitHubInstallationsResponse,
    Error,
    InfiniteData<ListGitHubInstallationsResponse, PageInput>,
    readonly unknown[],
    PageInput
  >({
    enabled: connected && online,
    gcTime: 0,
    getNextPageParam: (lastPage) => {
      const cursor = lastPage.page?.nextCursor;
      return cursor ? { cursor, pageSize: 25 } : undefined;
    },
    initialPageParam: { cursor: "", pageSize: 25 },
    queryFn: ({ pageParam }) =>
      client.listGitHubInstallations({
        owner: scope,
        page: pageParam,
      }),
    queryKey: [
      "realqa-tracker",
      "installations",
      scopeKey,
      ...connectionIdentityKey,
    ],
    retry: false,
    staleTime: 0,
  });
  const installationRows = installations.isError
    ? []
    : installations.data?.pages.flatMap((page) => page.installations) ?? [];
  const selectedInstallation =
    installationRows.find(
      (item) => item.installationId?.value === selectedInstallationId,
    ) ?? installationRows[0];

  const repositories = useInfiniteQuery<
    ListRepositoriesResponse,
    Error,
    InfiniteData<ListRepositoriesResponse, PageInput>,
    readonly unknown[],
    PageInput
  >({
    enabled: Boolean(
      connected && online && selectedInstallation?.installationId,
    ),
    gcTime: 0,
    getNextPageParam: (lastPage) => {
      const cursor = lastPage.page?.nextCursor;
      return cursor ? { cursor, pageSize: 50 } : undefined;
    },
    initialPageParam: { cursor: "", pageSize: 50 },
    queryFn: ({ pageParam }) =>
      client.listRepositories({
        installationId: selectedInstallation?.installationId,
        page: pageParam,
        query: repositoryQuery,
      }),
    queryKey: [
      "realqa-tracker",
      "repositories",
      scopeKey,
      ...connectionIdentityKey,
      selectedInstallation?.installationId?.value ?? "",
      repositoryQuery,
    ],
    retry: false,
    staleTime: 0,
  });
  const repositoryRows = repositories.isError
    ? []
    : repositories.data?.pages.flatMap((page) => page.repositories) ?? [];
  const selectedRepository = repositoryRows.find(
    (item) => item.repository?.repositoryId === selectedRepositoryId,
  );
  const issueSchema = useQuery({
    enabled: Boolean(
      online &&
        selectedInstallation?.installationId &&
        selectedRepository?.repository &&
        selectedRepository.issuesEnabled &&
        selectedRepository.callerCanSubmit,
    ),
    gcTime: 0,
    queryFn: () =>
      client.getRepositoryIssueSchema({
        installationId: selectedInstallation?.installationId,
        repository: selectedRepository?.repository,
      }),
    queryKey: [
      "realqa-tracker",
      "issue-schema",
      scopeKey,
      ...connectionIdentityKey,
      selectedInstallation?.installationId?.value ?? "",
      selectedRepository?.repository?.repositoryId ?? "",
    ],
    retry: false,
    staleTime: 0,
  });

  const disconnect = useMutation({
    mutationFn: (input: DisconnectGitHubConnectionInput) =>
      client.disconnectGitHubConnection(input),
  });

  const start = async () => {
    setActionError("");
    setMessage("");
    setStartPending(true);
    try {
      const response = await client.startGitHubConnection({
        owner: scope,
      });
      navigateToRealQAGitHubAuthorization(
        response.authorizationTarget,
        config,
        authorizationNavigator,
      );
    } catch (error) {
      setActionError(
        error instanceof Error &&
          error.message.startsWith("RealQA rejected")
          ? error.message
          : describeRealQAError(error),
      );
      void connection.refetch();
    } finally {
      setStartPending(false);
    }
  };

  const closeDisconnect = () => {
    if (disconnect.isPending) return;
    if (!disconnectAttempted) {
      pendingDisconnects.delete(scopeKey);
    }
    setDisconnectOpen(false);
    setDisconnectAttempted(false);
    setActionError("");
    disconnect.reset();
  };

  const confirmDisconnect = () => {
    if (!connectionValue?.revision) return;
    setActionError("");
    setMessage("");
    setDisconnectAttempted(true);
    const revision = connectionValue.revision.value.toString();
    const existing = pendingDisconnects.get(scopeKey);
    const pending =
      existing?.revision === revision
        ? existing
        : {
            idempotency: { value: { value: createUuidV7() } },
            revision,
          };
    pendingDisconnects.set(scopeKey, pending);
    disconnect.mutate(
      {
        expectedRevision: connectionValue.revision,
        idempotency: pending.idempotency,
        owner: scope,
      },
      {
        onError: async (error) => {
          const detail = getRealQAError(error);
          setActionError(detail.message);
          if (detail.reason === ErrorReason.STALE_REVISION) {
            pendingDisconnects.delete(scopeKey);
            setDisconnectAttempted(false);
            await connection.refetch().catch(() => undefined);
          }
        },
        onSuccess: async (response) => {
          pendingDisconnects.delete(scopeKey);
          setSelectedInstallationId("");
          setSelectedRepositoryId("");
          setDisconnectOpen(false);
          setDisconnectAttempted(false);
          setMessage(
            "GitHub disconnected. Existing RealQA presets and destination mappings were preserved.",
          );
          queryClient.setQueryData<GetGitHubConnectionResponse>(
            connectionQueryKey,
            (current) =>
              current
                ? { ...current, connection: response.connection }
                : current,
          );
          await connection.refetch().catch(() => undefined);
          disconnect.reset();
          requestAnimationFrame(() => messageRef.current?.focus());
        },
      },
    );
  };

  const submitRepositorySearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSelectedRepositoryId("");
    setRepositoryQuery(repositoryQueryInput.trim());
  };

  return (
    <section className="content-card integration-card">
      <div className="integration-heading">
        <div>
          <span className="eyebrow">RealQA destination</span>
          <h2>GitHub.com</h2>
          <p>
            Connect a GitHub App installation, review repositories available to
            your GitHub identity, and discover issue templates and forms.
          </p>
        </div>
        {connectionValue ? (
          <span
            className={`badge connection-state connection-state-${connectionValue.state}`}
          >
            {connectionStateLabel(connectionValue.state)}
          </span>
        ) : null}
      </div>

      {connection.isPending ? (
        <LoadingState label="Loading RealQA GitHub connection" />
      ) : null}
      {connection.isError ? (
        <div className="inline-state">
          <p className="inline-error" role="alert">
            {describeRealQAError(connection.error)}
          </p>
          <button
            className="button secondary compact"
            disabled={!online}
            onClick={() => void connection.refetch()}
            type="button"
          >
            Retry connection status
          </button>
        </div>
      ) : null}

      {connectionValue ? (
        <>
          <dl className="integration-facts">
            <div>
              <dt>Status</dt>
              <dd>{connectionStateLabel(connectionValue.state)}</dd>
            </div>
            <div>
              <dt>GitHub identity</dt>
              <dd>{connectionValue.githubLogin || "Not authorized"}</dd>
            </div>
            <div>
              <dt>Connection revision</dt>
              <dd>
                {connectionValue.revision
                  ? connectionValue.revision.value.toString()
                  : "Not assigned"}
              </dd>
            </div>
          </dl>
          {owner.kind === "organization" && !owner.canManage ? (
            <p className="field-hint">
              Owners and Admins bind or disconnect the organization
              installation. Your authorization only reveals repositories your
              own GitHub identity can access.
              {memberNeedsInstallation
                ? " Ask an Owner or Admin to connect the installation before authorizing your GitHub identity."
                : ""}
            </p>
          ) : null}
          <div className="button-row">
            <button
              className="button primary"
              disabled={!online || startPending || memberNeedsInstallation}
              onClick={() => void start()}
              type="button"
            >
              {startPending
                ? "Preparing GitHub…"
                : owner.kind === "organization" && !owner.canManage
                  ? "Authorize GitHub access"
                  : connected
                    ? "Reconnect GitHub"
                    : "Connect GitHub"}
            </button>
            {canManage && connected && connectionValue.revision ? (
              <button
                className="button danger"
                disabled={!online}
                onClick={() => {
                  setActionError("");
                  setDisconnectOpen(true);
                }}
                type="button"
              >
                Disconnect GitHub
              </button>
            ) : null}
            <button
              className="button secondary"
              disabled={!online || connection.isFetching}
              onClick={() => void connection.refetch()}
              type="button"
            >
              Refresh status
            </button>
          </div>
        </>
      ) : null}
      {!online ? <OfflineActionHint /> : null}
      {actionError && !disconnectOpen ? (
        <p className="inline-error" role="alert">
          {actionError}
        </p>
      ) : null}
      {message ? (
        <p
          className="inline-success"
          ref={messageRef}
          role="status"
          tabIndex={-1}
        >
          {message}
        </p>
      ) : null}

      {connected ? (
        <DestinationDiscovery
          installationRows={installationRows}
          installations={installations}
          issueSchema={issueSchema}
          online={online}
          onInstallationChange={(installationId) => {
            setSelectedInstallationId(installationId);
            setSelectedRepositoryId("");
          }}
          onLoadMoreInstallations={() =>
            void installations.fetchNextPage()
          }
          onLoadMoreRepositories={() => void repositories.fetchNextPage()}
          onRepositorySearch={submitRepositorySearch}
          onRepositorySearchInput={setRepositoryQueryInput}
          onSelectRepository={setSelectedRepositoryId}
          repositories={repositories}
          repositoryQueryInput={repositoryQueryInput}
          repositoryRows={repositoryRows}
          selectedInstallation={selectedInstallation}
          selectedRepository={selectedRepository}
          selectedRepositoryId={selectedRepositoryId}
        />
      ) : null}

      {disconnectOpen && connectionValue ? (
        <Dialog
          descriptionId="realqa-disconnect-description"
          onClose={closeDisconnect}
          titleId="realqa-disconnect-title"
        >
          <h2 id="realqa-disconnect-title">Disconnect GitHub from RealQA?</h2>
          <p id="realqa-disconnect-description">
            Provider tokens are removed immediately. Existing presets and
            repository mappings stay saved as disconnected records so they can
            be restored after reconnecting.
          </p>
          {actionError ? (
            <p className="inline-error" role="alert">
              {actionError}
            </p>
          ) : null}
          <div className="dialog-actions">
            <button
              className="button secondary"
              data-dialog-autofocus
              disabled={disconnect.isPending}
              onClick={closeDisconnect}
              type="button"
            >
              Keep connected
            </button>
            <button
              className="button danger"
              disabled={!online || disconnect.isPending}
              onClick={confirmDisconnect}
              type="button"
            >
              {disconnect.isPending
                ? "Disconnecting…"
                : "Disconnect GitHub"}
            </button>
          </div>
          {!online ? <OfflineActionHint /> : null}
        </Dialog>
      ) : null}
    </section>
  );
}

function DestinationDiscovery({
  installationRows,
  installations,
  issueSchema,
  online,
  onInstallationChange,
  onLoadMoreInstallations,
  onLoadMoreRepositories,
  onRepositorySearch,
  onRepositorySearchInput,
  onSelectRepository,
  repositories,
  repositoryQueryInput,
  repositoryRows,
  selectedInstallation,
  selectedRepository,
  selectedRepositoryId,
}: {
  installationRows: GitHubInstallation[];
  installations: InstallationQuery;
  issueSchema: IssueSchemaQuery;
  online: boolean;
  onInstallationChange: (installationId: string) => void;
  onLoadMoreInstallations: () => void;
  onLoadMoreRepositories: () => void;
  onRepositorySearch: (event: FormEvent<HTMLFormElement>) => void;
  onRepositorySearchInput: (value: string) => void;
  onSelectRepository: (repositoryId: string) => void;
  repositories: RepositoryQuery;
  repositoryQueryInput: string;
  repositoryRows: Repository[];
  selectedInstallation?: GitHubInstallation;
  selectedRepository?: Repository;
  selectedRepositoryId: string;
}) {
  return (
    <div className="destination-discovery">
      <h3>Available destination</h3>
      {installations.isPending ? (
        <LoadingState label="Loading GitHub installations" />
      ) : null}
      {installations.isError ? (
        <p className="inline-error" role="alert">
          {describeRealQAError(installations.error)}
        </p>
      ) : null}
      {installations.data &&
      !installations.isError &&
      installationRows.length === 0 ? (
        <EmptyState
          description="Reconnect GitHub to bind an installation to this owner."
          title="No installation is bound"
        />
      ) : null}
      {installationRows.length ? (
        <label>
          GitHub App installation
          <select
            onChange={(event) => onInstallationChange(event.target.value)}
            value={selectedInstallation?.installationId?.value ?? ""}
          >
            {installationRows.map((installation) => (
              <option
                key={installation.installationId?.value}
                value={installation.installationId?.value}
              >
                {installation.accountLogin} · revision{" "}
                {installation.revision?.value.toString() ?? "unavailable"}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {installations.hasNextPage && !installations.isError ? (
        <button
          className="button secondary compact"
          disabled={installations.isFetchingNextPage}
          onClick={onLoadMoreInstallations}
          type="button"
        >
          {installations.isFetchingNextPage
            ? "Loading installations…"
            : "Load more installations"}
        </button>
      ) : null}

      {selectedInstallation ? (
        <>
          <form
            className="repository-search"
            onSubmit={onRepositorySearch}
            role="search"
          >
            <label>
              Find an accessible repository
              <input
                maxLength={255}
                onChange={(event) =>
                  onRepositorySearchInput(event.target.value)
                }
                placeholder="owner or repository name"
                value={repositoryQueryInput}
              />
            </label>
            <button
              className="button secondary"
              disabled={!online || repositories.isFetching}
              type="submit"
            >
              Search repositories
            </button>
          </form>
          <p className="field-hint">
            Results are filtered by the signed-in user’s GitHub access. DeliDev
            does not infer repositories from the installation.
          </p>
          {repositories.isPending ? (
            <LoadingState label="Loading accessible repositories" />
          ) : null}
          {repositories.isError ? (
            <p className="inline-error" role="alert">
              {describeRealQAError(repositories.error)}
            </p>
          ) : null}
          {repositories.data &&
          !repositories.isError &&
          repositoryRows.length === 0 ? (
            <EmptyState
              description="Authorize GitHub access or search for another repository."
              title="No accessible repositories found"
            />
          ) : null}
          {repositoryRows.length ? (
            <ul className="repository-list">
              {repositoryRows.map((repository) => {
                const reference = repository.repository;
                if (!reference) return null;
                const selectable =
                  repository.issuesEnabled && repository.callerCanSubmit;
                return (
                  <li key={reference.repositoryId}>
                    <div>
                      <strong>
                        {reference.owner}/{reference.name}
                      </strong>
                      <span>
                        {!repository.issuesEnabled
                          ? "Issues disabled"
                          : repository.callerCanSubmit
                            ? "Issue submission permitted"
                            : "No issue submission permission"}
                      </span>
                    </div>
                    <button
                      aria-pressed={
                        selectedRepositoryId === reference.repositoryId
                      }
                      className="button secondary compact"
                      disabled={!selectable}
                      onClick={() =>
                        onSelectRepository(reference.repositoryId)
                      }
                      type="button"
                    >
                      {selectedRepositoryId === reference.repositoryId
                        ? "Selected"
                        : "Review definitions"}
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : null}
          {repositories.hasNextPage && !repositories.isError ? (
            <button
              className="button secondary compact"
              disabled={repositories.isFetchingNextPage}
              onClick={onLoadMoreRepositories}
              type="button"
            >
              {repositories.isFetchingNextPage
                ? "Loading repositories…"
                : "Load more repositories"}
            </button>
          ) : null}
        </>
      ) : null}

      {selectedRepository?.issuesEnabled &&
      selectedRepository.callerCanSubmit ? (
        <IssueDefinitionDiscovery
          issueSchema={issueSchema}
          repository={selectedRepository}
        />
      ) : null}
    </div>
  );
}

function IssueDefinitionDiscovery({
  issueSchema,
  repository,
}: {
  issueSchema: IssueSchemaQuery;
  repository: Repository;
}) {
  const schema = issueSchema.isError
    ? undefined
    : issueSchema.data?.schema;
  const definitions =
    (schema?.markdownTemplates.length ?? 0) +
    (schema?.issueForms.length ?? 0);
  return (
    <div className="issue-definition-discovery">
      <h3>
        Issue definitions for {repository.repository?.owner}/
        {repository.repository?.name}
      </h3>
      {issueSchema.isPending ? (
        <LoadingState label="Loading issue templates and forms" />
      ) : null}
      {issueSchema.isError ? (
        <p className="inline-error" role="alert">
          {describeRealQAError(issueSchema.error)}
        </p>
      ) : null}
      {schema ? (
        <>
          <p className="field-hint">
            Schema revision{" "}
            {schema.revision?.value.toString() ?? "unavailable"}
          </p>
          {definitions === 0 ? (
            <p className="muted">
              No repository templates or Issue Forms were discovered. GitHub’s
              blank issue definition remains available.
            </p>
          ) : null}
          {schema.markdownTemplates.length ? (
            <div>
              <h4>Markdown templates</h4>
              <ul className="definition-list">
                {schema.markdownTemplates.map((template) => (
                  <li key={template.definition?.definitionId}>
                    <strong>{definitionLabel(template.definition)}</strong>
                    <span>
                      {template.issueType
                        ? `Issue type: ${template.issueType}`
                        : "Default issue type"}
                    </span>
                    <span>
                      {template.definition?.path || "Path unavailable"}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {schema.issueForms.length ? (
            <div>
              <h4>Issue Forms</h4>
              <ul className="definition-list">
                {schema.issueForms.map((form) => (
                  <li key={form.definition?.definitionId}>
                    <strong>{definitionLabel(form.definition)}</strong>
                    <span>
                      {form.issueType
                        ? `Issue type: ${form.issueType}`
                        : "Default issue type"}
                    </span>
                    <ul className="form-field-list">
                      {form.fields.map((field, index) => (
                        <li key={`${field.fieldId}-${index}`}>
                          {field.label || "Informational content"} ·{" "}
                          {formatEnumLabel(
                            IssueFormFieldKind[field.kind] ?? field.kind,
                          )}
                          {field.required ? " · required" : ""}
                          {field.multiple ? " · multiple selections" : ""}
                          {field.defaultValue
                            ? ` · prefilled: ${field.defaultValue}`
                            : ""}
                          {field.renderLanguage
                            ? ` · code language: ${field.renderLanguage}`
                            : ""}
                        </li>
                      ))}
                    </ul>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
