// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
import { JobState, type JobObservation } from "./jobs";
import { confirmedRepository } from "./repository-registration";
import { useSettingsOpening } from "./settings-lifetime";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, ownedMessage, useLocale } from "./localization";
import { ToastKind, useNotifications } from "./toast-notifications";
import type { Document } from "./documents";

/** Complete only the original existing-repository edit in its live opening. */
export function RepositoryEditCompletion({ jobId, repositoryId, submittedId, expectedRevision, state, output, observation, active, saved }: {
  jobId: string; repositoryId: string; submittedId?: string; expectedRevision?: bigint;
  state: string; output: Document; observation: JobObservation; active: boolean; saved: () => void;
}) {
  useLocale();
  const opening = useSettingsOpening(), notifications = useNotifications();
  const completed = useRef(false);
  const identity = confirmedRepository(output);
  const confirmed = state === JobState.Succeeded && observation.verified && !observation.pending && submittedId === repositoryId && expectedRevision !== undefined && identity?.id === repositoryId && identity.revision > expectedRevision;
  useEffect(() => {
    if (!active || opening?.disposed || !confirmed || completed.current) return;
    // Set before either callback: Strict Mode and repeated original observations
    // cannot publish another toast or close a successor task.
    completed.current = true;
    notifications.notify({ kind: ToastKind.Success, message: ownedMessage("settings.savedKind.REPOSITORY"), id: jobId });
    saved();
  }, [active, opening, confirmed, notifications, jobId, saved]);
  return state !== JobState.Succeeded || confirmed ? null : <>
    <p role="status">{copy("pull-requests.profileSaveUnverified")}</p>
    {!observation.retryPresented ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || observation.pending || Boolean(opening?.disposed)} onClick={observation.retry}>{copy("jobs.retryStatusRead")}</SettingsActionButton> : null}
  </>;
}
