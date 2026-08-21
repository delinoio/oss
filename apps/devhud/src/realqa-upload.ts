import { canonicalPublicImageUrl } from "./realqa-submission.ts";
import type { FlattenedCaptureImage, R2CaptureUploadProfile } from "./native-bridge.ts";

export interface OfficialReservation {
  readonly uploadId: string;
  readonly submissionId: string;
  readonly uploadGroupId: string;
  readonly reservationId: string;
  readonly stagingGeneration: string;
  readonly signedPutUrl: string;
  readonly requiredHeaders: { readonly contentType: string; readonly checksumSha256Base64: string; readonly contentLength: string };
}

export interface OfficialUploadDependencies {
  readonly create: (image: FlattenedCaptureImage, group: { readonly submissionId: string; readonly uploadGroupId: string } | null) => Promise<OfficialReservation>;
  readonly put: (image: FlattenedCaptureImage, reservation: OfficialReservation) => Promise<string>;
  readonly finalize: (image: FlattenedCaptureImage, reservation: OfficialReservation, observedEtag: string) => Promise<{ readonly uploadId: string; readonly publicUrl: string }>;
  readonly onFinalized?: (uploadId: string) => void;
}

export async function uploadOfficialImages(images: readonly FlattenedCaptureImage[], dependencies: OfficialUploadDependencies): Promise<{ readonly urls: readonly string[]; readonly finalizedUploadIds: readonly string[] }> {
  const urls: string[] = [];
  const finalizedUploadIds: string[] = [];
  let group: { readonly submissionId: string; readonly uploadGroupId: string } | null = null;
  for (const image of images) {
    const reservation = await dependencies.create(image, group);
    if (reservation.requiredHeaders.contentType !== "image/png" || reservation.requiredHeaders.contentLength !== String(image.bytes) || !/^[A-Za-z0-9+/]{43}=$/u.test(reservation.requiredHeaders.checksumSha256Base64)) throw new TypeError("Invalid official upload headers");
    if (group !== null && (reservation.submissionId !== group.submissionId || reservation.uploadGroupId !== group.uploadGroupId)) throw new TypeError("Official upload group binding changed");
    group = { submissionId: reservation.submissionId, uploadGroupId: reservation.uploadGroupId };
    const observedEtag = await dependencies.put(image, reservation);
    if (observedEtag === "") throw new TypeError("Official upload response did not expose ETag");
    const finalized = await dependencies.finalize(image, reservation, observedEtag);
    if (finalized.uploadId !== reservation.uploadId) throw new TypeError("Finalized upload identity changed");
    finalizedUploadIds.push(finalized.uploadId);
    dependencies.onFinalized?.(finalized.uploadId);
    urls.push(canonicalPublicImageUrl(finalized.publicUrl));
  }
  return { urls, finalizedUploadIds };
}

export async function uploadR2Images(images: readonly FlattenedCaptureImage[], profile: R2CaptureUploadProfile, put: (image: FlattenedCaptureImage, profile: R2CaptureUploadProfile) => Promise<string>): Promise<readonly string[]> {
  const urls: string[] = [];
  for (const image of images) urls.push(canonicalPublicImageUrl(await put(image, profile)));
  return urls;
}
