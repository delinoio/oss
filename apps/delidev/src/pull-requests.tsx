import { useEffect, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, IntegrationQuery, PullRequestFixQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { ItemKind, ItemState, QueryOperation, type GitHubQuery } from "./github-query-model";
import { StandalonePullRequestResults, type PullRequestNavigation } from "./github-items";
import { Problem } from "./ui";
import { SettingsEntryDestination } from "./settings";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { useRetainedMutation, useRetainedMutationIntents, type RetainedMutationIntent } from "./mutation";
import { usePRWorkflow } from "./pr-workflow";

interface LoadedPullRequests {
  repositoryId: string;
  revision: bigint;
  scopeKey: string;
  state: ItemState;
  search: string;
  pageSize: number;
  query: GitHubQuery;
}

const plainSearch = (value: string) => value.length <= 120 && /^[\p{L}\p{N} ._-]*$/u.test(value);

function PendingDismissal({ intent, id }: { intent: RetainedMutationIntent; id: string }) {
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.dismissPullRequestProblem);
  return <article className="pending-pr-action"><strong>Problem dismissal · {id}</strong><p>{intent.busy ? "Submitting" : "Acknowledgment uncertain"}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original dismissal</button> : null}</article>;
}

function PendingCollection({ intent, repositoryId, number }: { intent: RetainedMutationIntent; repositoryId: string; number: string }) {
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.refreshPullRequestProblems);
  return <article className="pending-pr-action"><strong>Problem collection · repository {repositoryId} · PR #{number}</strong><p>{intent.busy ? "Submitting" : "Acknowledgment uncertain"}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original problem collection</button> : null}</article>;
}

function PendingAllowance({ intent, repositoryId, pullRequestId }: { intent: RetainedMutationIntent; repositoryId: string; pullRequestId: string }) {
  const workflow = usePRWorkflow();
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.resumePullRequestRemediation, () => workflow.cancelAllowance(`pr-remediation-confirm:${repositoryId}:${pullRequestId}`));
  return <article className="pending-pr-action"><strong>Attempt allowance · repository {repositoryId} · PR {pullRequestId}</strong><p>{intent.busy ? "Submitting" : "Acknowledgment uncertain"}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original allowance resumption</button> : null}</article>;
}

function PendingFix({ intent, remoteRepositoryId, pullRequestId }: { intent: RetainedMutationIntent; remoteRepositoryId: string; pullRequestId: string }) {
  const mutation = useRetainedMutation(intent.key, PullRequestFixQuery.requestPullRequestFix);
  return <article className="pending-pr-action"><strong>Manual PR fix · remote repository {remoteRepositoryId} · PR ID {pullRequestId}</strong><p>{intent.busy ? "Submitting" : "Acknowledgment uncertain"}</p><Problem error={mutation.error} />{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original fix request</button> : null}</article>;
}

function PendingPRActions() {
  const intents = useRetainedMutationIntents("pr-");
  const workflow = usePRWorkflow();
  const rows = intents.flatMap((intent) => {
    if (intent.key.startsWith("pr-problem-dismiss:")) {
      return [<PendingDismissal key={intent.key} intent={intent} id={intent.key.slice("pr-problem-dismiss:".length)} />];
    }
    if (intent.key.startsWith("pr-problem-refresh:")) {
      const [, repositoryId, number] = intent.key.split(":");
      return repositoryId && number ? [<PendingCollection key={intent.key} intent={intent} repositoryId={repositoryId} number={number} />] : [];
    }
    if (intent.key.startsWith("pr-remediation-resume:")) {
      const [, repositoryId, pullRequestId] = intent.key.split(":");
      return repositoryId && pullRequestId ? [<PendingAllowance key={intent.key} intent={intent} repositoryId={repositoryId} pullRequestId={pullRequestId} />] : [];
    }
    if (intent.key.startsWith("pr-fix:")) {
      const [, remoteRepositoryId, pullRequestId] = intent.key.split(":");
      return remoteRepositoryId && pullRequestId ? [<PendingFix key={intent.key} intent={intent} remoteRepositoryId={remoteRepositoryId} pullRequestId={pullRequestId} />] : [];
    }
    return [];
  });
  const confirmations = [...workflow.confirmations.values()].map((confirmation) => <article className="pending-pr-action" key={confirmation.key}><strong>Allowance confirmation · PR #{confirmation.selection.number}</strong><p>Confirmation retained. Open this PR and review current history before confirming; no action is submitted automatically.</p><button type="button" onClick={() => workflow.cancelAllowance(confirmation.key)}>Cancel allowance confirmation</button></article>);
  return <section className="pending-pr-actions" aria-label="Pending PR actions"><h3>Pending PR actions</h3>{rows.length || confirmations.length ? <>{rows}{confirmations}</> : <p>No pending PR actions.</p>}</section>;
}

export function PullRequests({ active, openSettings }: { active: boolean; openSettings: (destination?: SettingsEntryDestination) => void }) {
  const [repositoryPage, setRepositoryPage] = useState("");
  const [repositoryId, setRepositoryId] = useState("");
  const [state, setState] = useState(ItemState.Open);
  const [search, setSearch] = useState("");
  const [pageSize, setPageSize] = useState(20);
  const [loaded, setLoaded] = useState<LoadedPullRequests>();
  const [navigation, setNavigation] = useState<PullRequestNavigation>();
  const closeDrawer = useCloseSidebarDrawer();
  const repositories = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.REPOSITORY, pageSize: 50, pageToken: repositoryPage } }, { enabled: active });
  const selectedOnPage = repositories.data?.resources.find((row) => row.id === repositoryId);
  const selectedQuery = useQuery(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id: repositoryId }, { enabled: active && Boolean(repositoryId) && !selectedOnPage });
  const selected = selectedOnPage ?? selectedQuery.data?.resource;
  const config = document(selected);
  const configured = Boolean(selected && selected.schemaVersion === 1 && text(config.integration_id) && text(config.github_owner) && text(config.github_name));
  const searchValid = plainSearch(search.trim());
  const canLoad = Boolean(active && configured && !selectedQuery.isFetching && searchValid);
  const scopeKey = selected ? JSON.stringify([selected.id, selected.revision.toString(), state, search.trim(), pageSize]) : "";

  useEffect(() => {
    if (!active) setLoaded(undefined);
  }, [active]);

  const chooseRepository = (row: Resource) => {
    if (row.id === repositoryId) return;
    setLoaded(undefined);
    setNavigation(undefined);
    setRepositoryId(row.id);
    setState(ItemState.Open);
    setSearch("");
    setPageSize(20);
  };
  const load = (event: FormEvent) => {
    event.preventDefault();
    if (!selected || !canLoad) return;
    const term = search.trim();
    const query: GitHubQuery = { kind: ItemKind.PullRequest, operation: term ? QueryOperation.Search : QueryOperation.List, state, page: 1, page_size: pageSize, ...(term ? { search: term } : {}) };
    const retained = navigation?.scopeKey === scopeKey ? navigation : { scopeKey, query, previous: [] };
    setNavigation(retained);
    setLoaded({ repositoryId: selected.id, revision: selected.revision, scopeKey, state, search: term, pageSize, query: retained.query });
    closeDrawer();
  };

  const resultsCurrent = Boolean(active && loaded && selected && loaded.repositoryId === selected.id && loaded.revision === selected.revision);
  const filtersChanged = Boolean(loaded && (loaded.state !== state || loaded.search !== search.trim() || loaded.pageSize !== pageSize));
  return <>
    <SidebarSurface active={active} title="Pull requests">
      <header className="sidebar-list-heading"><h3>Repositories</h3><button type="button" disabled={!active || repositories.isFetching} onClick={() => { if (repositoryPage) setRepositoryPage(""); else void repositories.refetch(); }}>Refresh</button></header>
      <Problem error={repositories.error} />
      {repositories.isPending && active ? <p role="status">Loading repositories…</p> : null}
      {repositories.error && repositories.data ? <p className="sidebar-help">Refresh failed. Showing the previous repository page.</p> : null}
      {repositories.data?.resources.map((row) => <button key={row.id} type="button" className="sidebar-repository-row" aria-label={`${resourceName(row)}. Repository ID: ${row.id}`} aria-pressed={repositoryId === row.id} onClick={() => chooseRepository(row)}><svg className="sidebar-icon sidebar-repository-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M5 3h14v18H5zM9 3v18M13 7h3M13 11h3" /></svg><span className="sidebar-repository-info"><span>{resourceName(row)}</span><small>{row.id}</small></span></button>)}
      {!repositories.error && repositories.data?.resources.length === 0 ? <div className="sidebar-repository-empty"><svg className="sidebar-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h7l2 2h9v11H3z" /></svg><p>No repositories on this page.</p></div> : null}
      <nav className="sidebar-repository-pages" aria-label="Repository pages"><button disabled={!repositoryPage || repositories.isFetching} onClick={() => setRepositoryPage("")}>First</button><button disabled={!repositories.data?.nextPageToken || repositories.isFetching} onClick={() => setRepositoryPage(repositories.data!.nextPageToken)}>Next</button></nav>
      {repositoryId ? <>
        <section className="sidebar-query-options" aria-label="Query options"><h3>Query options</h3>
        <form className="sidebar-form" onSubmit={load}>
          <label>State<select value={state} onChange={(event) => setState(event.target.value as ItemState)}><option value={ItemState.Open}>Open</option><option value={ItemState.Closed}>Closed</option><option value={ItemState.All}>All</option></select></label>
          <label>Search title and body<input value={search} maxLength={120} onChange={(event) => setSearch(event.target.value)} /></label>
          {!searchValid ? <p role="alert">Use plain words, numbers, spaces, hyphens, underscores or periods.</p> : null}
          {selectedQuery.error ? <Problem error={selectedQuery.error} /> : null}
          {selected && !configured ? <p className="sidebar-help">Set a supported GitHub profile, owner and repository name in repository settings before loading.</p> : null}
          {repositoryId && !selectedOnPage && selectedQuery.isPending ? <p role="status">Loading repository settings…</p> : null}
          <label>PR page size<select value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))}>{[1, 5, 10, 20].map((size) => <option key={size} value={size}>{size}</option>)}</select></label>
          <p className="sidebar-help">No GitHub request is made until you load pull requests.</p>
          <button className="primary" disabled={!canLoad}>Load pull requests</button>
          {loaded && filtersChanged ? <p className="sidebar-help">The displayed results belong to the last loaded state and search. Load again to apply these edits.</p> : null}
        </form>
        </section>
      </> : <p className="sidebar-help">Select a repository. No GitHub request is made until you load pull requests.</p>}
      <button type="button" className="sidebar-action" onClick={() => { closeDrawer(); openSettings(SettingsEntryDestination.Repositories); }}>Repository settings</button>
    </SidebarSurface>
    <section hidden={!active} className="page pull-requests-page">
      <h2>Pull requests</h2>
      <PendingPRActions />
      <Problem error={selectedQuery.error} />
      {!repositoryId ? <p>Select one configured repository in the sidebar to get started.</p> : null}
      {repositoryId && !selectedQuery.isPending && !selected ? <p role="alert">This repository is no longer available. Refresh the repository catalog and choose another entry.</p> : null}
      {selected && !configured ? <p>Configure this repository's GitHub profile, owner and name in Repository settings before loading requests.</p> : null}
      {selected && configured && !loaded ? <p>Choose the state and optional title/body terms, then select Load pull requests. Returning to this screen requires an explicit load again.</p> : null}
      {resultsCurrent && loaded && navigation?.scopeKey === loaded.scopeKey ? <StandalonePullRequestResults key={loaded.scopeKey} selected={selected!} navigation={navigation} active={active} changeNavigation={setNavigation} /> : null}
    </section>
  </>;
}
