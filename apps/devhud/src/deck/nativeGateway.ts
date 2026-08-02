import { create } from "@bufbuild/protobuf";
import { invoke, isTauri } from "@tauri-apps/api/core";
import {
  AddLabelsMutationSchema,
  AssignUsersMutationSchema,
  BillingSelectionSchema,
  CancelAutoMergeMutationSchema,
  ChecksState,
  ClosePullRequestMutationSchema,
  CreateViewRequestSchema,
  CreateViewResponseSchema,
  DeleteViewRequestSchema,
  DeleteViewResponseSchema,
  EnableAutoMergeMutationSchema,
  FreshnessState,
  GetDeviceRequestSchema,
  GetDeviceResponseSchema,
  GetRefreshPreflightRequestSchema,
  GetRefreshPreflightResponseSchema,
  GetViewRequestSchema,
  GetViewResponseSchema,
  GitHubTeamSchema,
  GitHubUserSchema,
  IdempotencyKeySchema,
  ListPullRequestMutationCandidatesRequestSchema,
  ListPullRequestMutationCandidatesResponseSchema,
  ListOwnersRequestSchema,
  ListOwnersResponseSchema,
  ListPullRequestsRequestSchema,
  ListPullRequestsResponseSchema,
  ListViewsRequestSchema,
  ListViewsResponseSchema,
  MarkDraftMutationSchema,
  MarkReadyMutationSchema,
  MergeMethod,
  MergePullRequestMutationSchema,
  NotificationTransition,
  OwnerSchema,
  OwnerScope,
  PageRequestSchema,
  PullRequestLifecycleState,
  PullRequestMutationKind,
  PullRequestMutationSchema,
  PullRequestReferenceSchema,
  RefreshClientKind,
  RefreshOrigin,
  RefreshViewRequestSchema,
  RefreshViewResponseSchema,
  RemoveLabelsMutationSchema,
  RemoveReviewersMutationSchema,
  ReopenPullRequestMutationSchema,
  RepositoryReferenceSchema,
  RequestReviewersMutationSchema,
  ReviewDecision,
  RevisionSchema,
  ShortcutKey as ProtoShortcutKey,
  ShortcutModifier as ProtoShortcutModifier,
  ShortcutState,
  UnassignUsersMutationSchema,
  UpdateViewRequestSchema,
  UpdateViewResponseSchema,
  UuidV7Schema,
  ViewGrouping,
  ViewKind,
  ViewQuerySchema,
  ViewSort,
  type PullRequestResult,
  type Revision,
  type View,
} from "@delinoio/devhud-deck-connect";

import {
  ShortcutKey,
  ShortcutModifier,
  type StructuredShortcut,
} from "../persistence/contracts";

import {
  DeckFailureCode,
  DeckFreshness,
  DeckGrouping,
  DeckMergeMethod,
  DeckMutationKind,
  DeckNotificationTransition,
  DeckOwnerKind,
  DeckProductError,
  DeckPullRequestLifecycle,
  DeckSort,
  type DeckCursorPage,
  type DeckBillingSelection,
  type DeckGateway,
  type DeckMutationCandidate,
  type DeckMutationInput,
  type DeckOwner,
  type DeckPullRequest,
  type DeckPullRequestPage,
  type DeckReviewer,
  type DeckView,
  type DeckViewInput,
} from "./contracts";
import {
  DeckRefreshController,
  manualRefreshWarning,
  type DeckRefreshAttempt,
  type DeckRefreshAttemptStore,
} from "./refreshController";
import { DeckProcedure, invokeDeckProcedure, openDeckPullRequest } from "./transport";
import { createUuidV7 } from "./uuid";

const sortFromProto: Readonly<Record<number, DeckSort>> = {
  [ViewSort.RECENTLY_UPDATED]: DeckSort.RecentlyUpdated,
  [ViewSort.RECENTLY_CREATED]: DeckSort.RecentlyCreated,
  [ViewSort.ATTENTION]: DeckSort.Attention,
  [ViewSort.CHECKS]: DeckSort.Checks,
  [ViewSort.REVIEW_STATE]: DeckSort.ReviewState,
};
const sortToProto: Readonly<Record<DeckSort, ViewSort>> = {
  [DeckSort.RecentlyUpdated]: ViewSort.RECENTLY_UPDATED,
  [DeckSort.RecentlyCreated]: ViewSort.RECENTLY_CREATED,
  [DeckSort.Attention]: ViewSort.ATTENTION,
  [DeckSort.Checks]: ViewSort.CHECKS,
  [DeckSort.ReviewState]: ViewSort.REVIEW_STATE,
};
const groupingFromProto: Readonly<Record<number, DeckGrouping>> = {
  [ViewGrouping.NONE]: DeckGrouping.None,
  [ViewGrouping.REPOSITORY]: DeckGrouping.Repository,
  [ViewGrouping.AUTHOR]: DeckGrouping.Author,
  [ViewGrouping.REVIEWER]: DeckGrouping.Reviewer,
  [ViewGrouping.STATUS]: DeckGrouping.Status,
};
const groupingToProto: Readonly<Record<DeckGrouping, ViewGrouping>> = {
  [DeckGrouping.None]: ViewGrouping.NONE,
  [DeckGrouping.Repository]: ViewGrouping.REPOSITORY,
  [DeckGrouping.Author]: ViewGrouping.AUTHOR,
  [DeckGrouping.Reviewer]: ViewGrouping.REVIEWER,
  [DeckGrouping.Status]: ViewGrouping.STATUS,
};
const mutationFromProto: Readonly<Record<number, DeckMutationKind>> = {
  [PullRequestMutationKind.ASSIGN_USERS]: DeckMutationKind.AssignUsers,
  [PullRequestMutationKind.UNASSIGN_USERS]: DeckMutationKind.UnassignUsers,
  [PullRequestMutationKind.REQUEST_REVIEWERS]: DeckMutationKind.RequestReviewers,
  [PullRequestMutationKind.REMOVE_REVIEWERS]: DeckMutationKind.RemoveReviewers,
  [PullRequestMutationKind.ADD_LABELS]: DeckMutationKind.AddLabels,
  [PullRequestMutationKind.REMOVE_LABELS]: DeckMutationKind.RemoveLabels,
  [PullRequestMutationKind.MARK_DRAFT]: DeckMutationKind.MarkDraft,
  [PullRequestMutationKind.MARK_READY]: DeckMutationKind.MarkReady,
  [PullRequestMutationKind.CLOSE]: DeckMutationKind.Close,
  [PullRequestMutationKind.REOPEN]: DeckMutationKind.Reopen,
  [PullRequestMutationKind.MERGE]: DeckMutationKind.Merge,
  [PullRequestMutationKind.ENABLE_AUTO_MERGE]: DeckMutationKind.EnableAutoMerge,
  [PullRequestMutationKind.CANCEL_AUTO_MERGE]: DeckMutationKind.CancelAutoMerge,
};
const mutationToProto: Readonly<Record<DeckMutationKind, PullRequestMutationKind>> =
  Object.fromEntries(Object.entries(mutationFromProto).map(([key, value]) => [value, Number(key)])) as Readonly<Record<DeckMutationKind, PullRequestMutationKind>>;
const mergeFromProto: Readonly<Record<number, DeckMergeMethod>> = {
  [MergeMethod.MERGE]: DeckMergeMethod.Merge,
  [MergeMethod.SQUASH]: DeckMergeMethod.Squash,
  [MergeMethod.REBASE]: DeckMergeMethod.Rebase,
};
const mergeToProto: Readonly<Record<DeckMergeMethod, MergeMethod>> = {
  [DeckMergeMethod.Merge]: MergeMethod.MERGE,
  [DeckMergeMethod.Squash]: MergeMethod.SQUASH,
  [DeckMergeMethod.Rebase]: MergeMethod.REBASE,
};
const notificationFromProto: Readonly<Record<number, DeckNotificationTransition>> = {
  [NotificationTransition.ASSIGNED]: DeckNotificationTransition.Assigned,
  [NotificationTransition.REVIEW_REQUESTED]: DeckNotificationTransition.ReviewRequested,
  [NotificationTransition.CHECKS_FAILED]: DeckNotificationTransition.ChecksFailed,
  [NotificationTransition.BECAME_MERGEABLE]: DeckNotificationTransition.BecameMergeable,
  [NotificationTransition.CONFLICTED]: DeckNotificationTransition.Conflicted,
  [NotificationTransition.MERGED]: DeckNotificationTransition.Merged,
  [NotificationTransition.CLOSED]: DeckNotificationTransition.Closed,
};
const notificationToProto: Readonly<Record<DeckNotificationTransition, NotificationTransition>> =
  Object.fromEntries(Object.entries(notificationFromProto).map(([key, value]) => [value, Number(key)])) as Readonly<Record<DeckNotificationTransition, NotificationTransition>>;

function uuid(value: string) {
  return create(UuidV7Schema, { value });
}
function revision(value: Revision | undefined) {
  return { value: value?.value ?? 0n, etag: value?.etag ?? "" };
}
function idempotencyKey(value: string) {
  return create(IdempotencyKeySchema, { value: uuid(value) });
}
function ownerMessage(owner: DeckOwner) {
  return create(OwnerSchema, {
    scope: owner.kind === DeckOwnerKind.Personal ? OwnerScope.PERSONAL : OwnerScope.ORGANIZATION,
    ownerId: owner.kind === DeckOwnerKind.Personal
      ? { case: "accountId", value: uuid(owner.ownerId) }
      : { case: "organizationId", value: uuid(owner.ownerId) },
  });
}
function billingMessage(billing: DeckBillingSelection) {
  return create(BillingSelectionSchema, {
    organizationId: uuid(billing.organizationId),
    teamId: uuid(billing.teamId),
  });
}
function billingFromProto(
  billing: { organizationId?: { value: string }; teamId?: { value: string } } | undefined,
): DeckBillingSelection {
  return {
    organizationId: billing?.organizationId?.value ?? "",
    teamId: billing?.teamId?.value ?? "",
  };
}
function ownerFromView(view: View): DeckOwner {
  const organization = view.owner?.ownerId.case === "organizationId";
  const ownerId = view.owner?.ownerId.value?.value ?? "";
  return {
    ownerId,
    kind: organization ? DeckOwnerKind.Organization : DeckOwnerKind.Personal,
    label: organization ? "Organization" : "Personal",
    canManage: true,
    billingSelections: view.billing === undefined ? [] : [billingFromProto(view.billing)],
  };
}
function timestamp(value: { seconds: bigint; nanos: number } | undefined): string | undefined {
  if (value === undefined) return undefined;
  return new Date(Number(value.seconds) * 1000 + value.nanos / 1_000_000).toISOString();
}
function mapView(view: View): DeckView {
  return {
    viewId: view.viewId?.value ?? "",
    owner: ownerFromView(view),
    billing: billingFromProto(view.billing),
    name: view.name,
    rawQuery: view.query?.rawQuery ?? "",
    sort: sortFromProto[view.sort] ?? DeckSort.RecentlyUpdated,
    grouping: groupingFromProto[view.grouping] ?? DeckGrouping.None,
    notificationPreference: {
      enabled: view.notificationPreference?.enabled ?? false,
      transitions: (view.notificationPreference?.transitions ?? []).flatMap((transition) =>
        notificationFromProto[transition] ? [notificationFromProto[transition]!] : []
      ),
    },
    connection: view.connectionState === 3 ? "connected" : view.connectionState === 4 ? "reauthentication-required" : "disconnected",
    revision: revision(view.revision),
    widgetAttached: false,
  };
}
function mapPullRequest(value: PullRequestResult): DeckPullRequest {
  const reviewDecision = value.reviewDecision === ReviewDecision.APPROVED ? "approved"
    : value.reviewDecision === ReviewDecision.CHANGES_REQUESTED ? "changes-requested"
      : value.reviewDecision === ReviewDecision.REVIEW_REQUIRED ? "required" : "none";
  const checks = value.checks?.state === ChecksState.SUCCESS ? "success"
    : value.checks?.state === ChecksState.FAILURE ? "failure"
      : value.checks?.state === ChecksState.PENDING ? "pending" : "none";
  const lifecycle = value.lifecycleState === PullRequestLifecycleState.MERGED ? DeckPullRequestLifecycle.Merged
    : value.lifecycleState === PullRequestLifecycleState.CLOSED ? DeckPullRequestLifecycle.Closed
      : DeckPullRequestLifecycle.Open;
  return {
    repositoryOwner: value.repository?.owner ?? "",
    repositoryName: value.repository?.name ?? "",
    number: Number(value.number),
    title: value.title,
    author: value.author?.login ?? "",
    reviewDecision,
    checks,
    mergeability: value.mergeability === 2 ? "mergeable" : value.mergeability === 3 ? "conflicting" : value.mergeability === 4 ? "blocked" : "unknown",
    isDraft: value.isDraft,
    lifecycle,
    updatedAt: timestamp(value.updatedAt) ?? "",
    revision: revision(value.revision),
    reviewers: value.reviewers.flatMap<DeckReviewer>((reviewer) => {
      if (reviewer.reviewer.case === "user") return [{ kind: "user" as const, label: reviewer.reviewer.value.login }];
      if (reviewer.reviewer.case === "team") return [{ kind: "team" as const, label: `${reviewer.reviewer.value.organization}/${reviewer.reviewer.value.slug}` }];
      return [];
    }),
    assignees: value.assignees.map((user) => user.login),
    labels: value.labels,
    supportedMutations: value.supportedMutations.flatMap((kind) => mutationFromProto[kind] ? [mutationFromProto[kind]!] : []),
    mergeMethods: value.availableMergeMethods.flatMap((method) => mergeFromProto[method] ? [mergeFromProto[method]!] : []),
  };
}
function viewInput(input: DeckViewInput) {
  return {
    billing: billingMessage(input.billing),
    name: input.name,
    query: create(ViewQuerySchema, { rawQuery: input.rawQuery }),
    sort: sortToProto[input.sort],
    grouping: groupingToProto[input.grouping],
    notificationPreference: {
      enabled: input.notificationPreference.enabled,
      transitions: input.notificationPreference.transitions.map((transition) => notificationToProto[transition]),
    },
  };
}

const shortcutModifiers: Readonly<Partial<Record<ProtoShortcutModifier, ShortcutModifier>>> = {
  [ProtoShortcutModifier.CONTROL]: ShortcutModifier.Control,
  [ProtoShortcutModifier.ALT]: ShortcutModifier.Alt,
  [ProtoShortcutModifier.SHIFT]: ShortcutModifier.Shift,
  [ProtoShortcutModifier.META]: ShortcutModifier.Meta,
};
const shortcutKeys: Readonly<Partial<Record<ProtoShortcutKey, ShortcutKey>>> = {
  [ProtoShortcutKey.A]: ShortcutKey.A,
  [ProtoShortcutKey.B]: ShortcutKey.B,
  [ProtoShortcutKey.C]: ShortcutKey.C,
  [ProtoShortcutKey.D]: ShortcutKey.D,
  [ProtoShortcutKey.E]: ShortcutKey.E,
  [ProtoShortcutKey.F]: ShortcutKey.F,
  [ProtoShortcutKey.G]: ShortcutKey.G,
  [ProtoShortcutKey.H]: ShortcutKey.H,
  [ProtoShortcutKey.I]: ShortcutKey.I,
  [ProtoShortcutKey.J]: ShortcutKey.J,
  [ProtoShortcutKey.K]: ShortcutKey.K,
  [ProtoShortcutKey.L]: ShortcutKey.L,
  [ProtoShortcutKey.M]: ShortcutKey.M,
  [ProtoShortcutKey.N]: ShortcutKey.N,
  [ProtoShortcutKey.O]: ShortcutKey.O,
  [ProtoShortcutKey.P]: ShortcutKey.P,
  [ProtoShortcutKey.Q]: ShortcutKey.Q,
  [ProtoShortcutKey.R]: ShortcutKey.R,
  [ProtoShortcutKey.S]: ShortcutKey.S,
  [ProtoShortcutKey.T]: ShortcutKey.T,
  [ProtoShortcutKey.U]: ShortcutKey.U,
  [ProtoShortcutKey.V]: ShortcutKey.V,
  [ProtoShortcutKey.W]: ShortcutKey.W,
  [ProtoShortcutKey.X]: ShortcutKey.X,
  [ProtoShortcutKey.Y]: ShortcutKey.Y,
  [ProtoShortcutKey.Z]: ShortcutKey.Z,
  [ProtoShortcutKey.DIGIT_0]: ShortcutKey.Digit0,
  [ProtoShortcutKey.DIGIT_1]: ShortcutKey.Digit1,
  [ProtoShortcutKey.DIGIT_2]: ShortcutKey.Digit2,
  [ProtoShortcutKey.DIGIT_3]: ShortcutKey.Digit3,
  [ProtoShortcutKey.DIGIT_4]: ShortcutKey.Digit4,
  [ProtoShortcutKey.DIGIT_5]: ShortcutKey.Digit5,
  [ProtoShortcutKey.DIGIT_6]: ShortcutKey.Digit6,
  [ProtoShortcutKey.DIGIT_7]: ShortcutKey.Digit7,
  [ProtoShortcutKey.DIGIT_8]: ShortcutKey.Digit8,
  [ProtoShortcutKey.DIGIT_9]: ShortcutKey.Digit9,
  [ProtoShortcutKey.F1]: ShortcutKey.F1,
  [ProtoShortcutKey.F2]: ShortcutKey.F2,
  [ProtoShortcutKey.F3]: ShortcutKey.F3,
  [ProtoShortcutKey.F4]: ShortcutKey.F4,
  [ProtoShortcutKey.F5]: ShortcutKey.F5,
  [ProtoShortcutKey.F6]: ShortcutKey.F6,
  [ProtoShortcutKey.F7]: ShortcutKey.F7,
  [ProtoShortcutKey.F8]: ShortcutKey.F8,
  [ProtoShortcutKey.F9]: ShortcutKey.F9,
  [ProtoShortcutKey.F10]: ShortcutKey.F10,
  [ProtoShortcutKey.F11]: ShortcutKey.F11,
  [ProtoShortcutKey.F12]: ShortcutKey.F12,
  [ProtoShortcutKey.SPACE]: ShortcutKey.Space,
  [ProtoShortcutKey.ENTER]: ShortcutKey.Enter,
};
function shortcutFromProto(
  binding: { modifiers: readonly ProtoShortcutModifier[]; key: ProtoShortcutKey } | undefined,
): StructuredShortcut | undefined {
  if (binding === undefined) return undefined;
  const modifiers = binding.modifiers.flatMap((modifier) => {
    const mapped = shortcutModifiers[modifier];
    return mapped === undefined ? [] : [mapped];
  });
  const key = shortcutKeys[binding.key];
  return key === undefined || modifiers.length !== binding.modifiers.length
    ? undefined
    : { modifiers, key };
}
function reference(pullRequest: DeckPullRequest) {
  return create(PullRequestReferenceSchema, {
    repository: create(RepositoryReferenceSchema, {
      owner: pullRequest.repositoryOwner,
      name: pullRequest.repositoryName,
    }),
    number: BigInt(pullRequest.number),
  });
}
function users(values: readonly string[] | undefined) {
  return (values ?? []).map((login) => create(GitHubUserSchema, { login }));
}
function teams(values: readonly string[] | undefined) {
  return (values ?? []).map((value) => {
    const [organization = "", slug = ""] = value.split("/", 2);
    return create(GitHubTeamSchema, { organization, slug });
  });
}
function mutation(input: DeckMutationInput) {
  const value = (() => {
    switch (input.kind) {
      case DeckMutationKind.AssignUsers: return { case: "assignUsers" as const, value: create(AssignUsersMutationSchema, { users: users(input.users) }) };
      case DeckMutationKind.UnassignUsers: return { case: "unassignUsers" as const, value: create(UnassignUsersMutationSchema, { users: users(input.users) }) };
      case DeckMutationKind.RequestReviewers: return { case: "requestReviewers" as const, value: create(RequestReviewersMutationSchema, { users: users(input.users), teams: teams(input.teams) }) };
      case DeckMutationKind.RemoveReviewers: return { case: "removeReviewers" as const, value: create(RemoveReviewersMutationSchema, { users: users(input.users), teams: teams(input.teams) }) };
      case DeckMutationKind.AddLabels: return { case: "addLabels" as const, value: create(AddLabelsMutationSchema, { labels: [...(input.labels ?? [])] }) };
      case DeckMutationKind.RemoveLabels: return { case: "removeLabels" as const, value: create(RemoveLabelsMutationSchema, { labels: [...(input.labels ?? [])] }) };
      case DeckMutationKind.MarkDraft: return { case: "markDraft" as const, value: create(MarkDraftMutationSchema) };
      case DeckMutationKind.MarkReady: return { case: "markReady" as const, value: create(MarkReadyMutationSchema) };
      case DeckMutationKind.Close: return { case: "close" as const, value: create(ClosePullRequestMutationSchema) };
      case DeckMutationKind.Reopen: return { case: "reopen" as const, value: create(ReopenPullRequestMutationSchema) };
      case DeckMutationKind.Merge:
        if (input.mergeConfirmed !== true) throw new DeckProductError(DeckFailureCode.UnsupportedAction);
        return { case: "merge" as const, value: create(MergePullRequestMutationSchema, { method: mergeToProto[input.mergeMethod ?? DeckMergeMethod.Merge], confirmed: true }) };
      case DeckMutationKind.EnableAutoMerge: return { case: "enableAutoMerge" as const, value: create(EnableAutoMergeMutationSchema, { method: mergeToProto[input.mergeMethod ?? DeckMergeMethod.Merge] }) };
      case DeckMutationKind.CancelAutoMerge: return { case: "cancelAutoMerge" as const, value: create(CancelAutoMergeMutationSchema) };
    }
  })();
  return create(PullRequestMutationSchema, { mutation: value });
}

function mapFailure(error: unknown): never {
  const value = (typeof error === "string"
    ? error
    : error instanceof Error
      ? `${error.name} ${error.message}`
      : JSON.stringify(error) ?? String(error)).toLowerCase();
  if (value.includes("offline")) throw new DeckProductError(DeckFailureCode.Offline);
  if (value.includes("reauthentication") || value.includes("authentication")) {
    throw new DeckProductError(DeckFailureCode.AuthenticationRequired);
  }
  if (value.includes("permission")) throw new DeckProductError(DeckFailureCode.PermissionDenied);
  if (value.includes("conflict") || value.includes("stale")) throw new DeckProductError(DeckFailureCode.StaleRevision);
  if (value.includes("rate")) throw new DeckProductError(DeckFailureCode.RateLimited);
  if (value.includes("billing")) throw new DeckProductError(DeckFailureCode.BillingUnavailable);
  if (value.includes("provider")) throw new DeckProductError(DeckFailureCode.ProviderUnavailable);
  throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
}

export class NativeDeckGateway implements DeckGateway {
  readonly #manualAttempts = new Map<string, DeckRefreshAttempt>();
  readonly #mutationAttempts = new Map<string, DeckRefreshAttempt>();
  readonly #automaticAttempts = new MemoryRefreshAttemptStore();
  readonly #lastOpenedAt = new Map<string, Date>();
  readonly #shortcutViewIds = new Set<string>();
  #accountId = "";
  #deviceId = "";

  readonly #clientKind: RefreshClientKind.DESKTOP | RefreshClientKind.MOBILE;

  constructor(
    clientKind: RefreshClientKind.DESKTOP | RefreshClientKind.MOBILE = RefreshClientKind.DESKTOP,
  ) {
    this.#clientKind = clientKind;
  }

  async #call<T>(operation: () => Promise<T>): Promise<T> {
    try { return await operation(); } catch (error) { mapFailure(error); }
  }

  async listOwners(): Promise<readonly DeckOwner[]> {
    const response = await this.#call(() => invokeDeckProcedure(
      DeckProcedure.ListOwners,
      ListOwnersRequestSchema,
      ListOwnersResponseSchema,
      create(ListOwnersRequestSchema),
    ));
    const owners = response.owners.flatMap<DeckOwner>((access) => {
      const organization = access.owner?.ownerId.case === "organizationId";
      const ownerId = access.owner?.ownerId.value?.value ?? "";
      if (ownerId.length === 0) return [];
      return [{
        ownerId,
        kind: organization ? DeckOwnerKind.Organization : DeckOwnerKind.Personal,
        label: organization ? `Organization ${ownerId}` : "Personal",
        canManage: access.canManage,
        billingSelections: access.billingSelections.map(billingFromProto),
      }];
    });
    this.#accountId = owners.find((owner) => owner.kind === DeckOwnerKind.Personal)?.ownerId ?? "";
    return owners;
  }
  async listViews(owner: DeckOwner, cursor: string): Promise<DeckCursorPage<DeckView>> {
    const response = await this.#call(() => invokeDeckProcedure(
      DeckProcedure.ListViews,
      ListViewsRequestSchema,
      ListViewsResponseSchema,
      create(ListViewsRequestSchema, { owner: ownerMessage(owner), page: create(PageRequestSchema, { cursor, pageSize: 50 }) }),
    ));
    return { items: response.views.map(mapView), nextCursor: response.page?.nextCursor ?? "" };
  }
  async getView(viewId: string): Promise<DeckView> {
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.GetView, GetViewRequestSchema, GetViewResponseSchema, create(GetViewRequestSchema, { viewId: uuid(viewId) })));
    return mapView(response.view!);
  }
  async createView(owner: DeckOwner, input: DeckViewInput, key: string): Promise<DeckView> {
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.CreateView, CreateViewRequestSchema, CreateViewResponseSchema, create(CreateViewRequestSchema, {
      idempotencyKey: idempotencyKey(key),
      view: { owner: ownerMessage(owner), kind: ViewKind.GITHUB_PULL_REQUESTS, ...viewInput(input) },
    })));
    return mapView(response.view!);
  }
  async updateView(view: DeckView, input: DeckViewInput): Promise<DeckView> {
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.UpdateView, UpdateViewRequestSchema, UpdateViewResponseSchema, create(UpdateViewRequestSchema, {
      viewId: uuid(view.viewId), expectedRevision: create(RevisionSchema, view.revision), view: viewInput(input),
    })));
    return mapView(response.view!);
  }
  async deleteView(view: DeckView): Promise<void> {
    await this.#call(() => invokeDeckProcedure(DeckProcedure.DeleteView, DeleteViewRequestSchema, DeleteViewResponseSchema, create(DeleteViewRequestSchema, { viewId: uuid(view.viewId), expectedRevision: create(RevisionSchema, view.revision) })));
  }
  async listPullRequests(viewId: string, cursor: string): Promise<DeckPullRequestPage> {
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.ListPullRequests, ListPullRequestsRequestSchema, ListPullRequestsResponseSchema, create(ListPullRequestsRequestSchema, { viewId: uuid(viewId), page: create(PageRequestSchema, { cursor, pageSize: 50 }) })));
    const freshness = response.freshness === FreshnessState.FRESH ? DeckFreshness.Fresh : response.freshness === FreshnessState.STALE ? DeckFreshness.Stale : response.freshness === FreshnessState.DISCONNECTED ? DeckFreshness.Disconnected : response.freshness === FreshnessState.NEVER_REFRESHED ? DeckFreshness.NeverRefreshed : DeckFreshness.Offline;
    return { items: response.pullRequests.map(mapPullRequest), nextCursor: response.page?.nextCursor ?? "", freshness, refreshedAt: timestamp(response.refreshedAt), truncated: response.truncated, resultLimit: response.resultLimit };
  }
  async listMutationCandidates(viewId: string, pullRequest: DeckPullRequest, kind: DeckMutationKind, query: string, cursor: string): Promise<DeckCursorPage<DeckMutationCandidate>> {
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.ListPullRequestMutationCandidates, ListPullRequestMutationCandidatesRequestSchema, ListPullRequestMutationCandidatesResponseSchema, create(ListPullRequestMutationCandidatesRequestSchema, { viewId: uuid(viewId), pullRequest: reference(pullRequest), mutationKind: mutationToProto[kind], query, page: create(PageRequestSchema, { cursor, pageSize: 25 }) })));
    return { items: response.candidates.flatMap<DeckMutationCandidate>((candidate) => candidate.candidate.case === "user" ? [{ kind: "user", value: candidate.candidate.value.login }] : candidate.candidate.case === "team" ? [{ kind: "team", value: `${candidate.candidate.value.organization}/${candidate.candidate.value.slug}` }] : candidate.candidate.case === "label" ? [{ kind: "label", value: candidate.candidate.value }] : []), nextCursor: response.page?.nextCursor ?? "" };
  }
  async mutatePullRequest(viewId: string, pullRequest: DeckPullRequest, input: DeckMutationInput) {
    const { MutatePullRequestRequestSchema, MutatePullRequestResponseSchema } = await import("@delinoio/devhud-deck-connect");
    const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.MutatePullRequest, MutatePullRequestRequestSchema, MutatePullRequestResponseSchema, create(MutatePullRequestRequestSchema, { viewId: uuid(viewId), pullRequest: reference(pullRequest), expectedRevision: create(RevisionSchema, pullRequest.revision), mutation: mutation(input) })));
    return { pullRequest: response.pullRequest?.result ? mapPullRequest(response.pullRequest.result) : undefined, refreshRequired: response.refreshRequired };
  }
  async refreshAfterMutation(viewId: string): Promise<void> {
    let attempt = this.#mutationAttempts.get(viewId);
    if (attempt === undefined) {
      const requestId = createUuidV7();
      const response = await this.#call(() => invokeDeckProcedure(
        DeckProcedure.GetRefreshPreflight,
        GetRefreshPreflightRequestSchema,
        GetRefreshPreflightResponseSchema,
        create(GetRefreshPreflightRequestSchema, {
          viewId: uuid(viewId),
          refreshRequestId: idempotencyKey(requestId),
          origin: RefreshOrigin.VIEW_OPEN,
          clientKind: this.#clientKind,
        }),
      ));
      attempt = {
        request: { viewId, requestId, origin: RefreshOrigin.VIEW_OPEN, clientKind: this.#clientKind },
        preflightToken: response.preflightToken,
      };
      this.#mutationAttempts.set(viewId, attempt);
    }
    try {
      await this.#call(() => invokeDeckProcedure(
        DeckProcedure.RefreshView,
        RefreshViewRequestSchema,
        RefreshViewResponseSchema,
        create(RefreshViewRequestSchema, {
          viewId: uuid(viewId),
          refreshRequestId: idempotencyKey(attempt!.request.requestId),
          origin: attempt!.request.origin,
          billingPreflightToken: attempt!.preflightToken,
          clientKind: attempt!.request.clientKind,
        }),
      ));
      this.#mutationAttempts.delete(viewId);
    } catch (error) {
      if (!(error instanceof DeckProductError) || error.code !== DeckFailureCode.ServiceUnavailable) {
        this.#mutationAttempts.delete(viewId);
      }
      throw error;
    }
  }
  async refreshView(viewId: string, confirm: (warning: ReturnType<typeof manualRefreshWarning>) => Promise<boolean>): Promise<void> {
    let attempt = this.#manualAttempts.get(viewId);
    if (attempt === undefined) {
      const requestId = createUuidV7();
      const response = await this.#call(() => invokeDeckProcedure(DeckProcedure.GetRefreshPreflight, GetRefreshPreflightRequestSchema, GetRefreshPreflightResponseSchema, create(GetRefreshPreflightRequestSchema, { viewId: uuid(viewId), refreshRequestId: idempotencyKey(requestId), origin: RefreshOrigin.MANUAL, clientKind: this.#clientKind })));
      if (!await confirm(manualRefreshWarning(response.providerRefreshPrice?.value ?? 0n))) return;
      attempt = { request: { viewId, requestId, origin: RefreshOrigin.MANUAL, clientKind: this.#clientKind }, preflightToken: response.preflightToken };
      this.#manualAttempts.set(viewId, attempt);
    }
    try {
      await this.#call(() => invokeDeckProcedure(DeckProcedure.RefreshView, RefreshViewRequestSchema, RefreshViewResponseSchema, create(RefreshViewRequestSchema, { viewId: uuid(viewId), refreshRequestId: idempotencyKey(attempt!.request.requestId), origin: RefreshOrigin.MANUAL, billingPreflightToken: attempt!.preflightToken, clientKind: this.#clientKind })));
      this.#manualAttempts.delete(viewId);
    } catch (error) {
      if (!(error instanceof DeckProductError) || error.code !== DeckFailureCode.ServiceUnavailable) this.#manualAttempts.delete(viewId);
      throw error;
    }
  }
  openPullRequest(pullRequest: DeckPullRequest): Promise<void> { return openDeckPullRequest(pullRequest.repositoryOwner, pullRequest.repositoryName, pullRequest.number); }
  recordViewOpened(viewId: string): void { this.#lastOpenedAt.set(viewId, new Date()); }
  async synchronizeShortcuts(views: readonly DeckView[]): Promise<void> {
    if (!isTauri() || this.#clientKind !== RefreshClientKind.DESKTOP) return;
    if (views.length === 0) {
      this.#shortcutViewIds.clear();
      await invoke("synchronize_deck_shortcuts", { accountId: this.#accountId, definitions: [] });
      return;
    }
    if (this.#deviceId.length === 0) this.#deviceId = await invoke<string>("deck_device_id");
    const response = await this.#call(() => invokeDeckProcedure(
      DeckProcedure.GetDevice,
      GetDeviceRequestSchema,
      GetDeviceResponseSchema,
      create(GetDeviceRequestSchema, { deviceId: uuid(this.#deviceId) }),
    ));
    const definitions = (response.registration?.device?.shortcuts ?? []).flatMap((shortcut) => {
      if (shortcut.state !== ShortcutState.ACTIVE) return [];
      const mapped = shortcutFromProto(shortcut.binding);
      const viewId = shortcut.viewId?.value ?? "";
      return mapped === undefined || viewId.length === 0 ? [] : [{
        accountId: this.#accountId,
        viewId,
        shortcut: mapped,
      }];
    });
    this.#shortcutViewIds.clear();
    definitions.forEach((definition) => this.#shortcutViewIds.add(definition.viewId));
    await invoke("synchronize_deck_shortcuts", {
      accountId: this.#accountId,
      definitions,
    });
  }

  startEligibleRefreshes(views: readonly DeckView[]): () => void {
    if (views.length === 0) return () => undefined;
    const controller = new DeckRefreshController({
      clientKind: this.#clientKind,
      transport: {
        isAmbiguousRefreshError: (error) =>
          error instanceof DeckProductError && error.code === DeckFailureCode.ServiceUnavailable,
        getPreflight: async (request, signal) => {
          signal.throwIfAborted();
          const response = await this.#call(() => invokeDeckProcedure(
            DeckProcedure.GetRefreshPreflight,
            GetRefreshPreflightRequestSchema,
            GetRefreshPreflightResponseSchema,
            create(GetRefreshPreflightRequestSchema, {
              viewId: uuid(request.viewId),
              refreshRequestId: idempotencyKey(request.requestId),
              origin: request.origin,
              clientKind: request.clientKind,
            }),
          ));
          signal.throwIfAborted();
          return {
            priceUsdMicros: response.providerRefreshPrice?.value ?? 0n,
            token: response.preflightToken,
          };
        },
        refresh: async (request, signal) => {
          signal.throwIfAborted();
          await this.#call(() => invokeDeckProcedure(
            DeckProcedure.RefreshView,
            RefreshViewRequestSchema,
            RefreshViewResponseSchema,
            create(RefreshViewRequestSchema, {
              viewId: uuid(request.viewId),
              refreshRequestId: idempotencyKey(request.requestId),
              origin: request.origin,
              billingPreflightToken: request.preflightToken,
              clientKind: request.clientKind,
            }),
          ));
          signal.throwIfAborted();
        },
      },
      createRequestId: createUuidV7,
      listCandidates: async () => views.map((view) => ({
        viewId: view.viewId,
        lastOpenedAt: this.#lastOpenedAt.get(view.viewId),
        notificationAttached: view.notificationPreference.enabled,
        shortcutAttached: this.#shortcutViewIds.has(view.viewId),
        widgetAttached: view.widgetAttached,
      })),
      canPoll: () => navigator.onLine,
      automaticAttempts: this.#automaticAttempts,
      manualAttempts: new MemoryRefreshAttemptStore(),
    });
    controller.start();
    return () => controller.stop();
  }
}

class MemoryRefreshAttemptStore implements DeckRefreshAttemptStore {
  readonly #attempts = new Map<string, DeckRefreshAttempt>();

  get(viewId: string): DeckRefreshAttempt | undefined {
    return this.#attempts.get(viewId);
  }

  claim(viewId: string, attempt: DeckRefreshAttempt): DeckRefreshAttempt {
    const existing = this.#attempts.get(viewId);
    if (existing !== undefined) return existing;
    this.#attempts.set(viewId, attempt);
    return attempt;
  }

  deleteIfMatches(viewId: string, attempt: DeckRefreshAttempt): void {
    if (this.#attempts.get(viewId) === attempt) this.#attempts.delete(viewId);
  }
}
