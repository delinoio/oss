// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDismissButton } from "./settings-task";
import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { useState } from "react";
import { ConfigurationQuery, EntityKind, SubscriptionServiceId, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { ChatGPTAccountDeletion, useAccountDeletionCompletion } from "./account-deletion";
import { ApiAccountDeletion } from "./api-account-deletion";
import { serviceAccount } from "./subscription-resource";
import "./api-account.css";

interface DeletionProps { initial: Resource; active?: boolean; deleted: () => void; close: () => void }
export function ConfigurationDeletion({ active = true, ...props }: DeletionProps) {
  useLocale();
  const closeTask = useCloseSettingsTask(props.close);
  const key = props.initial.id;
  if (props.initial.kind === EntityKind.ACCOUNT && document(props.initial).type === "api") return <ApiAccountDeletion key={key} {...props} active={active} close={closeTask} />;
  return serviceAccount(props.initial, undefined, SubscriptionServiceId.ChatGPT) || serviceAccount(props.initial, undefined, SubscriptionServiceId.Claude)
    ? <ChatGPTAccountDeletion key={key} {...props} active={active} />
    : <ConfigurationDeletionRequest key={key} {...props} active={active} close={closeTask} />;
}
function ConfigurationDeletionRequest({ initial, active, deleted, close }: DeletionProps & { active: boolean }) {
  useLocale();
  const closeTask = close;
  const isApiEntry = initial.kind === EntityKind.ACCOUNT && document(initial).type === "api";
  const [accepted, setAccepted] = useState(false);
  const mutation = useRetainedMutation(`configuration-delete:${initial.kind}:${initial.id}`, ConfigurationQuery.deleteConfiguration, () => { if (initial.kind === EntityKind.ACCOUNT) setAccepted(true); else deleted(); });
  useAccountDeletionCompletion(accepted, active, deleted);
  if (accepted) return null;
  const blocked = !active || mutation.busy || mutation.uncertain;
  return <section className={initial.kind === EntityKind.PROJECT ? "project-deletion" : isApiEntry ? "api-entry-workflow" : undefined}>{isApiEntry ? <header className="api-entry-heading"><h2>{copy("configuration-actions.deleteEntry_e570cd")}</h2><p>{resourceName(initial)}</p><p className="api-entry-scope">{copy("configuration-actions.savedOnTheSelectedServer_93dbee")}</p></header> : <h3><LocalizedText id="configuration-actions.delete_cac286" components={{ s0: <>{resourceName(initial)}</> }} /></h3>}<p>{copy("configuration-actions.thisDeletesItsSavedConfigurationRetained_8aa78e")}</p>{initial.kind === EntityKind.PROJECT || initial.kind === EntityKind.AGENT ? <p>{copy("configuration-actions.schedulesUsingThisConfigurationWillBe_e67d03")}</p> : null}{initial.kind === EntityKind.ACCOUNT ? <p>{document(initial).type === "api" ? copy("configuration-actions.disconnectTheEntryAndFinishCredential_ad3caf") : copy("configuration-actions.disconnectTheAccountAndFinishCredential_9de80a")}</p> : null}{initial.kind === EntityKind.ACCOUNT ? <p>{copy("configuration-actions.browserProfileCleanupRemainsPendingOn_a24d79")}</p> : null}<Problem error={mutation.error} /><SettingsTaskActions className=""><button disabled={blocked} onClick={() => void mutation.send({ kind: initial.kind, mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } })}>{copy("configuration-actions.confirmConfigurationDeletion_5413bb")}</button>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("configuration-actions.retryTheSameDeletion_b32bf6")}</button> : null}<SettingsTaskDismissButton data-settings-task-cancel disabled={blocked} onClick={closeTask}>{copy("configuration-actions.keepConfiguration_1210fc")}</SettingsTaskDismissButton></SettingsTaskActions></section>;
}
export { RoutingPreview } from "./routing-preview";
