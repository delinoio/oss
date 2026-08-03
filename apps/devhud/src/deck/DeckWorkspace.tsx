import { useEffect, useMemo, useRef, useState } from "react";

import { Dialog } from "../ui/Dialog";
import {
  DeckFailureCode,
  DeckFreshness,
  DeckGrouping,
  DeckMergeMethod,
  DeckMutationKind,
  DeckOwnerKind,
  DeckProductError,
  DeckSort,
  deckFailureGuidance,
  type DeckBillingSelection,
  type DeckMutationCandidate,
  type DeckMutationInput,
  type DeckPullRequest,
  type DeckRevision,
  type DeckView,
  type DeckViewInput,
} from "./contracts";
import { useDeck } from "./DeckProvider";
import { DeckQueryEditor } from "./QueryEditor";

function defaultViewInput(billing: DeckBillingSelection | undefined): DeckViewInput {
  return {
    billing: billing ?? { organizationId: "", teamId: "" },
    name: "",
    rawQuery: "is:pr is:open",
    sort: DeckSort.RecentlyUpdated,
    grouping: DeckGrouping.None,
    notificationPreference: { enabled: false, transitions: [] },
  };
}

function failureMessage(error: unknown): string {
  if (error instanceof DeckProductError) {
    const base = deckFailureGuidance[error.code];
    return error.retryAfterSeconds === undefined
      ? base
      : `${base} Retry in ${error.retryAfterSeconds} seconds.`;
  }
  return deckFailureGuidance[DeckFailureCode.ServiceUnavailable];
}

export function DeckNavigation() {
  const { actions, state } = useDeck();
  return (
    <nav aria-label="Deck owners" className="deck-owner-navigation">
      {state.owners.map((owner) => (
        <button
          aria-current={state.selectedOwner?.ownerId === owner.ownerId ? "page" : undefined}
          key={owner.ownerId}
          onClick={() => actions.selectOwner(owner)}
          type="button"
        >
          <span>{owner.label}</span>
          <small>{owner.kind === DeckOwnerKind.Personal ? "Personal" : "Organization"}</small>
        </button>
      ))}
    </nav>
  );
}

function ViewForm({
  initial,
  billingSelections,
  onCancel,
  onSave,
  title,
}: {
  readonly initial: DeckViewInput;
  readonly billingSelections: readonly DeckBillingSelection[];
  readonly onCancel: () => void;
  readonly onSave: (input: DeckViewInput) => Promise<boolean>;
  readonly title: string;
}) {
  const { state } = useDeck();
  const [input, setInput] = useState(initial);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const hasAuthorizedBilling = billingSelections.some((billing) =>
    billing.organizationId === input.billing.organizationId && billing.teamId === input.billing.teamId
  );
  useEffect(() => headingRef.current?.focus(), []);
  return (
    <section aria-labelledby="deck-view-form-title" className="deck-editor-card">
      <h3 id="deck-view-form-title" ref={headingRef} tabIndex={-1}>{title}</h3>
      <label className="field" htmlFor="deck-view-name">
        View name
        <input
          id="deck-view-name"
          maxLength={120}
          onChange={(event) => setInput({ ...input, name: event.target.value })}
          required
          value={input.name}
        />
      </label>
      <DeckQueryEditor
        disabled={state.busy}
        onChange={(rawQuery) => setInput({ ...input, rawQuery })}
        value={input.rawQuery}
      />
      <label className="field" htmlFor="deck-view-billing">
        Billing team
        <select
          id="deck-view-billing"
          onChange={(event) => {
            const billing = billingSelections[Number(event.target.value)];
            if (billing !== undefined) setInput({ ...input, billing });
          }}
          value={Math.max(0, billingSelections.findIndex((billing) =>
            billing.organizationId === input.billing.organizationId && billing.teamId === input.billing.teamId
          ))}
        >
          {billingSelections.map((billing, index) => (
            <option key={`${billing.organizationId}:${billing.teamId}`} value={index}>
              {billing.organizationId} / {billing.teamId}
            </option>
          ))}
        </select>
      </label>
      <div className="deck-form-grid">
        <label className="field" htmlFor="deck-view-sort">
          Sort by
          <select
            id="deck-view-sort"
            onChange={(event) => setInput({ ...input, sort: event.target.value as DeckSort })}
            value={input.sort}
          >
            <option value={DeckSort.RecentlyUpdated}>Recently updated</option>
            <option value={DeckSort.RecentlyCreated}>Recently created</option>
            <option value={DeckSort.Attention}>Needs attention</option>
            <option value={DeckSort.Checks}>Checks</option>
            <option value={DeckSort.ReviewState}>Review state</option>
          </select>
        </label>
        <label className="field" htmlFor="deck-view-grouping">
          Group by
          <select
            id="deck-view-grouping"
            onChange={(event) => setInput({ ...input, grouping: event.target.value as DeckGrouping })}
            value={input.grouping}
          >
            <option value={DeckGrouping.None}>No grouping</option>
            <option value={DeckGrouping.Repository}>Repository</option>
            <option value={DeckGrouping.Author}>Author</option>
            <option value={DeckGrouping.Reviewer}>Reviewer</option>
            <option value={DeckGrouping.Status}>Status</option>
          </select>
        </label>
      </div>
      <div className="button-row">
        <button
          className="primary-button"
          disabled={state.busy || input.name.trim().length === 0 || !hasAuthorizedBilling}
          onClick={() => void onSave({ ...input, name: input.name.trim() })}
          type="button"
        >
          {state.busy ? "Saving…" : "Save view"}
        </button>
        <button className="secondary-button" disabled={state.busy} onClick={onCancel} type="button">
          Cancel
        </button>
      </div>
    </section>
  );
}

export function DeckViewManager() {
  const { actions, state } = useDeck();
  const [editing, setEditing] = useState<"create" | "update" | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<DeckView | null>(null);
  if (state.selectedOwner === null) return null;
  const canManage = state.selectedOwner.canManage;
  const inputForView = (view: DeckView): DeckViewInput => ({
    billing: view.billing,
    name: view.name,
    rawQuery: view.rawQuery,
    sort: view.sort,
    grouping: view.grouping,
    notificationPreference: view.notificationPreference,
  });
  return (
    <section aria-labelledby="deck-views-title" className="deck-views-panel">
      <div className="deck-section-heading">
        <div>
          <p className="eyebrow">Views</p>
          <h2 id="deck-views-title">{state.selectedOwner.label}</h2>
        </div>
        {canManage && editing === null ? (
          <button
            className="primary-button"
            disabled={state.selectedOwner.billingSelections.length === 0}
            onClick={() => setEditing("create")}
            type="button"
          >
            New view
          </button>
        ) : null}
      </div>
      {editing === "create" ? (
        <ViewForm
          billingSelections={state.selectedOwner.billingSelections}
          initial={defaultViewInput(state.selectedOwner.billingSelections[0])}
          onCancel={() => setEditing(null)}
          onSave={async (input) => {
            const saved = await actions.createView(state.selectedOwner!, input);
            if (saved) setEditing(null);
            return saved;
          }}
          title="Create view"
        />
      ) : null}
      {editing === "update" && state.selectedView !== null ? (
        <ViewForm
          billingSelections={state.selectedOwner.billingSelections}
          initial={inputForView(state.selectedView)}
          onCancel={() => setEditing(null)}
          onSave={async (input) => {
            const saved = await actions.saveView(state.selectedView!, input);
            if (saved) setEditing(null);
            return saved;
          }}
          title={`Edit ${state.selectedView.name}`}
        />
      ) : null}
      {canManage && state.selectedOwner.billingSelections.length === 0 ? (
        <p className="status-banner warning" role="status">A server-authorized billing team is required to create or edit a view.</p>
      ) : null}
      {editing === null ? (
        <div className="deck-view-list">
          {state.views.map((view) => (
            <article className="deck-view-row" key={view.viewId}>
              <button
                aria-current={state.selectedView?.viewId === view.viewId ? "page" : undefined}
                className="deck-view-select"
                onClick={() => actions.selectView(view)}
                type="button"
              >
                <strong>{view.name}</strong>
                <span>{view.grouping === DeckGrouping.None ? "Ungrouped" : `Grouped by ${view.grouping}`}</span>
              </button>
              {view.shortcut ? (
                <span className={view.shortcut.active ? "status-chip" : "status-chip warning"}>
                  Shortcut {view.shortcut.active ? "active" : view.shortcut.inactiveReason}
                </span>
              ) : null}
            </article>
          ))}
          {!state.loadingViews && state.views.length === 0 ? (
            <p className="muted">No views in this scope.</p>
          ) : null}
          {state.viewCursor ? (
            <button className="secondary-button" onClick={() => void actions.loadMoreViews()} type="button">
              Load more views
            </button>
          ) : null}
        </div>
      ) : null}
      {editing === null && state.selectedView !== null && canManage ? (
        <div className="button-row deck-view-actions">
          <button className="secondary-button" onClick={() => setEditing("update")} type="button">Edit view</button>
          <button className="danger-button" onClick={() => setConfirmDelete(state.selectedView)} type="button">Delete view</button>
        </div>
      ) : null}
      {confirmDelete ? (
        <Dialog title="Delete Deck view" onClose={() => setConfirmDelete(null)}>
          <p>This removes <strong>{confirmDelete.name}</strong>. It cannot be undone.</p>
          <div className="button-row">
            <button
              className="danger-button"
              disabled={state.busy}
              onClick={() => void actions.deleteView(confirmDelete).then((deleted) => {
                if (deleted) setConfirmDelete(null);
              })}
              type="button"
            >
              Confirm delete
            </button>
            <button className="secondary-button" onClick={() => setConfirmDelete(null)} type="button">Cancel</button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}

function groupKey(pullRequest: DeckPullRequest, grouping: DeckGrouping): string {
  switch (grouping) {
    case DeckGrouping.Repository: return `${pullRequest.repositoryOwner}/${pullRequest.repositoryName}`;
    case DeckGrouping.Author: return pullRequest.author;
    case DeckGrouping.Reviewer: return pullRequest.reviewers.map((reviewer) => reviewer.label).join(", ") || "No reviewer";
    case DeckGrouping.Status: return pullRequest.lifecycle;
    case DeckGrouping.None: return "Pull requests";
  }
}

function CandidatePicker({
  fixedCandidates,
  kind,
  onApply,
  pullRequest,
  remoteSearch,
}: {
  readonly fixedCandidates: readonly DeckMutationCandidate[];
  readonly kind: DeckMutationKind;
  readonly onApply: (
    input: DeckMutationInput,
    revision: DeckRevision | null,
  ) => Promise<boolean>;
  readonly pullRequest: DeckPullRequest;
  readonly remoteSearch: boolean;
}) {
  const { actions, state } = useDeck();
  const [query, setQuery] = useState("");
  const [candidates, setCandidates] = useState<readonly DeckMutationCandidate[]>([]);
  const [selected, setSelected] = useState<readonly DeckMutationCandidate[]>([]);
  const [loading, setLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState("");
  const [candidateRevision, setCandidateRevision] = useState<DeckRevision | null>(null);
  const visibleCandidates = remoteSearch ? candidates : fixedCandidates;
  const candidateKey = (candidate: DeckMutationCandidate) => `${candidate.kind}:${candidate.value}`;
  useEffect(() => {
    setCandidates([]);
    setSelected([]);
    setNextCursor("");
    setCandidateRevision(null);
  }, [kind, pullRequest]);
  const search = async (cursor = "") => {
    if (state.selectedView === null) return;
    setLoading(true);
    try {
      const page = await actions.searchMutationCandidates(
        pullRequest,
        kind,
        query,
        cursor,
      );
      if (page === null) return;
      setCandidates((current) => cursor.length === 0 ? page.items : [...current, ...page.items]);
      if (cursor.length === 0) setSelected([]);
      setNextCursor(page.nextCursor);
      setCandidateRevision(page.pullRequestRevision);
    } finally {
      setLoading(false);
    }
  };
  const apply = () => onApply({
    kind,
    users: selected.filter((candidate) => candidate.kind === "user").map((candidate) => candidate.value),
    teams: selected.filter((candidate) => candidate.kind === "team").map((candidate) => candidate.value),
    labels: selected.filter((candidate) => candidate.kind === "label").map((candidate) => candidate.value),
  }, candidateRevision);
  return (
    <div className="candidate-picker">
      {remoteSearch ? (
        <label className="field">
          Search allowed candidates
          <span className="button-row">
            <input onChange={(event) => setQuery(event.target.value)} value={query} />
            <button className="secondary-button" disabled={loading} onClick={() => void search()} type="button">
              {loading ? "Searching…" : "Search"}
            </button>
          </span>
        </label>
      ) : <p>Choose the current operands to remove.</p>}
      {visibleCandidates.map((candidate) => (
        <label className="check-field" key={`${candidate.kind}:${candidate.value}`}>
          <input
            checked={selected.some((item) => candidateKey(item) === candidateKey(candidate))}
            onChange={(event) => setSelected(event.target.checked
              ? [...selected.filter((item) => candidateKey(item) !== candidateKey(candidate)), candidate]
              : selected.filter((item) => candidateKey(item) !== candidateKey(candidate)))}
            type="checkbox"
          />
          {candidate.value} <small>{candidate.kind}</small>
        </label>
      ))}
      {remoteSearch && nextCursor.length > 0 ? (
        <button className="secondary-button" disabled={loading} onClick={() => void search(nextCursor)} type="button">
          {loading ? "Loading…" : "Load more candidates"}
        </button>
      ) : null}
      <button className="primary-button" disabled={selected.length === 0 || state.busy} onClick={() => void apply()} type="button">
        Apply
      </button>
    </div>
  );
}

function PullRequestActions({ pullRequest }: { readonly pullRequest: DeckPullRequest }) {
  const { actions, state } = useDeck();
  const [picker, setPicker] = useState<DeckMutationKind | null>(null);
  const [mergeMethod, setMergeMethod] = useState<DeckMergeMethod>(pullRequest.mergeMethods[0] ?? DeckMergeMethod.Merge);
  const [mergeOpen, setMergeOpen] = useState(false);
  const addKinds = new Set([
    DeckMutationKind.AssignUsers,
    DeckMutationKind.RequestReviewers,
    DeckMutationKind.AddLabels,
  ]);
  const removalKinds = new Set([
    DeckMutationKind.UnassignUsers,
    DeckMutationKind.RemoveReviewers,
    DeckMutationKind.RemoveLabels,
  ]);
  const removalCandidates = (kind: DeckMutationKind): DeckMutationCandidate[] => {
    if (kind === DeckMutationKind.UnassignUsers) {
      return pullRequest.assignees.map((value) => ({ kind: "user", value }));
    }
    if (kind === DeckMutationKind.RemoveReviewers) {
      return pullRequest.reviewers.map((reviewer) => ({ kind: reviewer.kind, value: reviewer.label }));
    }
    if (kind === DeckMutationKind.RemoveLabels) {
      return pullRequest.labels.map((value) => ({ kind: "label", value }));
    }
    return [];
  };
  const act = (kind: DeckMutationKind) => {
    if (addKinds.has(kind) || removalKinds.has(kind)) {
      setPicker(kind);
      return;
    }
    if (kind === DeckMutationKind.Merge) {
      setMergeOpen(true);
      return;
    }
    const input: DeckMutationInput =
      kind === DeckMutationKind.EnableAutoMerge
        ? { kind, mergeMethod }
        : { kind };
    void actions.mutate(pullRequest, input);
  };
  return (
    <div className="deck-pr-actions">
      <div className="button-row">
        {pullRequest.supportedMutations.map((kind) => (
          <button className="secondary-button" disabled={state.busy} key={kind} onClick={() => act(kind)} type="button">
            {kind.replaceAll("-", " ")}
          </button>
        ))}
        <button className="text-button" onClick={() => void actions.openOnGitHub(pullRequest)} type="button">
          Comment or review on GitHub
        </button>
      </div>
      {picker ? (
        <CandidatePicker
          fixedCandidates={removalCandidates(picker)}
          kind={picker}
          onApply={async (input, revision) => {
            const applied = await actions.mutate(
              revision === null ? pullRequest : { ...pullRequest, revision },
              input,
            );
            if (applied) setPicker(null);
            return applied;
          }}
          pullRequest={pullRequest}
          remoteSearch={addKinds.has(picker)}
        />
      ) : null}
      {mergeOpen ? (
        <Dialog title="Confirm pull request merge" onClose={() => setMergeOpen(false)}>
          <p>
            Merge <strong>{pullRequest.repositoryOwner}/{pullRequest.repositoryName}#{pullRequest.number}</strong>?
            This changes the repository and cannot be undone from Deck.
          </p>
          <label className="field">
            Merge method
            <select onChange={(event) => setMergeMethod(event.target.value as DeckMergeMethod)} value={mergeMethod}>
              {pullRequest.mergeMethods.map((method) => <option key={method} value={method}>{method}</option>)}
            </select>
          </label>
          <div className="button-row">
            <button
              className="danger-button"
              disabled={state.busy}
              onClick={() => void actions.mutate(pullRequest, {
                kind: DeckMutationKind.Merge,
                mergeMethod,
                mergeConfirmed: true,
              }).then((merged) => { if (merged) setMergeOpen(false); })}
              type="button"
            >
              Confirm merge
            </button>
            <button className="secondary-button" onClick={() => setMergeOpen(false)} type="button">Cancel</button>
          </div>
        </Dialog>
      ) : null}
    </div>
  );
}

export function DeckPullRequests() {
  const { actions, state } = useDeck();
  const grouped = useMemo(() => {
    const groups = new Map<string, DeckPullRequest[]>();
    for (const pullRequest of state.pullRequests) {
      const key = groupKey(pullRequest, state.selectedView?.grouping ?? DeckGrouping.None);
      groups.set(key, [...(groups.get(key) ?? []), pullRequest]);
    }
    return groups;
  }, [state.pullRequests, state.selectedView?.grouping]);
  if (!state.online) {
    return <section aria-labelledby="deck-offline-title" className="state-card error-card"><h2 id="deck-offline-title">Deck is offline</h2><p role="alert">Regular pull request results are hidden while the service is unavailable. Widget snapshots remain separate.</p></section>;
  }
  if (state.error !== null) {
    return <section className="state-card error-card"><h2>Pull requests unavailable</h2><p role="alert">{failureMessage(state.error)}</p><button className="secondary-button" onClick={() => void actions.retry()} type="button">Try again</button></section>;
  }
  if (state.selectedView === null) {
    return <section className="deck-empty-state"><h2>Select a view</h2><p>Choose a personal or organization view to load its permission-filtered pull requests.</p></section>;
  }
  if (state.freshness === DeckFreshness.Offline) {
    return <section className="state-card error-card"><h2>Deck service unavailable</h2><p role="alert">Regular pull request results are hidden until Deck is available again.</p><button className="secondary-button" onClick={() => void actions.retry()} type="button">Try again</button></section>;
  }
  if (state.freshness === DeckFreshness.Disconnected) {
    return <section className="state-card error-card"><h2>GitHub connection required</h2><p role="alert">Reconnect GitHub before loading pull requests.</p></section>;
  }
  if (state.selectedView.connection !== "connected") {
    return <section className="state-card error-card"><h2>GitHub connection required</h2><p role="alert">Reconnect GitHub before loading pull requests.</p></section>;
  }
  return (
    <section aria-labelledby="deck-results-title" className="deck-results">
      <div className="deck-section-heading">
        <div>
          <p className="eyebrow">Pull requests</p>
          <h2 id="deck-results-title">{state.selectedView.name}</h2>
        </div>
        <button className="secondary-button" disabled={state.busy} onClick={() => void actions.refresh()} type="button">
          Refresh
        </button>
      </div>
      {state.freshness === DeckFreshness.Stale ? <p className="status-banner warning" role="status">Results are stale because no eligible client refreshed this view recently.</p> : null}
      {state.truncated ? <p className="status-banner warning" role="status">Results are truncated at {state.resultLimit}. Narrow the query to see all matching pull requests.</p> : null}
      {state.loadingPullRequests ? <p role="status">Loading permission-filtered pull requests…</p> : null}
      {!state.loadingPullRequests && state.pullRequests.length === 0 ? <p className="muted">No accessible pull requests match this view.</p> : null}
      {[...grouped.entries()].map(([group, pullRequests]) => (
        <section aria-labelledby={`deck-group-${group.replaceAll(/[^a-z0-9]/giu, "-")}`} className="deck-pr-group" key={group}>
          <h3 id={`deck-group-${group.replaceAll(/[^a-z0-9]/giu, "-")}`}>{group}</h3>
          {pullRequests.map((pullRequest) => (
            <article className="deck-pr-card" key={`${pullRequest.repositoryOwner}/${pullRequest.repositoryName}#${pullRequest.number}`}>
              <div className="deck-pr-heading">
                <div>
                  <p className="muted">{pullRequest.repositoryOwner}/{pullRequest.repositoryName} #{pullRequest.number}</p>
                  <h4>{pullRequest.title}</h4>
                </div>
                <span className="status-chip">{pullRequest.lifecycle}{pullRequest.isDraft ? " · draft" : ""}</span>
              </div>
              <dl className="deck-pr-detail">
                <div><dt>Author</dt><dd>{pullRequest.author}</dd></div>
                <div><dt>Review</dt><dd>{pullRequest.reviewDecision}</dd></div>
                <div><dt>Checks</dt><dd>{pullRequest.checks}</dd></div>
                <div><dt>Merge</dt><dd>{pullRequest.mergeability}</dd></div>
                <div><dt>Reviewers</dt><dd>{pullRequest.reviewers.map((reviewer) => reviewer.label).join(", ") || "None"}</dd></div>
                <div><dt>Labels</dt><dd>{pullRequest.labels.join(", ") || "None"}</dd></div>
              </dl>
              <PullRequestActions pullRequest={pullRequest} />
            </article>
          ))}
        </section>
      ))}
      {state.pullRequestCursor ? <button className="secondary-button" onClick={() => void actions.loadMorePullRequests()} type="button">Load more pull requests</button> : null}
      {state.manualRefreshWarning ? (
        <Dialog title="Confirm billed refresh" onClose={() => actions.resolveManualRefresh(false)}>
          <p>{state.manualRefreshWarning.text}</p>
          <div className="button-row">
            <button className="danger-button" onClick={() => actions.resolveManualRefresh(true)} type="button">Refresh and accept charge</button>
            <button className="secondary-button" onClick={() => actions.resolveManualRefresh(false)} type="button">Cancel</button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}

export function DeckConflictDialog() {
  const { actions, state } = useDeck();
  const conflict = state.conflict;
  if (conflict === null) return null;
  return (
    <Dialog title="View changed elsewhere" onClose={actions.dismissConflict}>
      <p role="alert">The server has a newer revision. Review the values below before reapplying your edits.</p>
      <div className="deck-conflict-compare">
        <section><h3>Your edit</h3><p><strong>{conflict.attempted.name}</strong></p><code>{conflict.attempted.rawQuery}</code></section>
        <section><h3>Latest version</h3><p><strong>{conflict.current.name}</strong></p><code>{conflict.current.rawQuery}</code></section>
      </div>
      <div className="button-row">
        <button className="primary-button" onClick={() => void actions.reapplyConflict()} type="button">Reapply my changes</button>
        <button className="secondary-button" onClick={() => void actions.reloadConflict()} type="button">Reload latest</button>
        <button className="text-button" onClick={actions.dismissConflict} type="button">Cancel</button>
      </div>
    </Dialog>
  );
}

export function DeckWorkspace() {
  const { state } = useDeck();
  return (
    <div className="deck-workspace">
      <header className="deck-workspace-header">
        <div><p className="eyebrow">Closed internal tool</p><h1 id="deck-workspace-title" tabIndex={-1}>Deck</h1><p>Monitor and act on GitHub pull requests.</p></div>
        <span className="status-chip" role="status">{state.online ? "Online" : "Offline"}</span>
      </header>
      {state.loadingOwners ? <p role="status">Loading Deck ownership scopes…</p> : null}
      <DeckNavigation />
      <div className="deck-layout">
        <DeckViewManager
          key={`${state.selectedOwner?.kind ?? "none"}:${state.selectedOwner?.ownerId ?? "none"}:${state.selectedView?.viewId ?? "none"}`}
        />
        <DeckPullRequests />
      </div>
      <DeckConflictDialog />
    </div>
  );
}

export const Deck = {
  Workspace: DeckWorkspace,
  Navigation: DeckNavigation,
  ViewManager: DeckViewManager,
  PullRequests: DeckPullRequests,
  ConflictDialog: DeckConflictDialog,
};
