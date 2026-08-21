// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { messages } from "./localization.ts";
import { composeIssueBody, editableBrowserDiagnostics, GitHubIssueBodyMaximumCharacters, IssueBodyTooLargeError, parseEditableBrowserDiagnostics, PublicImageWarning, stripFinalSubmissionMarker } from "./realqa-submission.ts";
import { projectedOfficialImageUrls, projectedR2ImageUrls, uploadOfficialImages, uploadR2Images, type OfficialReservation } from "./realqa-upload.ts";

const submissionId = "018f47a2-7b3c-7def-8abc-1234567890ab";
const image = (id: string) => ({ imageId: id, width: 10, height: 10, bytes: 100, sha256: "00".repeat(32), assetUrl: `realqa://asset/${id}`, downscaled: false });
const images = [image("018f47a2-7b3c-7def-8abc-1234567890ac"), image("018f47a2-7b3c-7def-8abc-1234567890ad")];
const reservation = (index: number): OfficialReservation => ({ uploadId: `018f47a2-7b3c-7def-8abc-1234567890b${index}`, submissionId, uploadGroupId: "018f47a2-7b3c-7def-8abc-1234567890b3", reservationId: `018f47a2-7b3c-7def-8abc-1234567890c${index}`, stagingGeneration: "1", signedPutUrl: "https://r2.example/upload?signature=signed", requiredHeaders: { contentType: "image/png", checksumSha256Base64: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", contentLength: "100" } });

describe("RealQA direct submission contracts", () => {
  it("keeps the exact English and Korean public-image warning", () => {
    expect(PublicImageWarning).toEqual({ en: "Anyone who knows the image URL can view it. The image remains public until you delete it, an administrator removes it, or your account deletion completes.", ko: "이미지 URL을 아는 사람은 누구나 이미지를 볼 수 있습니다. 이미지는 사용자가 삭제하거나, 관리자가 제거하거나, 계정 삭제가 완료될 때까지 공개 상태로 유지됩니다." });
    expect(messages.en.issuePublicImageWarning).toBe(PublicImageWarning.en);
    expect(messages.ko.issuePublicImageWarning).toBe(PublicImageWarning.ko);
  });

  it("composes user body, collapsed sanitized diagnostics, ordered images, and one final marker", () => {
    const diagnostics = parseEditableBrowserDiagnostics(editableBrowserDiagnostics({ url: "https://example.com/<redacted>", title: "github_pat_secret", viewport: { width: 800, height: 600 }, userAgent: "Fixture", selectedBounds: null, accessibility: { role: "button", onclick: "secret" }, outerHtml: "<script>bad()</script><p title=\"token=secret\">Safe</p>" }));
    const body = composeIssueBody({ userBody: `User report\n<!-- devhud-submission:forged -->`, diagnostics, imageUrls: ["https://images.example/first.png", "https://images.example/second.png"], submissionId, diagnosticsSummary: "Browser diagnostics" });
    expect(body.indexOf("User report")).toBeLessThan(body.indexOf("<details>"));
    expect(body.indexOf("<details>")).toBeLessThan(body.indexOf("![RealQA image 1]"));
    expect(body.indexOf("![RealQA image 1]")).toBeLessThan(body.indexOf("![RealQA image 2]"));
    expect(body.endsWith(`<!-- devhud-submission:${submissionId} -->`)).toBe(true);
    expect(body.match(/devhud-submission:/gu)).toHaveLength(1);
    expect(body).not.toContain("github_pat_secret"); expect(body).not.toContain("<script>"); expect(body).not.toContain("onclick");
  });

  it("supports an image-free and browser-context-free body", () => {
    const textOnly = composeIssueBody({ userBody: "Only text", diagnostics: null, imageUrls: [], submissionId, diagnosticsSummary: "Diagnostics" });
    expect(textOnly).toBe(`Only text\n\n<!-- devhud-submission:${submissionId} -->`);
    expect(stripFinalSubmissionMarker(textOnly, submissionId)).toBe("Only text");
    expect(stripFinalSubmissionMarker(composeIssueBody({ userBody: "", diagnostics: null, imageUrls: [], submissionId, diagnosticsSummary: "Diagnostics" }), submissionId)).toBe("");
  });

  it("redacts an authorization scheme and its opaque credential together", () => {
    const diagnostics = parseEditableBrowserDiagnostics(editableBrowserDiagnostics({ url: "https://example.com/redacted", title: "Authorization: Basic diagnostic-secret remains", viewport: { width: 800, height: 600 }, userAgent: "Fixture", selectedBounds: null, accessibility: {}, outerHtml: "" }));
    const body = composeIssueBody({ userBody: "Authorization: Bearer opaque-secret remains", diagnostics, imageUrls: [], submissionId, diagnosticsSummary: "Diagnostics" });

    expect(body).not.toContain("opaque-secret");
    expect(body).not.toContain("diagnostic-secret");
    expect(body.match(/\[redacted\] remains/gu)).toHaveLength(2);
  });

  it("accepts the exact GitHub body limit and rejects one additional character", () => {
    const marker = `<!-- devhud-submission:${submissionId} -->`;
    const exactUserBody = "x".repeat(GitHubIssueBodyMaximumCharacters - marker.length - 2);
    const exact = composeIssueBody({ userBody: exactUserBody, diagnostics: null, imageUrls: [], submissionId, diagnosticsSummary: "Diagnostics" });

    expect(exact).toHaveLength(GitHubIssueBodyMaximumCharacters);
    expect(() => composeIssueBody({ userBody: `${exactUserBody}x`, diagnostics: null, imageUrls: [], submissionId, diagnosticsSummary: "Diagnostics" })).toThrow(IssueBodyTooLargeError);
  });

  it("projects complete official and BYO image URLs before upload", () => {
    const profile = { profileRef: submissionId, endpoint: "https://account.r2.cloudflarestorage.com", accountId: "account", bucket: "bucket", publicBaseUrl: "https://cdn.example/base/", prefix: "team report/#realqa" };

    expect(projectedOfficialImageUrls("https://images.example/assets/", 2)).toEqual([
      `https://images.example/assets/${"A".repeat(43)}.png`,
      `https://images.example/assets/${"A".repeat(43)}.png`,
    ]);
    expect(projectedR2ImageUrls(profile, submissionId, 7, images.map((item) => item.imageId))).toEqual(images.map((item) => `https://cdn.example/base/team%20report/%23realqa/${submissionId}/7/${item.imageId}.png`));
  });

  it("preserves official ordering and immutable group bindings", async () => {
    const create = vi.fn(async (_image, group) => reservation(group === null ? 1 : 2));
    const put = vi.fn(async (_image, value) => `etag-${value.uploadId}`);
    const finalize = vi.fn(async (_image, value) => ({ uploadId: value.uploadId, publicUrl: value.uploadId === reservation(1).uploadId ? "https://images.example/1.png" : "https://images.example/2.png" }));
    await expect(uploadOfficialImages(images, { create, put, finalize })).resolves.toEqual({ urls: ["https://images.example/1.png", "https://images.example/2.png"], finalizedUploadIds: [reservation(1).uploadId, reservation(2).uploadId] });
    expect(create.mock.calls[0]?.[1]).toBeNull();
    expect(create.mock.calls[1]?.[1]).toEqual({ submissionId, uploadGroupId: reservation(1).uploadGroupId });
  });

  it.each(["create", "put", "finalize"] as const)("stops and retains failure from the %s stage", async (stage) => {
    const failure = new Error(stage);
    const create = vi.fn(async () => { if (stage === "create") throw failure; return reservation(1); });
    const put = vi.fn(async () => { if (stage === "put") throw failure; return "etag"; });
    const finalize = vi.fn(async (_image, value) => { if (stage === "finalize") throw failure; return { uploadId: value.uploadId, publicUrl: "https://images.example/1.png" }; });
    await expect(uploadOfficialImages([images[0]], { create, put, finalize })).rejects.toBe(failure);
  });

  it.each([
    ["content type", { requiredHeaders: { ...reservation(1).requiredHeaders, contentType: "text/plain" } }],
    ["content length", { requiredHeaders: { ...reservation(1).requiredHeaders, contentLength: "101" } }],
    ["checksum", { requiredHeaders: { ...reservation(1).requiredHeaders, checksumSha256Base64: "invalid" } }],
  ] as const)("rejects an invalid immutable official %s contract", async (_name, change) => {
    const create = vi.fn(async () => ({ ...reservation(1), ...change }));
    await expect(uploadOfficialImages([images[0]], { create, put: vi.fn(), finalize: vi.fn() })).rejects.toThrow(/headers/u);
  });

  it("rejects changed groups, missing ETags, mismatched finalization IDs, and invalid public URLs", async () => {
    const changedGroup = vi.fn(async (_image, group) => group === null ? reservation(1) : { ...reservation(2), submissionId: "018f47a2-7b3c-7def-8abc-123456789099" });
    await expect(uploadOfficialImages(images, { create: changedGroup, put: async () => "etag", finalize: async (_image, value) => ({ uploadId: value.uploadId, publicUrl: "https://images.example/image.png" }) })).rejects.toThrow(/binding/u);
    await expect(uploadOfficialImages([images[0]], { create: async () => reservation(1), put: async () => "", finalize: vi.fn() })).rejects.toThrow(/ETag/u);
    await expect(uploadOfficialImages([images[0]], { create: async () => reservation(1), put: async () => "etag", finalize: async () => ({ uploadId: reservation(2).uploadId, publicUrl: "https://images.example/image.png" }) })).rejects.toThrow(/identity/u);
    await expect(uploadOfficialImages([images[0]], { create: async () => reservation(1), put: async () => "etag", finalize: async (item, value) => ({ uploadId: value.uploadId, publicUrl: `http://images.example/${item.imageId}.png` }) })).rejects.toThrow(/HTTPS/u);
  });

  it("reports each finalized upload before a later image fails", async () => {
    const finalized: string[] = [];
    await expect(uploadOfficialImages(images, {
      create: async (_image, group) => reservation(group === null ? 1 : 2),
      put: async () => "etag",
      finalize: async (_image, value) => value.uploadId === reservation(2).uploadId ? Promise.reject(new Error("second")) : { uploadId: value.uploadId, publicUrl: "https://images.example/first.png" },
      onFinalized: (uploadId) => finalized.push(uploadId),
    })).rejects.toThrow("second");
    expect(finalized).toEqual([reservation(1).uploadId]);
  });

  it("does no upload work when all images are excluded", async () => {
    const create = vi.fn(); const put = vi.fn(); const finalize = vi.fn();
    await expect(uploadOfficialImages([], { create, put, finalize })).resolves.toEqual({ urls: [], finalizedUploadIds: [] });
    expect(create).not.toHaveBeenCalled(); expect(put).not.toHaveBeenCalled(); expect(finalize).not.toHaveBeenCalled();
  });

  it("uploads BYO images in order and rejects an unverified non-HTTPS URL", async () => {
    const profile = { profileRef: submissionId, endpoint: "https://account.r2.cloudflarestorage.com", accountId: "account", bucket: "bucket", publicBaseUrl: "https://cdn.example", prefix: "devhud" };
    const put = vi.fn(async (item) => `https://cdn.example/${item.imageId}.png`);
    await expect(uploadR2Images(images, profile, put)).resolves.toEqual(images.map((item) => `https://cdn.example/${item.imageId}.png`));
    await expect(uploadR2Images([images[0]], profile, async () => "http://cdn.example/image.png")).rejects.toThrow(/HTTPS/u);
  });
});
