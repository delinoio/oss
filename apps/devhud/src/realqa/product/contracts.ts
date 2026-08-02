import type { CaptureMode, PointerInclusion } from "../capture";
import type { StructuredShortcut } from "../../persistence/contracts";
import type { RealQaProcessUrlRule } from "../drafts/presets";

export enum RealQaDesktopFamily {
  Macos = "macos",
  Windows = "windows",
  Ubuntu = "ubuntu",
}

export enum RealQaAccessMode {
  SignedOut = "signed-out",
  PriorBoundOffline = "prior-bound-offline",
  Online = "online",
}

export enum RealQaSelectorMode {
  Normal = "normal",
  Dom = "dom",
}

export enum RealQaFailureCode {
  AuthenticationRequired = "authentication-required",
  ReauthenticationRequired = "reauthentication-required",
  PermissionDenied = "permission-denied",
  OwnerScopeNotFound = "owner-scope-not-found",
  CapturePermissionRequired = "capture-permission-required",
  CapturePermissionDenied = "capture-permission-denied",
  ChromePermissionRequired = "chrome-permission-required",
  ChromePermissionDenied = "chrome-permission-denied",
  StaleRevision = "stale-revision",
  BillingRequired = "billing-required",
  R2Unavailable = "r2-unavailable",
  GitHubUnavailable = "github-unavailable",
  GitHubDisconnected = "github-disconnected",
  ImageTooLarge = "image-too-large",
  SessionTooLarge = "session-too-large",
  DecodedImageTooLarge = "decoded-image-too-large",
  FinalBodyTooLarge = "final-body-too-large",
  RateLimited = "rate-limited",
  PresetLimitExceeded = "preset-limit-exceeded",
  DeviceShortcutLimitExceeded = "device-shortcut-limit-exceeded",
  UploadConcurrencyLimited = "upload-concurrency-limited",
  UploadRejected = "upload-rejected",
  StorageBillingGrace = "storage-billing-grace",
  ProtectedContent = "protected-content",
  WindowUnavailable = "window-unavailable",
  DisplayChanged = "display-changed",
  PortalCancelled = "portal-cancelled",
  SubmissionAmbiguous = "submission-ambiguous",
  PublicImageConfirmationRequired = "public-image-confirmation-required",
  ServiceUnavailable = "service-unavailable",
}

export class RealQaProductError extends Error {
  constructor(readonly code: RealQaFailureCode) {
    super(code);
    this.name = "RealQaProductError";
  }
}

export interface RealQaDestination {
  readonly destinationId: string;
  readonly repository: string;
  readonly connected: boolean;
  readonly revision: number;
}

export type RealQaIssueField =
  | {
      readonly fieldId: string;
      readonly kind: "input" | "textarea";
      readonly label: string;
      readonly required: boolean;
      readonly defaultValue: string;
      readonly renderLanguage?: string;
    }
  | {
      readonly fieldId: string;
      readonly kind: "dropdown";
      readonly label: string;
      readonly required: boolean;
      readonly multiple: boolean;
      readonly options: readonly string[];
      readonly defaultValue: string;
    }
  | {
      readonly fieldId: string;
      readonly kind: "checkboxes";
      readonly label: string;
      readonly required: boolean;
      readonly options: readonly {
        readonly value: string;
        readonly label: string;
        readonly required: boolean;
      }[];
    };

export interface RealQaIssueDefinition {
  readonly destinationId: string;
  readonly definitionId: string;
  readonly name: string;
  readonly kind: "markdown-template" | "issue-form";
  readonly issueType: string;
  readonly fields: readonly RealQaIssueField[];
}

export interface RealQaPreset {
  readonly presetId: string;
  readonly revision: number;
  readonly name: string;
  readonly captureMode: CaptureMode;
  readonly pointer: PointerInclusion;
  readonly selectorMode: RealQaSelectorMode;
  readonly destinationId: string;
  readonly definitionId: string;
  readonly labels: readonly string[];
  readonly assignees: readonly string[];
  readonly milestoneNumber: number | null;
  readonly projectNodeIds: readonly string[];
  readonly processUrlRules: readonly RealQaProcessUrlRule[];
  readonly billing: RealQaBillingScope;
  readonly backgroundGrant: "active" | "rebind-required" | "revoked";
  readonly shortcut: StructuredShortcut | null;
}

export interface RealQaEditableField {
  readonly id: string;
  readonly label: string;
  readonly value: string;
}

export interface RealQaReviewImage {
  readonly imageId: string;
  readonly revision: number;
  readonly name: string;
  readonly encodedBytes: number;
  readonly selected: boolean;
  readonly uploadState:
    | "local"
    | "uploading"
    | "verified"
    | "public"
    | "removed";
  readonly uploadProgress: number;
}

export interface RealQaDraft {
  readonly draftId: string;
  readonly submissionIdempotencyKey: string;
  readonly revision: number;
  readonly presetId: string;
  readonly title: string;
  readonly body: string;
  readonly url: string;
  readonly urlWarning: boolean;
  readonly environment: readonly RealQaEditableField[];
  readonly dom: readonly RealQaEditableField[];
  readonly issueAnswers: Readonly<Record<string, readonly string[]>>;
  readonly labels: readonly string[];
  readonly assignees: readonly string[];
  readonly milestoneNumber: number | null;
  readonly projectNodeIds: readonly string[];
  readonly images: readonly RealQaReviewImage[];
}

export interface RealQaSubmissionReplay {
  readonly idempotencyKey: string;
  readonly expectedSubmissionRevision: number;
  readonly originalDraft: RealQaDraft;
}

export interface RealQaSubmissionSummary {
  readonly submissionId: string;
  readonly revision: number;
  readonly state:
    | "uploading"
    | "reconciling"
    | "submitted"
    | "failed"
    | "storage-billing-grace"
    | "assets-deleted"
    | "deleted";
  readonly issueUrl: string | null;
  readonly graceExpiresAt: string | null;
  readonly authorizationId: string | null;
  readonly authorizationRevision: number;
  readonly rebindAvailable: boolean;
  readonly images: readonly RealQaReviewImage[];
  /** Available only while the encrypted local draft can reconstruct the exact request. */
  readonly replay: RealQaSubmissionReplay | null;
}

export interface RealQaBillingScope {
  readonly organizationId: string;
  readonly teamId: string;
}

export interface RealQaBillingScopeChoice extends RealQaBillingScope {
  readonly label: string;
}

export enum RealQaOwnerScopeKind {
  Personal = "personal",
  Organization = "organization",
}

export type RealQaOwnerScope =
  | {
      readonly kind: RealQaOwnerScopeKind.Personal;
      readonly personalAccountId: string;
    }
  | {
      readonly kind: RealQaOwnerScopeKind.Organization;
      readonly organizationId: string;
    };

export type RealQaOwnerScopeChoice = RealQaOwnerScope & {
  readonly label: string;
};

export interface RealQaProductSnapshot {
  readonly platform: RealQaDesktopFamily;
  readonly access: RealQaAccessMode;
  readonly online: boolean;
  readonly presets: readonly RealQaPreset[];
  readonly destinations: readonly RealQaDestination[];
  readonly definitions: readonly RealQaIssueDefinition[];
  readonly drafts: readonly RealQaDraft[];
  readonly submissions: readonly RealQaSubmissionSummary[];
  readonly replacementBillingScopes: readonly RealQaBillingScopeChoice[];
  readonly featureDeletionScopes: readonly RealQaOwnerScopeChoice[];
}

export type RealQaProductAction =
  | { readonly kind: "connect-destination" }
  | {
      readonly kind: "create-preset";
      readonly preset: RealQaPreset;
      readonly idempotencyKey: string;
    }
  | { readonly kind: "save-preset"; readonly preset: RealQaPreset }
  | {
      readonly kind: "delete-preset";
      readonly presetId: string;
      readonly expectedRevision: number;
    }
  | {
      readonly kind: "disconnect-destination";
      readonly destinationId: string;
      readonly expectedRevision: number;
      readonly idempotencyKey: string;
    }
  | { readonly kind: "reconnect-destination"; readonly destinationId: string }
  | { readonly kind: "create-draft"; readonly presetId: string }
  | { readonly kind: "save-draft"; readonly draft: RealQaDraft }
  | {
      readonly kind: "edit-image";
      readonly draftId: string;
      readonly imageId: string;
    }
  | {
      readonly kind: "delete-draft";
      readonly draftId: string;
      readonly expectedRevision: number;
    }
  | {
      readonly kind: "capture";
      readonly draftId: string;
      readonly captureMode: CaptureMode;
      readonly pointer: PointerInclusion;
      readonly selectorMode: RealQaSelectorMode;
      readonly selection: { readonly x: number; readonly y: number; readonly width: number; readonly height: number };
    }
  | {
      readonly kind: "submit";
      readonly draft: RealQaDraft;
      readonly idempotencyKey: string;
      readonly publicImageConfirmation: true;
    }
  | {
      readonly kind: "retry-submission";
      readonly submissionId: string;
      readonly expectedSubmissionRevision: number;
      readonly idempotencyKey: string;
      readonly originalDraft: RealQaDraft;
      readonly publicImageConfirmation: true;
    }
  | {
      readonly kind: "delete-image";
      readonly submissionId: string;
      readonly imageId: string;
      readonly expectedSubmissionRevision: number;
      readonly expectedImageRevision: number;
      readonly idempotencyKey: string;
    }
  | {
      readonly kind: "delete-submission-assets";
      readonly submissionId: string;
      readonly expectedSubmissionRevision: number;
      readonly idempotencyKey: string;
    }
  | {
      readonly kind: "rebind-authorization";
      readonly submissionId: string;
      readonly expectedAuthorizationId: string;
      readonly expectedRevision: number;
      readonly replacementBilling: {
        readonly organizationId: string;
        readonly teamId: string;
      };
      readonly idempotencyKey: string;
    }
  | {
      readonly kind: "delete-feature-data";
      readonly owner: RealQaOwnerScope;
      readonly idempotencyKey: string;
    };

export interface RealQaProductGateway {
  load(): Promise<RealQaProductSnapshot>;
  execute(
    action: RealQaProductAction,
    reportProgress: (progress: number) => void,
  ): Promise<RealQaProductSnapshot>;
}

export const realQaFailureGuidance: Readonly<Record<RealQaFailureCode, string>> = {
  [RealQaFailureCode.AuthenticationRequired]:
    "Sign in with the previously bound DeliDev account to enter RealQA.",
  [RealQaFailureCode.ReauthenticationRequired]:
    "Connect to the internet and sign in again before uploading or submitting.",
  [RealQaFailureCode.PermissionDenied]:
    "You do not have permission to perform this RealQA action. Ask an owner or choose a scope you can access.",
  [RealQaFailureCode.OwnerScopeNotFound]:
    "The selected personal or organization scope is unavailable. Refresh and choose another scope.",
  [RealQaFailureCode.CapturePermissionRequired]:
    "Allow screen capture in the operating system prompt to continue.",
  [RealQaFailureCode.CapturePermissionDenied]:
    "Screen capture is blocked. Review the operating system privacy settings.",
  [RealQaFailureCode.ChromePermissionRequired]:
    "Chrome needs one-time access to this site before DOM selection.",
  [RealQaFailureCode.ChromePermissionDenied]:
    "Chrome site access was denied. Continue without DOM metadata or review the extension permission.",
  [RealQaFailureCode.StaleRevision]:
    "This item changed elsewhere. Reload it, compare your changes, and apply them again.",
  [RealQaFailureCode.BillingRequired]:
    "The selected payer cannot authorize this transfer. Review billing before retrying.",
  [RealQaFailureCode.R2Unavailable]:
    "Screenshot storage is temporarily unavailable. The encrypted draft remains on this device.",
  [RealQaFailureCode.GitHubUnavailable]:
    "GitHub could not complete this request. RealQA will reconcile before another issue is created.",
  [RealQaFailureCode.GitHubDisconnected]:
    "This GitHub connection was disconnected. Reload the workspace, then reconnect it before retrying.",
  [RealQaFailureCode.ImageTooLarge]: "An image exceeds the 25 MiB encoded limit.",
  [RealQaFailureCode.SessionTooLarge]: "The selected images exceed the 250 MiB session limit.",
  [RealQaFailureCode.DecodedImageTooLarge]: "An image exceeds the 100 megapixel decoded limit.",
  [RealQaFailureCode.FinalBodyTooLarge]:
    "The serialized GitHub issue exceeds 60,000 UTF-8 bytes. Remove content or images, or save part as another draft.",
  [RealQaFailureCode.RateLimited]: "The 30 submissions per hour limit is active. Retry later.",
  [RealQaFailureCode.PresetLimitExceeded]:
    "This account already has 100 presets. Delete an unused preset before creating another.",
  [RealQaFailureCode.DeviceShortcutLimitExceeded]:
    "This device already has 20 active RealQA shortcuts. Remove a shortcut before adding another.",
  [RealQaFailureCode.UploadConcurrencyLimited]:
    "Three uploads are already active. Wait for one to finish and retry.",
  [RealQaFailureCode.UploadRejected]:
    "The screenshot was malformed, unsupported, or failed secure image verification. Remove it from the draft and capture a new image.",
  [RealQaFailureCode.StorageBillingGrace]:
    "Storage billing is in its 30-day grace period. New submissions are blocked until recovery.",
  [RealQaFailureCode.ProtectedContent]: "The operating system protected this capture source.",
  [RealQaFailureCode.WindowUnavailable]: "The selected window was minimized or closed.",
  [RealQaFailureCode.DisplayChanged]: "The display layout changed. Select the capture area again.",
  [RealQaFailureCode.PortalCancelled]: "The Wayland capture prompt was cancelled.",
  [RealQaFailureCode.SubmissionAmbiguous]:
    "The GitHub result is uncertain. Retry reconciliation with the same submission; no new issue will be created first.",
  [RealQaFailureCode.PublicImageConfirmationRequired]:
    "Review the public-screenshot warning and confirm this submission attempt again.",
  [RealQaFailureCode.ServiceUnavailable]:
    "RealQA is temporarily unavailable. No screenshot or issue content was included in this error.",
};
