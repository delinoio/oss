import type { StructuredShortcut } from "../persistence/contracts";
import type { ManualRefreshWarning } from "./refreshController";

export enum DeckOwnerKind {
  Personal = "personal",
  Organization = "organization",
}

export enum DeckSort {
  RecentlyUpdated = "recently-updated",
  RecentlyCreated = "recently-created",
  Attention = "attention",
  Checks = "checks",
  ReviewState = "review-state",
}

export enum DeckGrouping {
  None = "none",
  Repository = "repository",
  Author = "author",
  Reviewer = "reviewer",
  Status = "status",
}

export enum DeckFreshness {
  Fresh = "fresh",
  Stale = "stale",
  Offline = "offline",
  Disconnected = "disconnected",
  NeverRefreshed = "never-refreshed",
}

export enum DeckPullRequestLifecycle {
  Open = "open",
  Closed = "closed",
  Merged = "merged",
}

export enum DeckMutationKind {
  AssignUsers = "assign-users",
  UnassignUsers = "unassign-users",
  RequestReviewers = "request-reviewers",
  RemoveReviewers = "remove-reviewers",
  AddLabels = "add-labels",
  RemoveLabels = "remove-labels",
  MarkDraft = "mark-draft",
  MarkReady = "mark-ready",
  Close = "close",
  Reopen = "reopen",
  Merge = "merge",
  EnableAutoMerge = "enable-auto-merge",
  CancelAutoMerge = "cancel-auto-merge",
}

export enum DeckMergeMethod {
  Merge = "merge",
  Squash = "squash",
  Rebase = "rebase",
}

export enum DeckFailureCode {
  AuthenticationRequired = "authentication-required",
  PermissionDenied = "permission-denied",
  GitHubPermissionDenied = "github-permission-denied",
  StaleRevision = "stale-revision",
  Offline = "offline",
  ServiceUnavailable = "service-unavailable",
  Disconnected = "disconnected",
  RateLimited = "rate-limited",
  ProviderRateLimited = "provider-rate-limited",
  ProviderUnavailable = "provider-unavailable",
  BillingPreflightRejected = "billing-preflight-rejected",
  BillingUnavailable = "billing-unavailable",
  BrowserUnavailable = "browser-unavailable",
  BranchProtectionBlocked = "branch-protection-blocked",
  MergeConfirmationRequired = "merge-confirmation-required",
  UnsupportedAction = "unsupported-action",
}

export class DeckProductError extends Error {
  constructor(
    readonly code: DeckFailureCode,
    readonly retryAfterSeconds?: number,
  ) {
    super(code);
    this.name = "DeckProductError";
  }
}

export const deckFailureGuidance: Readonly<Record<DeckFailureCode, string>> = {
  [DeckFailureCode.AuthenticationRequired]: "Sign in to load Deck.",
  [DeckFailureCode.PermissionDenied]: "You do not have permission to manage this view.",
  [DeckFailureCode.GitHubPermissionDenied]:
    "GitHub no longer permits this operation or repository. Reconnect or choose another action.",
  [DeckFailureCode.StaleRevision]:
    "This item changed elsewhere. Compare the latest version and reapply your changes.",
  [DeckFailureCode.Offline]:
    "Deck is offline. Regular pull request results are hidden until the service is available.",
  [DeckFailureCode.ServiceUnavailable]:
    "Deck is temporarily unavailable. Regular pull request results are hidden.",
  [DeckFailureCode.Disconnected]: "Reconnect GitHub to refresh this view.",
  [DeckFailureCode.RateLimited]: "Deck is rate limited. Wait before trying again.",
  [DeckFailureCode.ProviderRateLimited]: "GitHub is rate limited. Try again after the displayed retry time.",
  [DeckFailureCode.ProviderUnavailable]: "GitHub did not complete the request. Try again later.",
  [DeckFailureCode.BillingPreflightRejected]:
    "The refresh confirmation expired or no longer matches this request. Start a new refresh.",
  [DeckFailureCode.BillingUnavailable]: "Refresh billing is unavailable, so GitHub was not contacted.",
  [DeckFailureCode.BrowserUnavailable]: "DevHud could not open GitHub in the system browser. Try again.",
  [DeckFailureCode.BranchProtectionBlocked]: "Repository rules currently block this action.",
  [DeckFailureCode.MergeConfirmationRequired]: "Confirm the merge before trying again.",
  [DeckFailureCode.UnsupportedAction]: "That action is not allowed for this pull request.",
};

export interface DeckRevision {
  readonly value: bigint;
  readonly etag: string;
}

export interface DeckOwner {
  readonly ownerId: string;
  readonly kind: DeckOwnerKind;
  readonly label: string;
  readonly canManage: boolean;
  readonly billingSelections: readonly DeckBillingSelection[];
}

export interface DeckBillingSelection {
  readonly organizationId: string;
  readonly teamId: string;
}

export interface DeckNotificationPreference {
  readonly enabled: boolean;
  readonly transitions: readonly DeckNotificationTransition[];
}

export enum DeckNotificationTransition {
  Assigned = "assigned",
  ReviewRequested = "review-requested",
  ChecksFailed = "checks-failed",
  BecameMergeable = "became-mergeable",
  Conflicted = "conflicted",
  Merged = "merged",
  Closed = "closed",
}

export interface DeckView {
  readonly viewId: string;
  readonly owner: DeckOwner;
  readonly billing: DeckBillingSelection;
  readonly name: string;
  readonly rawQuery: string;
  readonly sort: DeckSort;
  readonly grouping: DeckGrouping;
  readonly notificationPreference: DeckNotificationPreference;
  readonly connection: "connected" | "disconnected" | "reauthentication-required";
  readonly revision: DeckRevision;
  readonly lastOpenedAt?: string;
  readonly shortcut?: DeckViewShortcut;
  readonly widgetAttached: boolean;
}

export interface DeckViewShortcut {
  readonly definitionId: string;
  readonly shortcut: StructuredShortcut;
  readonly active: boolean;
  readonly inactiveReason?: "conflict" | "unavailable" | "limit-exceeded" | "malformed";
}

export interface DeckViewInput {
  readonly billing: DeckBillingSelection;
  readonly name: string;
  readonly rawQuery: string;
  readonly sort: DeckSort;
  readonly grouping: DeckGrouping;
  readonly notificationPreference: DeckNotificationPreference;
}

export interface DeckReviewer {
  readonly kind: "user" | "team";
  readonly label: string;
}

export interface DeckPullRequest {
  readonly repositoryOwner: string;
  readonly repositoryName: string;
  readonly number: number;
  readonly title: string;
  readonly author: string;
  readonly reviewDecision: "required" | "changes-requested" | "approved" | "none";
  readonly checks: "pending" | "success" | "failure" | "none";
  readonly mergeability: "unknown" | "mergeable" | "conflicting" | "blocked";
  readonly isDraft: boolean;
  readonly lifecycle: DeckPullRequestLifecycle;
  readonly updatedAt: string;
  readonly createdAt?: string;
  readonly revision: DeckRevision;
  readonly reviewers: readonly DeckReviewer[];
  readonly assignees: readonly string[];
  readonly labels: readonly string[];
  readonly supportedMutations: readonly DeckMutationKind[];
  readonly mergeMethods: readonly DeckMergeMethod[];
}

export interface DeckCursorPage<T> {
  readonly items: readonly T[];
  readonly nextCursor: string;
}

export interface DeckPullRequestPage extends DeckCursorPage<DeckPullRequest> {
  readonly freshness: DeckFreshness;
  readonly refreshedAt?: string;
  readonly truncated: boolean;
  readonly resultLimit: number;
}

export interface DeckMutationCandidate {
  readonly kind: "user" | "team" | "label";
  readonly value: string;
}

export interface DeckMutationCandidatePage extends DeckCursorPage<DeckMutationCandidate> {
  readonly pullRequestRevision: DeckRevision;
}

export interface DeckMutationInput {
  readonly kind: DeckMutationKind;
  readonly users?: readonly string[];
  readonly teams?: readonly string[];
  readonly labels?: readonly string[];
  readonly mergeMethod?: DeckMergeMethod;
  readonly mergeConfirmed?: true;
}

export interface DeckConflict<T> {
  readonly attempted: T;
  readonly current: DeckView;
}

export interface DeckGateway {
  listOwners(): Promise<readonly DeckOwner[]>;
  listViews(owner: DeckOwner, cursor: string): Promise<DeckCursorPage<DeckView>>;
  createView(owner: DeckOwner, input: DeckViewInput, idempotencyKey: string): Promise<DeckView>;
  updateView(view: DeckView, input: DeckViewInput): Promise<DeckView>;
  deleteView(view: DeckView): Promise<void>;
  getView(viewId: string): Promise<DeckView>;
  listPullRequests(viewId: string, cursor: string): Promise<DeckPullRequestPage>;
  listMutationCandidates(
    viewId: string,
    pullRequest: DeckPullRequest,
    kind: DeckMutationKind,
    query: string,
    cursor: string,
  ): Promise<DeckMutationCandidatePage>;
  mutatePullRequest(
    viewId: string,
    pullRequest: DeckPullRequest,
    mutation: DeckMutationInput,
  ): Promise<{ readonly pullRequest?: DeckPullRequest; readonly refreshRequired: boolean }>;
  refreshAfterMutation(viewId: string): Promise<void>;
  refreshView(
    viewId: string,
    confirm: (warning: ManualRefreshWarning) => Promise<boolean>,
  ): Promise<void>;
  openPullRequest(pullRequest: DeckPullRequest): Promise<void>;
  recordViewOpened(viewId: string): void;
  synchronizeShortcuts(): Promise<void>;
  clearShortcuts(): Promise<void>;
  startEligibleRefreshes(
    views: readonly DeckView[],
    onRefreshed: (viewId: string) => void,
  ): () => void;
}

export const unavailableDeckGateway: DeckGateway = {
  listOwners: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  listViews: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  createView: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  updateView: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  deleteView: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  getView: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  listPullRequests: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  listMutationCandidates: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  mutatePullRequest: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  refreshAfterMutation: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  refreshView: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  openPullRequest: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  recordViewOpened: () => undefined,
  synchronizeShortcuts: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  clearShortcuts: async () => {
    throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
  },
  startEligibleRefreshes: () => () => undefined,
};
