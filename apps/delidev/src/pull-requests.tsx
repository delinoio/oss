// SPDX-License-Identifier: Apache-2.0
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import {
  useProductLabels,
  PresentationKind,
  productResourceName,
} from "./product-identity-labels";
import { useSidebarPaneVisible } from "./sidebar-context";
import {
  RepositoryDetailsPresentation,
  useRepositoryDetailsPopup,
} from "./repository-details-popup";
import {
  DisclosureButton,
  DisclosureContent,
  DisclosureDensity,
} from "./disclosure";
import { ScrollContinuation } from "./scroll-continuation";
import {
  paginationError,
  useGitHubCatalog,
  useGitHubScrollRoot,
} from "./github-scroll";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useId, useRef, useState } from "react";
import {
  createQueryOptions,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { useQuery as useBusinessQuery } from "@tanstack/react-query";
import { type Transport } from "@connectrpc/connect";
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";
import {
  EntityKind,
  IntegrationQuery,
  PullRequestFixQuery,
  ResourceQuery,
  SystemCapability,
  SystemQuery,
  type Resource,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import {
  ItemKind,
  ItemState,
  QueryOperation,
  type GitHubQuery,
} from "./github-query-model";
import {
  StandalonePullRequestResults,
  type PullRequestNavigation,
} from "./github-items";
import { Problem } from "./ui";
import {
  NavigationIntentSource,
  useNavigationIntent,
} from "./navigation-intent";
import {
  SettingsEntryDestination,
  type SettingsNavigationEntry,
} from "./settings";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { useDestinationDispatch } from "./destination-window";
import { Surface } from "./surface";
import {
  useRetainedMutation,
  useRetainedMutationIntents,
  type RetainedMutationIntent,
  repositoryConfigurationPrefix,
  useRepositoryConfigurationPending,
} from "./mutation";
import { usePRWorkflow } from "./pr-workflow";
import { Icon } from "./sidebar";
import {
  SettingsActionButton,
  SettingsActionIcon,
  SettingsActionPresentation,
  SettingsActionScope,
} from "./settings-action";

import {
  PendingRepositoryConfiguration,
  RepositoryProfileAssociation,
} from "./repository-association";
import { SettingsCategory } from "./settings-category";
import { useConnectionEpoch } from "./connection-epoch";

/** Closed set of PR page sizes offered by the sidebar. Fifty needs the
 * negotiated System capability PULL_REQUEST_PAGING_50_V1. */
enum PullRequestPageSize {
  Ten = 10,
  Twenty = 20,
  Fifty = 50,
}
const pageSizeChoices = [
  PullRequestPageSize.Ten,
  PullRequestPageSize.Twenty,
  PullRequestPageSize.Fifty,
];

interface LoadedPullRequests {
  repositoryId: string;
  revision: bigint;
  scopeKey: string;
  state: ItemState;
  search: string;
  pageSize: number;
  query: GitHubQuery;
}

const plainSearch = (value: string) =>
  value.length <= 120 && /^[\p{L}\p{N} ._-]*$/u.test(value);

function RepositoryNavigationRow({
  row,
  selected,
  expanded,
  active,
  label,
  choose,
  toggleDetails,
}: {
  row: Resource;
  selected: boolean;
  expanded: boolean;
  active: boolean;
  label: string;
  choose: () => void;
  toggleDetails: () => void;
}) {
  useLocale();
  const dispatch = useDestinationDispatch();
  const detailsId = useId();
  const name = label;
  const config = document(row);
  const owner = text(config.github_owner),
    repository = text(config.github_name);
  const detailsLabel = copy("pull-requests.repositoryDetails", {
    name,
  });
  const { trigger, popup, presentation, reveal } = useRepositoryDetailsPopup({
    expanded,
    active,
    contentKey: `${detailsLabel}\0${owner}\0${repository}`,
  });
  const failed =
    presentation === RepositoryDetailsPresentation.Unavailable ||
    presentation === RepositoryDetailsPresentation.Failed;
  const failureId = `${detailsId}-failure`;
  const visible =
    expanded && active && presentation === RepositoryDetailsPresentation.Open;
  return (
    <div className="pr-repository-item">
      <div className="pr-repository-heading" data-selected={selected}>
        <button
          type="button"
          className="sidebar-repository-row"
          aria-label={name}
          aria-pressed={selected}
          onClick={(event) =>
            dispatch(
              event,
              { surface: Surface.PullRequests, item: row.id },
              choose,
            )
          }
        >
          <svg
            className="sidebar-icon sidebar-repository-icon"
            aria-hidden="true"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M5 3h14v18H5zM9 3v18M13 7h3M13 11h3" />
          </svg>
          <span className="sidebar-repository-info">{name}</span>
        </button>
        <DisclosureButton
          ref={trigger}
          density={DisclosureDensity.Compact}
          type="button"
          className="pr-repository-details-toggle"
          aria-label={detailsLabel}
          aria-expanded={visible}
          aria-describedby={failed ? failureId : undefined}
          title={
            failed
              ? copy("pull-requests.repositoryDetailsUnavailable")
              : undefined
          }
          aria-controls={detailsId}
          onClick={() => (expanded && !visible ? reveal() : toggleDetails())}
        >
          <span className="sidebar-sr-only">{detailsLabel}</span>
        </DisclosureButton>
      </div>
      <DisclosureContent
        id={detailsId}
        ref={popup}
        popover="manual"
        tabIndex={0}
        className="pr-repository-details"
        role="region"
        aria-label={detailsLabel}
        hidden={!visible}
      >
        <dl>
          <dt>{copy("pull-requests.githubRepository")}</dt>
          <dd>
            {owner && repository
              ? `${owner}/${repository}`
              : copy("pull-requests.repositoryNotConfigured")}
          </dd>
        </dl>
      </DisclosureContent>
      {failed ? (
        <span id={failureId} className="sidebar-sr-only" role="status">
          {copy("pull-requests.repositoryDetailsUnavailable")}
        </span>
      ) : null}
    </div>
  );
}

function PendingDismissal({
  intent,
  id,
}: {
  intent: RetainedMutationIntent;
  id: string;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const mutation = useRetainedMutation(
    intent.key,
    IntegrationQuery.dismissPullRequestProblem,
  );
  return (
    <article className="pending-pr-action">
      <strong>
        <LocalizedText
          id="pull-requests.problemDismissal_9c312b"
          components={{
            s0: <>{productLabel(PresentationKind.Resource, id)}</>,
          }}
        />
      </strong>
      <p>
        {intent.busy
          ? copy("pull-requests.submitting_cba659")
          : copy("pull-requests.acknowledgmentUncertain_62e6b9")}
      </p>
      {intent.uncertain ? (
        <button disabled={mutation.busy} onClick={mutation.retry}>
          {copy("pull-requests.retryOriginalDismissal_bb2de0")}
        </button>
      ) : null}
    </article>
  );
}

function PendingCollection({
  intent,
  repositoryId,
  number,
}: {
  intent: RetainedMutationIntent;
  repositoryId: string;
  number: string;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const mutation = useRetainedMutation(
    intent.key,
    IntegrationQuery.refreshPullRequestProblems,
  );
  return (
    <article className="pending-pr-action">
      <strong>
        <LocalizedText
          id="pull-requests.problemCollectionRepositoryPr_233e06"
          components={{
            s0: <>{productLabel(PresentationKind.Repository, repositoryId)}</>,
            s1: <>{number}</>,
          }}
        />
      </strong>
      <p>
        {intent.busy
          ? copy("pull-requests.submitting_cba659")
          : copy("pull-requests.acknowledgmentUncertain_62e6b9")}
      </p>
      {intent.uncertain ? (
        <button disabled={mutation.busy} onClick={mutation.retry}>
          {copy("pull-requests.retryOriginalProblemCollection_31fab2")}
        </button>
      ) : null}
    </article>
  );
}

function PendingAllowance({
  intent,
  repositoryId,
  pullRequestId,
}: {
  intent: RetainedMutationIntent;
  repositoryId: string;
  pullRequestId: string;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const workflow = usePRWorkflow();
  const mutation = useRetainedMutation(
    intent.key,
    IntegrationQuery.resumePullRequestRemediation,
    () =>
      workflow.cancelAllowance(
        `pr-remediation-confirm:${repositoryId}:${pullRequestId}`,
      ),
  );
  return (
    <article className="pending-pr-action">
      <strong>
        <LocalizedText
          id="pull-requests.attemptAllowanceRepositoryPr_510835"
          components={{
            s0: <>{productLabel(PresentationKind.Repository, repositoryId)}</>,
            s1: <>{pullRequestId}</>,
          }}
        />
      </strong>
      <p>
        {intent.busy
          ? copy("pull-requests.submitting_cba659")
          : copy("pull-requests.acknowledgmentUncertain_62e6b9")}
      </p>
      {intent.uncertain ? (
        <button disabled={mutation.busy} onClick={mutation.retry}>
          {copy("pull-requests.retryOriginalAllowanceResumption_991985")}
        </button>
      ) : null}
    </article>
  );
}

function PendingFix({
  intent,
  remoteRepositoryId,
  pullRequestId,
}: {
  intent: RetainedMutationIntent;
  remoteRepositoryId: string;
  pullRequestId: string;
}) {
  const mutation = useRetainedMutation(
    intent.key,
    PullRequestFixQuery.requestPullRequestFix,
  );
  return (
    <article className="pending-pr-action">
      <strong>
        <LocalizedText
          id="pull-requests.pendingFix"
          components={{
            s0: <>{remoteRepositoryId}</>,
            s1: <>{pullRequestId}</>,
          }}
        />
      </strong>
      <p>
        {intent.busy
          ? copy("pull-requests.submitting_cba659")
          : copy("pull-requests.acknowledgmentUncertain_62e6b9")}
      </p>
      <Problem error={mutation.error} />
      {intent.uncertain ? (
        <button disabled={mutation.busy} onClick={mutation.retry}>
          {copy("pr-fix.retryOriginalFixRequest_3ecf60")}
        </button>
      ) : null}
    </article>
  );
}

function PendingPRActions() {
  useLocale();
  const intents = useRetainedMutationIntents("pr-");
  const repositoryIntents = useRetainedMutationIntents(
    repositoryConfigurationPrefix,
  );
  const workflow = usePRWorkflow();
  const rows = [
    ...repositoryIntents.map((intent) => (
      <PendingRepositoryConfiguration key={intent.key} intent={intent} />
    )),
    ...intents.flatMap((intent) => {
      if (intent.key.startsWith("pr-problem-dismiss:")) {
        return [
          <PendingDismissal
            key={intent.key}
            intent={intent}
            id={intent.key.slice("pr-problem-dismiss:".length)}
          />,
        ];
      }
      if (intent.key.startsWith("pr-problem-refresh:")) {
        const [, repositoryId, number] = intent.key.split(":");
        return repositoryId && number
          ? [
              <PendingCollection
                key={intent.key}
                intent={intent}
                repositoryId={repositoryId}
                number={number}
              />,
            ]
          : [];
      }
      if (intent.key.startsWith("pr-remediation-resume:")) {
        const [, repositoryId, pullRequestId] = intent.key.split(":");
        return repositoryId && pullRequestId
          ? [
              <PendingAllowance
                key={intent.key}
                intent={intent}
                repositoryId={repositoryId}
                pullRequestId={pullRequestId}
              />,
            ]
          : [];
      }
      if (intent.key.startsWith("pr-fix:")) {
        const [, remoteRepositoryId, pullRequestId] = intent.key.split(":");
        return remoteRepositoryId && pullRequestId
          ? [
              <PendingFix
                key={intent.key}
                intent={intent}
                remoteRepositoryId={remoteRepositoryId}
                pullRequestId={pullRequestId}
              />,
            ]
          : [];
      }
      return [];
    }),
  ];
  const confirmations = [...workflow.confirmations.values()].map(
    (confirmation) => (
      <article className="pending-pr-action" key={confirmation.key}>
        <strong>
          <LocalizedText
            id="pull-requests.allowanceConfirmationPr_3184de"
            components={{ s0: <>{confirmation.selection.number}</> }}
          />
        </strong>
        <p>{copy("pull-requests.confirmationRetainedOpenThisPrAnd_15ba22")}</p>
        <button
          type="button"
          onClick={() => workflow.cancelAllowance(confirmation.key)}
        >
          {copy("pull-requests.cancelAllowanceConfirmation_111886")}
        </button>
      </article>
    ),
  );
  return (
    <section
      className="pending-pr-actions"
      data-empty={!rows.length && !confirmations.length}
      aria-label={copy("pull-requests.pendingPrActions_7f3945")}
    >
      <h3>{copy("pull-requests.pendingPrActions_7f3945")}</h3>
      {rows.length || confirmations.length ? (
        <>
          {rows}
          {confirmations}
        </>
      ) : (
        <p>{copy("pull-requests.noPendingPrActions_d8073e")}</p>
      )}
    </section>
  );
}

const transportIdentities = new WeakMap<Transport, string>();
/** Stable opaque identity for one transport object, used only in query keys. */
function transportIdentity(transport: Transport): string {
  let identity = transportIdentities.get(transport);
  if (!identity) {
    identity = newRequestId();
    transportIdentities.set(transport, identity);
  }
  return identity;
}

export function PullRequests({
  active,
  openSettings,
  restore,
  selectionChanged,
}: {
  active: boolean;
  openSettings: (destination?: SettingsNavigationEntry) => void;
  /** Repository reopened by the product route after a reload. */
  restore?: { id: string; generation: string };
  /** Reports the selected repository ID ("" when none) for the product route. */
  selectionChanged?: (id: string) => void;
}) {
  const productLabel = useProductLabels();
  const navigationIntent = useNavigationIntent();
  useLocale();
  // Explicit destination activation (first entry, or returning to this retained
  // page) owns one 160ms Y4 entrance on the page content. Repository and state
  // changes, typing, refresh and reads never restart it, and nothing waits for it.
  const destinationActivations = useActivationCount(active);
  const pageEntrance = useEntranceActivation(
    `pull-requests:${destinationActivations}`,
    active,
    EntranceMotion.Y4,
  );
  const { root, bindRoot } = useGitHubScrollRoot();
  const [repositoryId, setRepositoryId] = useState("");
  const [expandedRepositoryId, setExpandedRepositoryId] = useState("");
  const [state, setState] = useState(ItemState.Open);
  const stateGroupId = useId();
  // Assign exact-ID presentation numbers once in this connection-owned view.
  // Removed rows never donate their numbers to later catalog entries.
  const repositoryNames = useRef(
    new Map<string, { number: number; name: string }>(),
  );
  const [search, setSearch] = useState("");
  const [appliedSearch, setAppliedSearch] = useState("");
  const [composing, setComposing] = useState(false);
  const [pageSize, setPageSize] = useState<number>(PullRequestPageSize.Twenty);
  // The capability gate trusts only a GetStatus observation made through the
  // current transport during the current connection epoch. A same-identity
  // reconnect preserves the query client and may even keep the transport, so the
  // observation is keyed by both: after a downgrade or peer replacement the
  // previous peer's capabilities are unavailable in the same render and no
  // 50-item request can reach the new peer before it answers.
  const transport = useTransport();
  const connectionEpoch = useConnectionEpoch();
  const statusOptions = createQueryOptions(
    SystemQuery.getStatus,
    {},
    { transport },
  );
  const status = useBusinessQuery({
    ...statusOptions,
    queryKey: [
      ...statusOptions.queryKey,
      {
        pageSizeCapabilityTransport: transportIdentity(transport),
        pageSizeCapabilityEpoch: connectionEpoch,
      },
    ],
    queryFn: (context) =>
      statusOptions.queryFn!({
        ...context,
        queryKey: statusOptions.queryKey,
      } as never),
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    gcTime: 0,
    staleTime: Infinity,
    enabled: active,
  });
  const statusSettled = Boolean(status.data) || Boolean(status.error);
  const pagingSupported = Boolean(
    status.data?.capabilities.includes(
      SystemCapability.PULL_REQUEST_PAGING_50_V1,
    ),
  );
  // A peer without the capability never receives a 50-item request, even when
  // 50 was chosen before the connection changed.
  const pageSizeAllowed =
    pageSize !== PullRequestPageSize.Fifty || pagingSupported;
  useEffect(() => {
    if (!pageSizeAllowed && statusSettled)
      setPageSize(PullRequestPageSize.Twenty);
  }, [pageSizeAllowed, statusSettled]);
  const [loaded, setLoaded] = useState<LoadedPullRequests>();
  const [navigation, setNavigation] = useState<PullRequestNavigation>();
  const closeDrawer = useCloseSidebarDrawer();
  const paneVisible = useSidebarPaneVisible();
  const navigationActive = active && paneVisible;
  const repositories = useGitHubCatalog(
    EntityKind.REPOSITORY,
    navigationActive,
  );
  for (const row of repositories.data?.resources ?? []) {
    const known = repositoryNames.current.get(row.id);
    repositoryNames.current.set(row.id, {
      number: known?.number ?? repositoryNames.current.size + 1,
      name: productResourceName(productLabel, row),
    });
  }
  const nameCounts = new Map<string, number>();
  for (const { name } of repositoryNames.current.values())
    nameCounts.set(name, (nameCounts.get(name) ?? 0) + 1);
  const repositoryLabel = (row: Resource) => {
    const observed = repositoryNames.current.get(row.id)!;
    return (nameCounts.get(observed.name) ?? 0) > 1
      ? copy("pull-requests.repositoryNumberedName", {
          name: observed.name,
          number: observed.number,
        })
      : observed.name;
  };
  const selectedQuery = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.REPOSITORY, id: repositoryId },
    { enabled: active && Boolean(repositoryId) },
  );
  const metadataScope = useRef({ active: false, id: "", generation: 0 });
  if (
    metadataScope.current.active !== active ||
    metadataScope.current.id !== repositoryId
  ) {
    metadataScope.current = {
      active,
      id: repositoryId,
      generation: metadataScope.current.generation + 1,
    };
  }
  const metadataGeneration = metadataScope.current.generation;
  const [confirmedMetadataGeneration, setConfirmedMetadataGeneration] =
    useState<number>();
  useEffect(() => {
    if (!active || !repositoryId) return;
    let current = true;
    // A cached selected resource is historical on reentry. Join the original
    // query's current read rather than admitting GitHub work before it settles.
    void selectedQuery.refetch({ cancelRefetch: false }).then(
      () => {
        if (current && metadataScope.current.generation === metadataGeneration)
          setConfirmedMetadataGeneration(metadataGeneration);
      },
      () => {
        console.warn("delidev.pull_requests.metadata_confirmation_failed", {
          phase: "activation",
        });
      },
    );
    return () => {
      current = false;
    };
  }, [active, repositoryId, metadataGeneration, selectedQuery.refetch]);
  const metadataConfirmed = confirmedMetadataGeneration === metadataGeneration;
  const selected = selectedQuery.data?.resource;
  const config = document(selected);
  const configured = Boolean(
    selected &&
    selected.id === repositoryId &&
    selected.kind === EntityKind.REPOSITORY &&
    selected.schemaVersion === 1 &&
    text(config.integration_id) &&
    text(config.github_owner) &&
    text(config.github_name),
  );
  const searchValid = plainSearch(search.trim());
  const repositorySavePending = useRepositoryConfigurationPending(repositoryId);
  const canLoad = Boolean(
    pageSizeAllowed &&
    !repositorySavePending &&
    active &&
    metadataConfirmed &&
    configured &&
    !selectedQuery.error &&
    !selectedQuery.isFetching,
  );
  const scopeKey = selected
    ? JSON.stringify([
        selected.id,
        selected.revision.toString(),
        state,
        appliedSearch,
        pageSize,
      ])
    : "";

  useEffect(() => {
    if (!active) {
      setLoaded(undefined);
      setNavigation(undefined);
    }
  }, [active]);

  // Only a user's choice is a navigation intent; the route restore selects the
  // repository through `selectRepositoryId` without registering one.
  const chooseRepository = (row: Resource) => {
    navigationIntent(NavigationIntentSource.RepositorySelect);
    selectRepositoryId(row.id);
  };
  const selectRepositoryId = (id: string) => {
    if (id === repositoryId) return;
    setLoaded(undefined);
    setNavigation(undefined);
    setRepositoryId(id);
    setState(ItemState.Open);
    setSearch("");
    setAppliedSearch("");
    setComposing(false);
    setPageSize(PullRequestPageSize.Twenty);
  };
  const restored = useRef("");
  useEffect(() => {
    if (!restore || restored.current === restore.generation) return;
    restored.current = restore.generation;
    selectRepositoryId(restore.id);
    // The generation guard makes a rerun with a newer closure a no-op.
  }, [restore, selectRepositoryId]);
  useEffect(() => {
    selectionChanged?.(repositoryId);
  }, [repositoryId, selectionChanged]);
  useEffect(() => {
    const term = search.trim();
    if (!active || composing || !searchValid || term === appliedSearch) return;
    const timer = setTimeout(() => setAppliedSearch(term), 300);
    return () => clearTimeout(timer);
  }, [active, composing, search, searchValid, appliedSearch]);

  useEffect(() => {
    if (!selected || !canLoad || loaded?.scopeKey === scopeKey) return;
    // A return during a pending search must not read the superseded term first.
    if (!loaded && searchValid && !composing && search.trim() !== appliedSearch)
      return;
    const query: GitHubQuery = {
      kind: ItemKind.PullRequest,
      operation: appliedSearch ? QueryOperation.Search : QueryOperation.List,
      state,
      page: 1,
      page_size: pageSize,
      ...(appliedSearch ? { search: appliedSearch } : {}),
    };
    // Every effective query owns a new first-page scope. Existing pagination
    // owners cancel replacement reads and fence transports that ignore abort.
    setNavigation({ scopeKey, query, previous: [] });
    setLoaded({
      repositoryId: selected.id,
      revision: selected.revision,
      scopeKey,
      state,
      search: appliedSearch,
      pageSize,
      query,
    });
  }, [
    canLoad,
    scopeKey,
    loaded,
    selected,
    state,
    pageSize,
    appliedSearch,
    search,
    searchValid,
    composing,
  ]);

  const resultsCurrent = Boolean(
    active &&
    loaded &&
    selected &&
    loaded.repositoryId === selected.id &&
    loaded.revision === selected.revision,
  );
  const listLayout =
    !navigation ||
    navigation.query.operation === QueryOperation.List ||
    navigation.query.operation === QueryOperation.Search;
  const cardsCurrent =
    resultsCurrent &&
    loaded &&
    navigation?.scopeKey === loaded.scopeKey &&
    (navigation.query.operation === QueryOperation.List ||
      navigation.query.operation === QueryOperation.Search);
  const filtersChanged = Boolean(
    loaded &&
    (loaded.state !== state ||
      loaded.search !== search.trim() ||
      loaded.pageSize !== pageSize),
  );
  return (
    <>
      <SidebarSurface
        active={active}
        title={copy("pull-requests.pullRequests_d9e3f2")}
        showHeading={false}
      >
        <header className="pr-sidebar-title">
          <h2>{copy("pull-requests.pullRequests_d9e3f2")}</h2>
          <SettingsActionScope>
            <SettingsActionButton
              className="pr-sidebar-refresh"
              icon={SettingsActionIcon.Refresh}
              presentation={SettingsActionPresentation.Icon}
              aria-label={copy("pull-requests.refresh_0e9161")}
              type="button"
              disabled={!active || repositories.isFetching}
              onClick={repositories.refetch}
            >
              {copy("pull-requests.refresh_0e9161")}
            </SettingsActionButton>
          </SettingsActionScope>
        </header>
        <div ref={bindRoot}>
          <Problem error={paginationError(repositories.error?.failure)} />
          {repositories.isPending && active ? (
            <p role="status">
              {copy("pull-requests.loadingRepositories_460ca9")}
            </p>
          ) : null}
          {repositories.error && repositories.data ? (
            <p className="sidebar-help">
              {copy(
                "pull-requests.refreshFailedShowingThePreviousRepository_6c5a34",
              )}
            </p>
          ) : null}
          {repositories.data?.resources.map((row) => (
            <RepositoryNavigationRow
              key={row.id}
              row={row}
              selected={repositoryId === row.id}
              expanded={expandedRepositoryId === row.id}
              active={navigationActive}
              label={repositoryLabel(row)}
              choose={() => chooseRepository(row)}
              toggleDetails={() =>
                setExpandedRepositoryId((current) =>
                  current === row.id ? "" : row.id,
                )
              }
            />
          ))}
          {repositories.data?.resources.length === 0 ? (
            <div className="sidebar-repository-empty">
              <svg
                className="sidebar-icon"
                aria-hidden="true"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M3 6h7l2 2h9v11H3z" />
              </svg>
              <p>{copy("pull-requests.noRepositoriesOnThisPage_249a41")}</p>
            </div>
          ) : null}
          <ScrollContinuation
            query={repositories}
            root={root}
            active={active}
            label={copy("pull-requests.repositories_1e32af")}
            showInitial={false}
          />
        </div>
        {repositoryId ? (
          <>
            <section
              className="sidebar-query-options"
              aria-label={copy("pull-requests.queryOptions_aeced2")}
            >
              <h3>{copy("pull-requests.queryOptions_aeced2")}</h3>
              <form
                onKeyDown={guardEnterSubmission}
                className="sidebar-form"
                onSubmit={(event) => {
                  consumeFormSubmit(event);
                }}
              >
                <fieldset className="pr-state-field">
                  <legend>{copy("pull-requests.state_a3b50c")}</legend>
                  <div className="pr-state-options">
                    {[
                      {
                        value: ItemState.Open,
                        label: copy("pull-requests.open_ed077f"),
                      },
                      {
                        value: ItemState.Closed,
                        label: copy("pull-requests.closed_c21ead"),
                      },
                      {
                        value: ItemState.All,
                        label: copy("pull-requests.all_a52ace"),
                      },
                    ].map((option) => (
                      <label key={option.value} className="pr-state-choice">
                        <input
                          type="radio"
                          name={stateGroupId}
                          value={option.value}
                          checked={state === option.value}
                          onChange={() => setState(option.value)}
                        />
                        <span>{option.label}</span>
                      </label>
                    ))}
                  </div>
                </fieldset>
                <label>
                  {copy("pull-requests.searchTitleAndBody_f2c94c")}
                  <span className="pr-search-input">
                    <Icon name="search" />
                    <input
                      value={search}
                      maxLength={120}
                      onChange={(event) => setSearch(event.target.value)}
                      onCompositionStart={() => setComposing(true)}
                      onCompositionEnd={() => setComposing(false)}
                    />
                  </span>
                </label>
                {!searchValid ? (
                  <p role="alert">
                    {copy(
                      "pull-requests.usePlainWordsNumbersSpacesHyphens_0bce88",
                    )}
                  </p>
                ) : null}
                {selectedQuery.error ? (
                  <Problem error={selectedQuery.error} />
                ) : null}
                {selected && !configured ? (
                  <p className="sidebar-help">
                    {copy(
                      "pull-requests.setASupportedGithubProfileOwner_c9cd89",
                    )}
                  </p>
                ) : null}
                {repositoryId && selectedQuery.isPending ? (
                  <p role="status">
                    {copy("pull-requests.loadingRepositorySettings_98ac56")}
                  </p>
                ) : null}
                <label>
                  {copy("pull-requests.prPageSize_f04cb9")}
                  <select
                    value={pageSize}
                    onChange={(event) => {
                      const size = Number(event.target.value);
                      if (
                        size === PullRequestPageSize.Fifty &&
                        !pagingSupported
                      )
                        return;
                      setPageSize(size);
                    }}
                  >
                    {pageSizeChoices.map((size) => (
                      <option
                        key={size}
                        value={size}
                        disabled={
                          size === PullRequestPageSize.Fifty && !pagingSupported
                        }
                      >
                        {size}
                      </option>
                    ))}
                  </select>
                </label>
                {!pagingSupported ? (
                  <p className="sidebar-help">
                    {copy(
                      statusSettled
                        ? "pull-requests.pageSizeUnsupported"
                        : "pull-requests.pageSizeChecking",
                    )}
                  </p>
                ) : null}
                {loaded && filtersChanged ? (
                  <p className="sidebar-help">
                    {copy(
                      "pull-requests.theDisplayedResultsBelongToThe_44e130",
                    )}
                  </p>
                ) : null}
              </form>
            </section>
          </>
        ) : (
          <p className="sidebar-help pr-repository-guidance">
            {copy("pull-requests.selectARepositoryNoGithubRequest_b499d2")}
          </p>
        )}
        <div className="pr-repository-settings">
          <button
            type="button"
            className="sidebar-action"
            onClick={() => {
              closeDrawer();
              openSettings(SettingsEntryDestination.Repositories);
            }}
          >
            <Icon name="settings" />
            {copy("pull-requests.repositorySettings_b00980")}
          </button>
        </div>
      </SidebarSurface>
      <section
        hidden={!active}
        className="page pull-requests-page"
        data-list-layout={listLayout}
        {...pageEntrance}
      >
        {!cardsCurrent ? (
          <>
            {listLayout ? (
              <header className="pr-list-header">
                <div>
                  <h2>{copy("pull-requests.pullRequests_d9e3f2")}</h2>
                  {selected ? (
                    <p>{productResourceName(productLabel, selected)}</p>
                  ) : null}
                </div>
              </header>
            ) : (
              <h2>{copy("pull-requests.pullRequests_d9e3f2")}</h2>
            )}
            <PendingPRActions />
          </>
        ) : null}
        <Problem error={selectedQuery.error} />
        {!repositoryId ? (
          <p>
            {copy("pull-requests.selectOneConfiguredRepositoryInThe_6c065a")}
          </p>
        ) : null}
        {repositoryId && !selectedQuery.isPending && !selected ? (
          <p role="alert">
            {copy("pull-requests.thisRepositoryIsNoLongerAvailable_3fa1ad")}
          </p>
        ) : null}
        {selected && !configured ? (
          selected.id === repositoryId &&
          selected.kind === EntityKind.REPOSITORY &&
          selected.schemaVersion === 1 &&
          selected.revision > 0n &&
          text(config.github_owner) &&
          text(config.github_name) &&
          !text(config.integration_id) &&
          active &&
          metadataConfirmed &&
          !selectedQuery.isFetching &&
          !selectedQuery.error ? (
            <RepositoryProfileAssociation
              key={`${metadataGeneration}:${selected.revision}`}
              repository={selected}
              active={active}
              profiles={() => {
                closeDrawer();
                openSettings(SettingsEntryDestination.GitProfiles);
              }}
              review={async () => {
                const result = await selectedQuery.refetch({
                  cancelRefetch: false,
                });
                const row = result.data?.resource;
                if (
                  result.error ||
                  row?.id !== repositoryId ||
                  row.kind !== EntityKind.REPOSITORY ||
                  row.schemaVersion !== 1
                )
                  throw new Error("Repository review is unavailable");
              }}
            />
          ) : (
            <>
              <p>
                {copy(
                  "pull-requests.configureThisRepositorySGithubProfile_86db03",
                )}
              </p>
              <div className="actions">
                <button
                  type="button"
                  onClick={() => {
                    closeDrawer();
                    openSettings({
                      category: SettingsCategory.Repositories,
                      generation: newRequestId(),
                      resourceId: repositoryId,
                      resourceKind: EntityKind.REPOSITORY,
                    });
                  }}
                >
                  {copy("pull-requests.editCurrentRepository")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    closeDrawer();
                    openSettings(SettingsEntryDestination.GitProfiles);
                  }}
                >
                  {copy("pull-requests.githubProfiles")}
                </button>
              </div>
            </>
          )
        ) : null}
        {selected && configured && !loaded ? (
          <p>{copy("pull-requests.chooseTheStateAndOptionalTitle_5fb280")}</p>
        ) : null}
        {resultsCurrent &&
        loaded &&
        navigation?.scopeKey === loaded.scopeKey ? (
          <StandalonePullRequestResults
            key={loaded.scopeKey}
            selected={selected!}
            navigation={navigation}
            // A retained 50-item scope pauses until the current transport
            // confirms the capability, so a replaced peer never receives it.
            // This follows the loaded scope, which lags the page-size choice.
            active={
              active &&
              (loaded.pageSize !== PullRequestPageSize.Fifty || pagingSupported)
            }
            changeNavigation={setNavigation}
            pending={<PendingPRActions />}
          />
        ) : null}
      </section>
    </>
  );
}
