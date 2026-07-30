import type {
  ComposerImage,
  EditorOperation,
  ImageMediaType,
} from "../capture";

export const REALQA_DRAFT_RECORD_FAMILY = "devhud.realqa-draft.v1" as const;
export const REALQA_DRAFT_SCHEMA_VERSION = 1 as const;

export enum RealQaDraftAccess {
  Locked = "locked",
  Offline = "offline",
  Online = "online",
}

export enum RealQaDraftFailure {
  AuthenticationRequired = "authentication-required",
  FirstTimeOffline = "first-time-offline",
  ReauthenticationRequired = "reauthentication-required",
  AccountLocked = "account-locked",
  Conflict = "conflict",
  Corrupt = "corrupt",
  FutureVersion = "future-version",
  InvalidRecord = "invalid-record",
  StorageUnavailable = "storage-unavailable",
  WriteFailed = "write-failed",
}

export interface RemovableDraftField {
  readonly id: string;
  readonly label: string;
  readonly value: string;
}

export interface RestorableDraftUrl {
  /** The reviewed value. Query and fragment are absent unless explicitly restored. */
  readonly value: string;
  /** Encrypted local-only source parts used by the explicit restore action. */
  readonly strippedQuery: string | null;
  readonly strippedFragment: string | null;
  readonly warning: "localhost-or-private-host" | null;
}

export interface DraftEditorImageReference {
  readonly imageId: string;
  readonly sourceRevision: number;
  readonly operations: readonly EditorOperation[];
  readonly outputMediaType: ImageMediaType;
}

export interface RealQaDraftContent {
  readonly title: string;
  readonly body: string;
  readonly presetId: string | null;
  readonly presetRevision: number | null;
  readonly destinationId: string | null;
  readonly repositoryDefinitionId: string | null;
  readonly environment: readonly RemovableDraftField[];
  readonly url: RestorableDraftUrl | null;
  readonly dom: readonly RemovableDraftField[];
  /**
   * Full source bytes are intentionally absent. Native persistence resolves
   * these references against the composer and encrypts originals with the
   * nondestructive operation list.
   */
  readonly images: readonly DraftEditorImageReference[];
}

export interface SaveRealQaDraftRequest {
  readonly draftId: string;
  readonly composerSessionId: string;
  readonly expectedRevision: number | null;
  readonly submissionIdempotencyKey: string;
  readonly content: RealQaDraftContent;
}

export interface RealQaDraftSummary {
  readonly draftId: string;
  readonly revision: number;
  readonly createdAtUnixMs: number;
  readonly updatedAtUnixMs: number;
}

export interface LoadedDraftEditorImage
  extends ComposerImage, DraftEditorImageReference {}

export interface LoadedRealQaDraftContent
  extends Omit<RealQaDraftContent, "images"> {
  readonly images: readonly LoadedDraftEditorImage[];
}

export interface LoadedRealQaDraft extends RealQaDraftSummary {
  readonly submissionIdempotencyKey: string;
  readonly content: LoadedRealQaDraftContent;
}

export interface RealQaDraftStatus {
  readonly access: RealQaDraftAccess;
  readonly canCapture: boolean;
  readonly canSubmit: boolean;
}

export interface NativeRealQaDraftBridge {
  status(): Promise<RealQaDraftStatus>;
  list(): Promise<readonly RealQaDraftSummary[]>;
  load(draftId: string, composerSessionId: string): Promise<LoadedRealQaDraft>;
  save(request: SaveRealQaDraftRequest): Promise<RealQaDraftSummary>;
  delete(draftId: string, expectedRevision: number): Promise<void>;
  assertSubmissionAllowed(draftId: string, expectedRevision: number): Promise<void>;
}

export function removeDraftField(
  fields: readonly RemovableDraftField[],
  id: string,
): readonly RemovableDraftField[] {
  return fields.filter((field) => field.id !== id);
}

export function removeDraftUrl(
  content: RealQaDraftContent,
): RealQaDraftContent {
  return { ...content, url: null };
}
