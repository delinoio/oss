export const PUBLIC_SCREENSHOT_WARNING =
  "Anyone with the GitHub issue URL or an image URL can view these screenshots." as const;

export const MAX_FINAL_BODY_UTF8_BYTES = 60_000 as const;
export const MAX_ISSUE_TITLE_UTF8_BYTES = 256 as const;
export const MAX_SESSION_ENCODED_BYTES = 250 * 1024 * 1024;

const UUID_V7 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

export interface EncryptedDraftEnvelope {
  /** Persistent local replay identity. It is generated once when the draft is created. */
  readonly idempotencyKey: string;
  readonly accountBindingDigest: string;
  readonly encryptionVersion: number;
  readonly nonce: string;
  readonly ciphertext: string;
}

export interface SubmissionImage {
  readonly clientImageId: string;
  readonly body: Uint8Array;
}

export interface OnlineSubmissionDraft {
  readonly encrypted: EncryptedDraftEnvelope;
  readonly finalBody: string;
  readonly images: readonly SubmissionImage[];
}

export interface SubmissionResource {
  readonly submissionId: string;
  readonly uploadDeadline: Date;
  readonly assets: readonly {
    readonly assetId: string;
    readonly clientImageId: string;
    readonly revision: bigint;
  }[];
}

export interface UploadCapability {
  readonly assetRevision: bigint;
  readonly signedPutUrl: string;
  readonly contentType: string;
  readonly sha256: string;
  readonly expiresAt: Date;
}

export interface SubmissionResult {
  readonly state: "submitted";
  readonly issueUrl: string;
}

/**
 * The native implementation maps these operations to the closed RealQA
 * Connect/signed-PUT transport. It accepts no arbitrary URL, method, or header.
 */
export interface OnlineSubmissionGateway {
  createSubmission(
    draft: OnlineSubmissionDraft,
    idempotencyKey: string,
  ): Promise<SubmissionResource>;
  createImageUpload(
    submissionId: string,
    assetId: string,
    expectedRevision: bigint,
    idempotencyRoot: string,
  ): Promise<UploadCapability>;
  putImage(
    capability: UploadCapability,
    body: Uint8Array,
  ): Promise<void>;
  finalizeImageUpload(
    submissionId: string,
    assetId: string,
    expectedRevision: bigint,
    idempotencyRoot: string,
  ): Promise<void>;
  submitIssue(
    submissionId: string,
    idempotencyKey: string,
    publicImageConfirmation: true,
  ): Promise<SubmissionResult>;
}

/**
 * Draft bytes are already application-encrypted by the native draft boundary.
 * This interface deliberately has no plaintext draft write method.
 */
export interface EncryptedDraftVault {
  retain(envelope: EncryptedDraftEnvelope): Promise<void>;
  remove(idempotencyKey: string): Promise<void>;
}

export interface PublicWarningPresenter {
  confirm(message: typeof PUBLIC_SCREENSHOT_WARNING): Promise<boolean>;
}

export type OnlineSubmissionOutcome =
  | SubmissionResult
  | { readonly state: "cancelled"; readonly draftRetained: true }
  | {
      readonly state: "failed";
      readonly draftRetained: true;
      readonly reason: "body-overflow" | "ambiguous-or-failed";
    };

/**
 * Runs one online attempt. Every invocation displays and consumes a fresh
 * confirmation, retains the encrypted draft before remote work, and reuses the
 * draft's UUID v7 for create and SubmitIssue reconciliation.
 */
export class RealQAOnlineSubmission {
  constructor(
    private readonly gateway: OnlineSubmissionGateway,
    private readonly vault: EncryptedDraftVault,
    private readonly warning: PublicWarningPresenter,
    private readonly now: () => Date = () => new Date(),
  ) {}

  async submit(draft: OnlineSubmissionDraft): Promise<OnlineSubmissionOutcome> {
    validateDraft(draft);
    await this.vault.retain(draft.encrypted);

    if (new TextEncoder().encode(draft.finalBody).byteLength > MAX_FINAL_BODY_UTF8_BYTES) {
      return {
        state: "failed",
        draftRetained: true,
        reason: "body-overflow",
      };
    }

    const confirmed = await this.warning.confirm(PUBLIC_SCREENSHOT_WARNING);
    if (!confirmed) {
      return { state: "cancelled", draftRetained: true };
    }

    try {
      const key = draft.encrypted.idempotencyKey;
      const submission = await this.gateway.createSubmission(draft, key);
      if (
        !UUID_V7.test(submission.submissionId)
        || this.now().getTime() >= submission.uploadDeadline.getTime()
      ) {
        throw new Error("RealQA returned an invalid or expired submission.");
      }
      const assetsByClientId = new Map(
        submission.assets.map((asset) => [asset.clientImageId, asset]),
      );
      for (const image of draft.images) {
        const asset = assetsByClientId.get(image.clientImageId);
        if (asset === undefined) {
          throw new Error("RealQA omitted a declared image asset.");
        }
        const capability = await this.gateway.createImageUpload(
          submission.submissionId,
          asset.assetId,
          asset.revision,
          key,
        );
        if (
          capability.expiresAt.getTime() > submission.uploadDeadline.getTime()
          || this.now().getTime() >= capability.expiresAt.getTime()
        ) {
          throw new Error("RealQA returned an invalid upload deadline.");
        }
        await this.gateway.putImage(capability, image.body);
        await this.gateway.finalizeImageUpload(
          submission.submissionId,
          asset.assetId,
          capability.assetRevision,
          key,
        );
      }
      const result = await this.gateway.submitIssue(
        submission.submissionId,
        key,
        true,
      );
      if (result.state !== "submitted" || !isExactPublicIssueURL(result.issueUrl)) {
        throw new Error("RealQA returned an invalid final submission.");
      }
      await this.vault.remove(key);
      return result;
    } catch {
      // The same encrypted envelope and UUID v7 remain available for an exact
      // authenticated replay. No branch creates a second issue or splits body.
      await this.vault.retain(draft.encrypted);
      return {
        state: "failed",
        draftRetained: true,
        reason: "ambiguous-or-failed",
      };
    }
  }
}

function validateDraft(draft: OnlineSubmissionDraft): void {
  const envelope = draft.encrypted;
  if (
    !UUID_V7.test(envelope.idempotencyKey)
    || envelope.accountBindingDigest.length < 32
    || envelope.encryptionVersion <= 0
    || envelope.nonce.length < 16
    || envelope.ciphertext.length === 0
    || draft.images.some(
      (image) => !UUID_V7.test(image.clientImageId) || image.body.byteLength === 0,
    )
    || new Set(draft.images.map((image) => image.clientImageId)).size
      !== draft.images.length
  ) {
    throw new Error("RealQA encrypted draft contract is invalid.");
  }
}

function isExactPublicIssueURL(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (
      parsed.protocol === "https:"
      && parsed.hostname === "github.com"
      && parsed.username === ""
      && parsed.password === ""
      && parsed.port === ""
      && parsed.search === ""
      && parsed.hash === ""
      && /^\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/issues\/[1-9][0-9]*$/u.test(
        parsed.pathname,
      )
    );
  } catch {
    return false;
  }
}
