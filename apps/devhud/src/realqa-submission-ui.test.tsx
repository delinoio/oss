// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createGitHubProvider, type GitHubProvider } from "./github-provider.ts";
import { messages } from "./localization.ts";
import type { CaptureDraft, NativeBridgeV1 } from "./native-bridge.ts";
import { RealqaSubmissionModal } from "./realqa-submission-ui.tsx";
import type { IdentitySettingsValue } from "./service-boundary.tsx";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract.ts";

let identity: IdentitySettingsValue;
let mutationFunctions: Record<string, ReturnType<typeof vi.fn>>;
vi.mock("./service-boundary.tsx", () => ({ useIdentitySettings: () => identity }));
vi.mock("@connectrpc/connect-query", () => ({
  useMutation: (method: { localName: string }) => ({ mutateAsync: mutationFunctions[method.localName] ?? vi.fn() }),
}));

const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
const bootstrap = { issuer: "https://identity.example/", audience: "https://api.example", clientId: "desktop", redirectUri: "devhud://auth/callback" as const, publicAssetBaseUrl: "https://images.example/assets/", capabilities: [] };
const settings = parseDevHudSettings({
  ...defaultDevHudSettings,
  github: {
    ...defaultDevHudSettings.github,
    profiles: [profile],
    repositories: [{ owner: "delinoio", name: "oss", profileRef: profile.id }],
    issueTracker: { owner: "delinoio", repository: "oss", labels: ["bug", "qa"], profileRef: profile.id },
  },
});
const draft: CaptureDraft = {
  id: "018f47a2-7b3c-7def-8abc-1234567890ac",
  revision: 2,
  createdAt: 1,
  updatedAt: 2,
  expiresAt: 3,
  hasBrowserContext: true,
  browserContext: { mappingId: "mapping", context: { url: "https://example.com/redacted", title: "Page", viewport: { width: 800, height: 600 }, userAgent: "Fixture", selectedBounds: null, accessibility: {}, outerHtml: "" } },
  imageCount: 1,
  images: [{ id: "018f47a2-7b3c-7def-8abc-1234567890ad", width: 10, height: 10, previewUrl: "realqa://asset/preview", layers: [], crop: null }],
  canUndo: false,
  canRedo: false,
};

function identityValue(): IdentitySettingsValue {
  return {
    status: "guest", bootstrap, account: null, settings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: settings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("origin.scope"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true), signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
  };
}

function bridge(): NativeBridgeV1 {
  return {
    request: vi.fn(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "github_pat_fixture" } : { kind: "ok" }),
    listen: vi.fn(async () => () => undefined),
  } as NativeBridgeV1;
}

function provider(overrides: Partial<GitHubProvider> = {}): GitHubProvider {
  return { ...createGitHubProvider({ fetch: vi.fn() }), listLabels: vi.fn(async () => ({ items: [{ name: "bug", color: "fff", description: null }, { name: "qa", color: "000", description: null }], nextPage: null, notModified: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })), ...overrides };
}

beforeEach(() => {
  identity = identityValue();
  mutationFunctions = {
    createUpload: vi.fn(),
    deleteUpload: vi.fn(),
    finalizeUpload: vi.fn(),
  };
});
afterEach(cleanup);

describe.each([["English", messages.en], ["Korean", messages.ko]] as const)("RealQA submission modal in %s", (_language, copy) => {
  it("is accessible, autofocuses title, supports optional images, and Esc preserves the draft", async () => {
    const onClose = vi.fn();
    const onConfirmed = vi.fn(async () => undefined);
    const native = bridge();
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={copy} onClose={onClose} onConfirmed={onConfirmed} provider={provider()} />);

    const dialog = screen.getByRole("dialog", { name: copy.issueModalTitle });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText(copy.issueTitle)));
    expect(screen.getByLabelText(copy.issueRepository)).toBeTruthy();
    expect(screen.getByLabelText(copy.issueBody)).toBeTruthy();
    expect(screen.getByLabelText(copy.issueBrowserDiagnostics)).toBeTruthy();
    expect((await screen.findByLabelText("bug") as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText("qa") as HTMLInputElement).checked).toBe(true);
    expect(screen.getByText(copy.issuePublicImageWarning)).toBeTruthy();
    fireEvent.click(screen.getByLabelText(`${copy.editorImage} 1`));
    expect(screen.queryByText(copy.issuePublicImageWarning)).toBeNull();

    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(native.request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.confirm-issue-created" }));
  });
});

describe("RealQA image-free submission", () => {
  it("skips flattening and uploads while confirming the draft only after issue creation", async () => {
    const createIssue = vi.fn(async () => ({ issue: { number: 1, title: "Issue", url: "https://github.com/delinoio/oss/issues/1", marker: "marker", reconciled: false }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const native = bridge();
    const onConfirmed = vi.fn(async () => undefined);
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={messages.en} onClose={vi.fn()} onConfirmed={onConfirmed} provider={provider({ createIssue })} />);
    fireEvent.click(screen.getByLabelText(`${messages.en.editorImage} 1`));
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Image-free issue" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    await waitFor(() => expect(createIssue).toHaveBeenCalledOnce());
    expect(native.request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.flatten" }));
    await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(draft.revision));
  });

  it("reuses the submitted revision when draft cleanup is retried", async () => {
    const createIssue = vi.fn(async () => ({ issue: { number: 1, title: "Issue", url: "https://github.com/delinoio/oss/issues/1", marker: "marker", reconciled: false }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const onConfirmed = vi.fn(async () => { throw new Error("revision changed"); });
    const props = { bridge: bridge(), copy: messages.en, onClose: vi.fn(), onConfirmed, provider: provider({ createIssue }) };
    const view = render(<RealqaSubmissionModal draft={draft} {...props} />);
    fireEvent.click(screen.getByLabelText(`${messages.en.editorImage} 1`));
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Image-free issue" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    await waitFor(() => expect(onConfirmed).toHaveBeenCalledWith(draft.revision));
    view.rerender(<RealqaSubmissionModal draft={{ ...draft, revision: draft.revision + 1 }} {...props} />);
    fireEvent.click(await screen.findByRole("button", { name: messages.en.issueRetryDraftCleanup }));

    await waitFor(() => expect(onConfirmed).toHaveBeenCalledTimes(2));
    expect(onConfirmed.mock.calls).toEqual([[draft.revision], [draft.revision]]);
  });
});

describe("RealQA upload eligibility and cleanup", () => {
  it("requires authentication for official images while retaining image-free guest submission", async () => {
    const native = bridge();
    const createUpload = mutationFunctions.createUpload;
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={messages.en} onClose={vi.fn()} onConfirmed={vi.fn()} provider={provider()} />);

    const uploadProvider = screen.getByLabelText(messages.en.issueUploadProvider) as HTMLSelectElement;
    expect(uploadProvider.value).toBe("r2");
    expect((screen.getByRole("option", { name: messages.en.issueUploadOfficial }) as HTMLOptionElement).disabled).toBe(true);
    expect(screen.getByText(messages.en.issueOfficialSignInRequired)).toBeTruthy();
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Guest issue" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    expect(await screen.findByRole("alert")).toHaveProperty("textContent", messages.en.issueR2SetupRequired);
    expect(createUpload).not.toHaveBeenCalled();
    expect(native.request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.flatten" }));
  });

  it("resolves the selected credential before starting an official image upload", async () => {
    identity = { ...identityValue(), status: "authenticated" };
    const native = bridge();
    vi.mocked(native.request).mockImplementation(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: null } : { kind: "ok" });
    const createIssue = vi.fn();
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={messages.en} onClose={vi.fn()} onConfirmed={vi.fn()} provider={provider({ createIssue })} />);
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Issue with image" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveProperty("textContent", messages.en.issueSubmissionFailed));
    expect(native.request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.flatten" }));
    expect(mutationFunctions.createUpload).not.toHaveBeenCalled();
    expect(createIssue).not.toHaveBeenCalled();
  });

  it("rejects an oversized complete body before credentials or uploads", async () => {
    identity = { ...identityValue(), status: "authenticated" };
    const native = bridge();
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={messages.en} onClose={vi.fn()} onConfirmed={vi.fn()} provider={provider()} />);
    await screen.findByLabelText("bug");
    vi.mocked(native.request).mockClear();
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Oversized issue" } });
    fireEvent.change(screen.getByLabelText(messages.en.issueBody), { target: { value: "x".repeat(65_536) } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    expect(await screen.findByRole("alert")).toHaveProperty("textContent", messages.en.issueBodyTooLarge);
    expect(native.request).not.toHaveBeenCalled();
    expect(mutationFunctions.createUpload).not.toHaveBeenCalled();
  });

  it("retains failed finalized-image cleanup for explicit retry", async () => {
    identity = { ...identityValue(), status: "authenticated" };
    const createUpload = vi.fn(async () => ({ reservation: {
      uploadId: { value: "019b0000-0000-7000-8000-000000000020" }, submissionId: { value: "019b0000-0000-7000-8000-000000000021" },
      uploadGroupId: { value: "019b0000-0000-7000-8000-000000000022" }, reservationId: { value: "019b0000-0000-7000-8000-000000000023" },
      stagingGeneration: 1n, signedPutUrl: "https://upload.example/object", requiredHeaders: { contentType: "image/png", checksumSha256Base64: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", contentLength: 3n },
    } }));
    const finalizeUpload = vi.fn(async () => ({ upload: { uploadId: { value: "019b0000-0000-7000-8000-000000000020" }, publicUrl: "https://images.example/image.png" } }));
    const deleteUpload = vi.fn()
      .mockRejectedValueOnce(new Error("cleanup unavailable"))
      .mockResolvedValueOnce({});
    mutationFunctions = { createUpload, finalizeUpload, deleteUpload };
    const native = bridge();
    vi.mocked(native.request).mockImplementation(async (request) => {
      if (request.operation === "secure.read") return { kind: "secure-value", value: "github_pat_fixture" };
      if (request.operation === "capture.flatten") return { kind: "capture-flattened", images: [{ imageId: draft.images[0].id, width: 1, height: 1, bytes: 3, sha256: "00".repeat(32), assetUrl: "realqa://asset/flattened", downscaled: false }] };
      if (request.operation === "capture.upload-official") return { kind: "capture-uploaded", observedEtag: "etag", publicUrl: null };
      return { kind: "ok" };
    });
    const createIssue = vi.fn(async () => { throw new Error("definite GitHub failure"); });
    render(<RealqaSubmissionModal draft={draft} bridge={native} copy={messages.en} onClose={vi.fn()} onConfirmed={vi.fn()} provider={provider({ createIssue })} />);
    fireEvent.change(screen.getByLabelText(messages.en.issueTitle), { target: { value: "Issue with image" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));

    await waitFor(() => expect(createUpload).toHaveBeenCalledOnce());
    expect(finalizeUpload).toHaveBeenCalledOnce();
    expect(createIssue).toHaveBeenCalledOnce();
    expect(deleteUpload).toHaveBeenCalledOnce();
    const retry = await screen.findByRole("button", { name: messages.en.issueRetryUploadCleanup });
    expect(screen.getByRole("button", { name: messages.en.close })).toHaveProperty("disabled", true);
    expect(deleteUpload).toHaveBeenCalledOnce();
    fireEvent.click(retry);

    await waitFor(() => expect(deleteUpload).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("button", { name: messages.en.issueRetryUploadCleanup })).toBeNull());
    expect(screen.getByRole("button", { name: messages.en.close })).toHaveProperty("disabled", false);
    expect(createUpload).toHaveBeenCalledOnce();
  });
});
