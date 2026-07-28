import { create } from "@bufbuild/protobuf";
import type { Client } from "@connectrpc/connect";

import {
  CaptureMode,
  CreateSubmissionRequestSchema,
  OwnerScopeKind,
  RealQALimit,
  RealQAPresetService,
  RealQASubmissionService,
  RealQATrackerService,
  SelectorMode,
  TrackerKind,
  UuidV7Schema,
} from "./index.js";

export const canonicalServiceDescriptors = [
  RealQAPresetService,
  RealQATrackerService,
  RealQASubmissionService,
] as const;

export const localSubmissionId = create(UuidV7Schema, {
  value: "018f47f2-7c5d-7abc-8def-0123456789ab",
});

export const createSubmission = create(CreateSubmissionRequestSchema, {
  owner: {
    kind: OwnerScopeKind.PERSONAL,
    owner: {
      case: "personalAccountId",
      value: localSubmissionId,
    },
  },
  images: [],
});

export const contractLimits = {
  personalPresets: RealQALimit.REAL_QA_LIMIT_PERSONAL_PRESETS,
  organizationPresets: RealQALimit.REAL_QA_LIMIT_ORGANIZATION_PRESETS,
  deviceShortcuts: RealQALimit.REAL_QA_LIMIT_DEVICE_SHORTCUTS,
  imageEncodedBytes: RealQALimit.REAL_QA_LIMIT_MAX_IMAGE_ENCODED_BYTES,
  sessionEncodedBytes: RealQALimit.REAL_QA_LIMIT_MAX_SESSION_ENCODED_BYTES,
  decodedImagePixels: RealQALimit.REAL_QA_LIMIT_MAX_DECODED_IMAGE_PIXELS,
  finalBodyUtf8Bytes: RealQALimit.REAL_QA_LIMIT_MAX_FINAL_BODY_UTF8_BYTES,
} as const;

export const defaultCapture = {
  mode: CaptureMode.REGION,
  selector: SelectorMode.NORMAL,
  tracker: TrackerKind.GITHUB_COM,
} as const;

export type SubmissionClient = Client<typeof RealQASubmissionService>;
