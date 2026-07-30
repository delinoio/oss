import { describe, expect, it, vi } from "vitest";

import {
  MAX_FINAL_BODY_UTF8_BYTES,
  PUBLIC_SCREENSHOT_WARNING,
  RealQAOnlineSubmission,
  type EncryptedDraftEnvelope,
  type OnlineSubmissionDraft,
  type OnlineSubmissionGateway,
} from "./onlineSubmission";

const draftKey = "0198a000-0000-7000-8000-000000000757";
const imageId = "0198a000-0000-7000-8000-000000000758";
const submissionId = "0198a000-0000-7000-8000-000000000759";
const assetId = "0198a000-0000-7000-8000-000000000760";

function encryptedDraft(): EncryptedDraftEnvelope {
  return {
    idempotencyKey: draftKey,
    accountBindingDigest: "a".repeat(64),
    encryptionVersion: 1,
    nonce: "n".repeat(24),
    ciphertext: "encrypted-draft-bytes",
  };
}

function draft(finalBody = "valid issue body"): OnlineSubmissionDraft {
  return {
    encrypted: encryptedDraft(),
    finalBody,
    images: [{ clientImageId: imageId, body: new Uint8Array([1, 2, 3]) }],
  };
}

function gateway(): OnlineSubmissionGateway {
  return {
    createSubmission: vi.fn(async () => ({
      submissionId,
      uploadDeadline: new Date("2030-01-01T23:00:00Z"),
      assets: [{ assetId, clientImageId: imageId, revision: 1n }],
    })),
    createImageUpload: vi.fn(async () => ({
      assetRevision: 2n,
      signedPutUrl: "https://assets.realqa.deli.dev/uploads/fixture",
      contentType: "image/png",
      sha256: "a".repeat(64),
      expiresAt: new Date("2030-01-01T00:05:00Z"),
    })),
    putImage: vi.fn(async () => undefined),
    finalizeImageUpload: vi.fn(async () => undefined),
    submitIssue: vi.fn(async () => ({
      state: "submitted" as const,
      issueUrl: "https://github.com/delinoio/oss/issues/757",
    })),
  };
}

describe("RealQA online submission", () => {
  it("warns every time, sequences one issue, and removes only a successful draft", async () => {
    const remote = gateway();
    const retain = vi.fn(async () => undefined);
    const remove = vi.fn(async () => undefined);
    const confirm = vi.fn(async () => true);
    const machine = new RealQAOnlineSubmission(
      remote,
      { retain, remove },
      { confirm },
      () => new Date("2030-01-01T00:00:00Z"),
    );

    await expect(machine.submit(draft())).resolves.toEqual({
      state: "submitted",
      issueUrl: "https://github.com/delinoio/oss/issues/757",
    });
    expect(confirm).toHaveBeenCalledWith(PUBLIC_SCREENSHOT_WARNING);
    expect(retain).toHaveBeenCalledWith(encryptedDraft());
    expect(remove).toHaveBeenCalledWith(draftKey);
    expect(remote.createSubmission).toHaveBeenCalledWith(draft(), draftKey);
    expect(remote.submitIssue).toHaveBeenCalledWith(submissionId, draftKey, true);
    const firstCall = (value: unknown) =>
      vi.mocked(value as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0]!;
    expect(firstCall(retain)).toBeLessThan(firstCall(confirm));
    expect(firstCall(confirm)).toBeLessThan(firstCall(remote.createSubmission));
    expect(firstCall(remote.createSubmission)).toBeLessThan(
      firstCall(remote.createImageUpload),
    );
    expect(firstCall(remote.createImageUpload)).toBeLessThan(
      firstCall(remote.putImage),
    );
    expect(firstCall(remote.putImage)).toBeLessThan(
      firstCall(remote.finalizeImageUpload),
    );
    expect(firstCall(remote.finalizeImageUpload)).toBeLessThan(
      firstCall(remote.submitIssue),
    );
    expect(firstCall(remote.submitIssue)).toBeLessThan(firstCall(remove));

    await machine.submit(draft());
    expect(confirm).toHaveBeenCalledTimes(2);
  });

  it("retains cancellation without creating a server submission", async () => {
    const remote = gateway();
    const retain = vi.fn(async () => undefined);
    const machine = new RealQAOnlineSubmission(
      remote,
      { retain, remove: vi.fn(async () => undefined) },
      { confirm: vi.fn(async () => false) },
    );

    await expect(machine.submit(draft())).resolves.toEqual({
      state: "cancelled",
      draftRetained: true,
    });
    expect(retain).toHaveBeenCalledOnce();
    expect(remote.createSubmission).not.toHaveBeenCalled();
  });

  it("blocks the exact UTF-8 overflow before warning or network and never splits", async () => {
    const remote = gateway();
    const confirm = vi.fn(async () => true);
    const retain = vi.fn(async () => undefined);
    const machine = new RealQAOnlineSubmission(
      remote,
      { retain, remove: vi.fn(async () => undefined) },
      { confirm },
    );

    await expect(
      machine.submit(draft("é".repeat(MAX_FINAL_BODY_UTF8_BYTES / 2 + 1))),
    ).resolves.toEqual({
      state: "failed",
      draftRetained: true,
      reason: "body-overflow",
    });
    expect(confirm).not.toHaveBeenCalled();
    expect(remote.createSubmission).not.toHaveBeenCalled();
    expect(remote.submitIssue).not.toHaveBeenCalled();
    expect(retain).toHaveBeenCalledOnce();
  });

  it("accepts exactly 60,000 UTF-8 bytes", async () => {
    const remote = gateway();
    const machine = new RealQAOnlineSubmission(
      remote,
      {
        retain: vi.fn(async () => undefined),
        remove: vi.fn(async () => undefined),
      },
      { confirm: vi.fn(async () => true) },
      () => new Date("2030-01-01T00:00:00Z"),
    );

    await expect(
      machine.submit(draft("é".repeat(MAX_FINAL_BODY_UTF8_BYTES / 2))),
    ).resolves.toMatchObject({ state: "submitted" });
    expect(remote.createSubmission).toHaveBeenCalledOnce();
    expect(remote.submitIssue).toHaveBeenCalledOnce();
  });

  it("keeps the same encrypted draft and UUID after an ambiguous provider result", async () => {
    const remote = gateway();
    vi.mocked(remote.submitIssue).mockRejectedValueOnce(
      new Error("ambiguous provider result"),
    );
    const retain = vi.fn(async () => undefined);
    const remove = vi.fn(async () => undefined);
    const machine = new RealQAOnlineSubmission(
      remote,
      { retain, remove },
      { confirm: vi.fn(async () => true) },
      () => new Date("2030-01-01T00:00:00Z"),
    );

    await expect(machine.submit(draft())).resolves.toEqual({
      state: "failed",
      draftRetained: true,
      reason: "ambiguous-or-failed",
    });
    expect(retain).toHaveBeenLastCalledWith(encryptedDraft());
    expect(remove).not.toHaveBeenCalled();
    expect(remote.createSubmission).toHaveBeenCalledWith(draft(), draftKey);
    expect(remote.submitIssue).toHaveBeenCalledWith(submissionId, draftKey, true);
  });

  it("rejects plaintext-shaped or non-v7 local draft state", async () => {
    const machine = new RealQAOnlineSubmission(
      gateway(),
      {
        retain: vi.fn(async () => undefined),
        remove: vi.fn(async () => undefined),
      },
      { confirm: vi.fn(async () => true) },
    );
    const malformed = {
      ...draft(),
      encrypted: {
        ...encryptedDraft(),
        idempotencyKey: "not-a-uuid",
        ciphertext: "",
      },
    };

    await expect(machine.submit(malformed)).rejects.toThrow(
      "encrypted draft contract",
    );
  });
});
