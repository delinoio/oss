import { invoke } from "@tauri-apps/api/core";

import type {
  LoadedRealQaDraft,
  NativeRealQaDraftBridge,
  RealQaDraftStatus,
  RealQaDraftSummary,
} from "./contracts";

interface NativeDraftResults {
  realqa_assert_local_draft_submission_allowed: void;
  realqa_delete_local_draft: void;
  realqa_get_local_draft_status: RealQaDraftStatus;
  realqa_list_local_drafts: readonly RealQaDraftSummary[];
  realqa_load_local_draft: LoadedRealQaDraft;
  realqa_save_local_draft: RealQaDraftSummary;
}

/**
 * Record-specific by construction: no method accepts a path, account, key,
 * origin, arbitrary record name, or raw source image.
 */
export const tauriRealQaDraftBridge: NativeRealQaDraftBridge = {
  status: () =>
    invoke<NativeDraftResults["realqa_get_local_draft_status"]>(
      "realqa_get_local_draft_status",
    ),
  list: () =>
    invoke<NativeDraftResults["realqa_list_local_drafts"]>(
      "realqa_list_local_drafts",
    ),
  load: (draftId, composerSessionId) =>
    invoke<NativeDraftResults["realqa_load_local_draft"]>(
      "realqa_load_local_draft",
      { draftId, composerSessionId },
    ),
  save: (request) =>
    invoke<NativeDraftResults["realqa_save_local_draft"]>(
      "realqa_save_local_draft",
      { request },
    ),
  delete: (draftId, expectedRevision) =>
    invoke<NativeDraftResults["realqa_delete_local_draft"]>(
      "realqa_delete_local_draft",
      { draftId, expectedRevision },
    ),
  assertSubmissionAllowed: (draftId, expectedRevision) =>
    invoke<NativeDraftResults["realqa_assert_local_draft_submission_allowed"]>(
      "realqa_assert_local_draft_submission_allowed",
      { draftId, expectedRevision },
    ),
};
