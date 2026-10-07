// SPDX-License-Identifier: Apache-2.0
import { LocalizedText, copy, useLocale, formatTimestamp } from "./localization";
import { statusLabel } from "./product-status";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, SubscriptionServiceId, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { ChatGPTAccountDeletion, useAccountDeletionCompletion } from "./account-deletion";
import { serviceAccount } from "./subscription-resource";
import "./api-account.css";

interface DeletionProps { initial: Resource; active?: boolean; deleted: () => void; close: () => void }
export function ConfigurationDeletion({ active = true, ...props }: DeletionProps) {
  useLocale();
  const closeTask = useCloseSettingsTask(props.close);
  const key = props.initial.id;
  return serviceAccount(props.initial, undefined, SubscriptionServiceId.ChatGPT)
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
  return <section className={initial.kind === EntityKind.PROJECT ? "project-deletion" : isApiEntry ? "api-entry-workflow" : undefined}>{isApiEntry ? <header className="api-entry-heading"><h2>{copy("configuration-actions.deleteEntry_e570cd")}</h2><p>{resourceName(initial)}</p><p className="api-entry-scope">{copy("configuration-actions.savedOnTheSelectedServer_93dbee")}</p></header> : <h3><LocalizedText id="configuration-actions.delete_cac286" components={{ s0: <>{resourceName(initial)}</> }} /></h3>}<p>{copy("configuration-actions.thisDeletesItsSavedConfigurationRetained_8aa78e")}</p>{initial.kind === EntityKind.PROJECT || initial.kind === EntityKind.AGENT ? <p>{copy("configuration-actions.schedulesUsingThisConfigurationWillBe_e67d03")}</p> : null}{initial.kind === EntityKind.ACCOUNT ? <p>{document(initial).type === "api" ? copy("configuration-actions.disconnectTheEntryAndFinishCredential_ad3caf") : copy("configuration-actions.disconnectTheAccountAndFinishCredential_9de80a")}</p> : null}{initial.kind === EntityKind.ACCOUNT ? <p>{copy("configuration-actions.browserProfileCleanupRemainsPendingOn_a24d79")}</p> : null}<Problem error={mutation.error} /><SettingsTaskActions className=""><button disabled={blocked} onClick={() => void mutation.send({ kind: initial.kind, mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } })}>{copy("configuration-actions.confirmConfigurationDeletion_5413bb")}</button>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("configuration-actions.retryTheSameDeletion_b32bf6")}</button> : null}<button data-settings-task-cancel disabled={blocked} onClick={closeTask}>{copy("configuration-actions.keepConfiguration_1210fc")}</button></SettingsTaskActions></section>;
}
export function RoutingPreview({ agent, active, close }: { agent: Resource; active: boolean; close: () => void }) {
  useLocale();
  const [project, setProject] = useState("");
  const result = useQuery(ConfigurationQuery.previewRouting, { agentId: agent.id, projectId: project }, { enabled: active });
  let route: Document | undefined;
  try { if (result.data) route = object(JSON.parse(new TextDecoder().decode(result.data.routeJson))); } catch { /* Invalid evidence stays unavailable. */ }
  return <section><header><h3><LocalizedText id="configuration-actions.accountRouting_9e0296" components={{ s0: <>{resourceName(agent)}</> }} /></h3><button onClick={close}>{copy("configuration-actions.backToAgentWorkers_b825b4")}</button></header><p>{copy("configuration-actions.thisPreviewIsReadOnlyAnd_dd9bb1")}</p><ResourceChoice label={copy("configuration-actions.project_985959")} kind={EntityKind.PROJECT} value={project} change={setProject} active={active} /><p>{project ? copy("configuration-actions.usingTheSelectedProjectSRestrictions_f4c29e") : copy("configuration-actions.generalChatNoProjectRestrictions_abff59")}</p><button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("configuration-actions.refreshRoutingPreview_3b8c83")}</button><Problem error={result.error} />{route ? <><p><LocalizedText id="configuration-actions.policySelectedAccount_a559e9" components={{ s0: <>{text(route.policy)}</>, s1: <>{text(route.selected) || copy("configuration-actions.extra.af7eed90a41e")}</> }} /></p>{route.fallback === true ? <p>{copy("configuration-actions.insufficientComparableQuotaEvidenceTheServer_95890b")}</p> : null}<ul>{items(route.candidates).map(object).map((candidate) => <li key={text(candidate.id)}><p>{text(candidate.id)} · {statusLabel(text(candidate.eligibility))}</p><p><LocalizedText id="configuration-actions.weightQuota_fe7185" components={{ s0: <>{String(candidate.weight)}</>, s1: <>{statusLabel(text(candidate.quota_state))}</>, s2: <>{typeof candidate.score === "number" ? copy("configuration-actions.score_aa9007", { v0: candidate.score }) : ""}</>, s3: <>{text(candidate.reset_at) ? copy("configuration-actions.reset_0cc57c", { v0: formatTimestamp(text(candidate.reset_at)) }) : ""}</> }} /></p></li>)}</ul></> : result.data ? <p role="alert">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : null}</section>;
}
