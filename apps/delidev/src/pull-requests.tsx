import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useId, useState, type FormEvent } from "react";
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
import { Icon } from "./sidebar";

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

function RepositoryNavigationRow({ row, selected, expanded, choose, toggleDetails }: {
  row: Resource; selected: boolean; expanded: boolean; choose: () => void; toggleDetails: () => void;
}) {
  useLocale();
  const detailsId = useId();
  const name = resourceName(row);
  const config = document(row);
  const owner = text(config.github_owner), repository = text(config.github_name);
  const detailsLabel = copy("pull-requests.repositoryDetails", { name, id: row.id });
  return <div className="pr-repository-item">
    <div className="pr-repository-heading" data-selected={selected}>
      <button type="button" className="sidebar-repository-row" aria-label={copy("pull-requests.repositoryId_cfd937", { v0: name, v1: row.id })} aria-pressed={selected} onClick={choose}>
        <svg className="sidebar-icon sidebar-repository-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M5 3h14v18H5zM9 3v18M13 7h3M13 11h3" /></svg>
        <span className="sidebar-repository-info">{name}</span>
      </button>
      <button type="button" className="pr-repository-details-toggle" aria-label={detailsLabel} aria-expanded={expanded} aria-controls={detailsId} onClick={toggleDetails}>
        <Icon name="chevron" />
      </button>
    </div>
    <div id={detailsId} className="pr-repository-details" role="region" aria-label={detailsLabel} hidden={!expanded}>
      <dl><dt>{copy("pull-requests.githubRepository")}</dt><dd>{owner && repository ? `${owner}/${repository}` : copy("pull-requests.repositoryNotConfigured")}</dd>
        <dt>{copy("pull-requests.repositoryIdentifier")}</dt><dd>{row.id}</dd></dl>
    </div>
  </div>;
}

function PendingDismissal({ intent, id }: { intent: RetainedMutationIntent; id: string }) {
  useLocale();
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.dismissPullRequestProblem);
  return <article className="pending-pr-action"><strong><LocalizedText id="pull-requests.problemDismissal_9c312b" components={{ s0: <>{id}</> }} /></strong><p>{intent.busy ? copy("pull-requests.submitting_cba659") : copy("pull-requests.acknowledgmentUncertain_62e6b9")}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("pull-requests.retryOriginalDismissal_bb2de0")}</button> : null}</article>;
}

function PendingCollection({ intent, repositoryId, number }: { intent: RetainedMutationIntent; repositoryId: string; number: string }) {
  useLocale();
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.refreshPullRequestProblems);
  return <article className="pending-pr-action"><strong><LocalizedText id="pull-requests.problemCollectionRepositoryPr_233e06" components={{ s0: <>{repositoryId}</>, s1: <>{number}</> }} /></strong><p>{intent.busy ? copy("pull-requests.submitting_cba659") : copy("pull-requests.acknowledgmentUncertain_62e6b9")}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("pull-requests.retryOriginalProblemCollection_31fab2")}</button> : null}</article>;
}

function PendingAllowance({ intent, repositoryId, pullRequestId }: { intent: RetainedMutationIntent; repositoryId: string; pullRequestId: string }) {
  useLocale();
  const workflow = usePRWorkflow();
  const mutation = useRetainedMutation(intent.key, IntegrationQuery.resumePullRequestRemediation, () => workflow.cancelAllowance(`pr-remediation-confirm:${repositoryId}:${pullRequestId}`));
  return <article className="pending-pr-action"><strong><LocalizedText id="pull-requests.attemptAllowanceRepositoryPr_510835" components={{ s0: <>{repositoryId}</>, s1: <>{pullRequestId}</> }} /></strong><p>{intent.busy ? copy("pull-requests.submitting_cba659") : copy("pull-requests.acknowledgmentUncertain_62e6b9")}</p>{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("pull-requests.retryOriginalAllowanceResumption_991985")}</button> : null}</article>;
}

function PendingFix({ intent, remoteRepositoryId, pullRequestId }: { intent: RetainedMutationIntent; remoteRepositoryId: string; pullRequestId: string }) {
  const mutation = useRetainedMutation(intent.key, PullRequestFixQuery.requestPullRequestFix);
  return <article className="pending-pr-action"><strong>Manual PR fix · remote repository {remoteRepositoryId} · PR ID {pullRequestId}</strong><p>{intent.busy ? "Submitting" : "Acknowledgment uncertain"}</p><Problem error={mutation.error} />{intent.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original fix request</button> : null}</article>;
}

function PendingPRActions() {
  useLocale();
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
  const confirmations = [...workflow.confirmations.values()].map((confirmation) => <article className="pending-pr-action" key={confirmation.key}><strong><LocalizedText id="pull-requests.allowanceConfirmationPr_3184de" components={{ s0: <>{confirmation.selection.number}</> }} /></strong><p>{copy("pull-requests.confirmationRetainedOpenThisPrAnd_15ba22")}</p><button type="button" onClick={() => workflow.cancelAllowance(confirmation.key)}>{copy("pull-requests.cancelAllowanceConfirmation_111886")}</button></article>);
  return <section className="pending-pr-actions" aria-label={copy("pull-requests.pendingPrActions_7f3945")}><h3>{copy("pull-requests.pendingPrActions_7f3945")}</h3>{rows.length || confirmations.length ? <>{rows}{confirmations}</> : <p>{copy("pull-requests.noPendingPrActions_d8073e")}</p>}</section>;
}

export function PullRequests({ active, openSettings }: { active: boolean; openSettings: (destination?: SettingsEntryDestination) => void }) {
  useLocale();
  const [repositoryPage, setRepositoryPage] = useState("");
  const [repositoryId, setRepositoryId] = useState("");
  const [expandedRepositoryId, setExpandedRepositoryId] = useState("");
  const [state, setState] = useState(ItemState.Open);
  const stateGroupId = useId();
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
    <SidebarSurface active={active} title={copy("pull-requests.pullRequests_d9e3f2")}>
      <header className="sidebar-list-heading"><h3>{copy("pull-requests.repositories_1e32af")}</h3><button type="button" disabled={!active || repositories.isFetching} onClick={() => { if (repositoryPage) setRepositoryPage(""); else void repositories.refetch(); }}><Icon name="refresh" />{copy("pull-requests.refresh_0e9161")}</button></header>
      <Problem error={repositories.error} />
      {repositories.isPending && active ? <p role="status">{copy("pull-requests.loadingRepositories_460ca9")}</p> : null}
      {repositories.error && repositories.data ? <p className="sidebar-help">{copy("pull-requests.refreshFailedShowingThePreviousRepository_6c5a34")}</p> : null}
      {repositories.data?.resources.map((row) => <RepositoryNavigationRow key={row.id} row={row} selected={repositoryId === row.id} expanded={expandedRepositoryId === row.id} choose={() => chooseRepository(row)} toggleDetails={() => setExpandedRepositoryId((current) => current === row.id ? "" : row.id)} />)}
      {!repositories.error && repositories.data?.resources.length === 0 ? <div className="sidebar-repository-empty"><svg className="sidebar-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h7l2 2h9v11H3z" /></svg><p>{copy("pull-requests.noRepositoriesOnThisPage_249a41")}</p></div> : null}
      <nav className="sidebar-repository-pages" aria-label={copy("pull-requests.repositoryPages_eeaada")}><button disabled={!repositoryPage || repositories.isFetching} onClick={() => setRepositoryPage("")}>{copy("pull-requests.first_a151ce")}</button><button disabled={!repositories.data?.nextPageToken || repositories.isFetching} onClick={() => setRepositoryPage(repositories.data!.nextPageToken)}>{copy("pull-requests.next_1ff57a")}</button></nav>
      {repositoryId ? <>
        <section className="sidebar-query-options" aria-label={copy("pull-requests.queryOptions_aeced2")}><h3>{copy("pull-requests.queryOptions_aeced2")}</h3>
        <form className="sidebar-form" onSubmit={load}>
          <fieldset className="pr-state-field"><legend>{copy("pull-requests.state_a3b50c")}</legend><div className="pr-state-options">
            {[{ value: ItemState.Open, label: copy("pull-requests.open_ed077f") }, { value: ItemState.Closed, label: copy("pull-requests.closed_c21ead") }, { value: ItemState.All, label: copy("pull-requests.all_a52ace") }].map((option) => <label key={option.value} className="pr-state-choice"><input type="radio" name={stateGroupId} value={option.value} checked={state === option.value} onChange={() => setState(option.value)} /><span>{option.label}</span></label>)}
          </div></fieldset>
          <label>{copy("pull-requests.searchTitleAndBody_f2c94c")}<span className="pr-search-input"><Icon name="search" /><input value={search} maxLength={120} onChange={(event) => setSearch(event.target.value)} /></span></label>
          {!searchValid ? <p role="alert">{copy("pull-requests.usePlainWordsNumbersSpacesHyphens_0bce88")}</p> : null}
          {selectedQuery.error ? <Problem error={selectedQuery.error} /> : null}
          {selected && !configured ? <p className="sidebar-help">{copy("pull-requests.setASupportedGithubProfileOwner_c9cd89")}</p> : null}
          {repositoryId && !selectedOnPage && selectedQuery.isPending ? <p role="status">{copy("pull-requests.loadingRepositorySettings_98ac56")}</p> : null}
          <label>{copy("pull-requests.prPageSize_f04cb9")}<select value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))}>{[1, 5, 10, 20].map((size) => <option key={size} value={size}>{size}</option>)}</select></label>
          <p className="sidebar-help">{copy("pull-requests.noGithubRequestIsMadeUntil_55d1b2")}</p>
          <button className="primary" disabled={!canLoad}>{copy("pull-requests.loadPullRequests_c952ba")}</button>
          {loaded && filtersChanged ? <p className="sidebar-help">{copy("pull-requests.theDisplayedResultsBelongToThe_44e130")}</p> : null}
        </form>
        </section>
      </> : <p className="sidebar-help pr-repository-guidance">{copy("pull-requests.selectARepositoryNoGithubRequest_b499d2")}</p>}
      <div className="pr-repository-settings"><button type="button" className="sidebar-action" onClick={() => { closeDrawer(); openSettings(SettingsEntryDestination.Repositories); }}><Icon name="settings" />{copy("pull-requests.repositorySettings_b00980")}</button></div>
    </SidebarSurface>
    <section hidden={!active} className="page pull-requests-page">
      <h2>{copy("pull-requests.pullRequests_d9e3f2")}</h2>
      <PendingPRActions />
      <Problem error={selectedQuery.error} />
      {!repositoryId ? <p>{copy("pull-requests.selectOneConfiguredRepositoryInThe_6c065a")}</p> : null}
      {repositoryId && !selectedQuery.isPending && !selected ? <p role="alert">{copy("pull-requests.thisRepositoryIsNoLongerAvailable_3fa1ad")}</p> : null}
      {selected && !configured ? <p>{copy("pull-requests.configureThisRepositorySGithubProfile_86db03")}</p> : null}
      {selected && configured && !loaded ? <p>{copy("pull-requests.chooseTheStateAndOptionalTitle_5fb280")}</p> : null}
      {resultsCurrent && loaded && navigation?.scopeKey === loaded.scopeKey ? <StandalonePullRequestResults key={loaded.scopeKey} selected={selected!} navigation={navigation} active={active} changeNavigation={setNavigation} /> : null}
    </section>
  </>;
}
