import { EntityKind, isEntityId, type InboxView } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";

export function currentInboxSource(view: InboxView, id: string): boolean {
  const { entry, session, interaction } = view;
  if(entry?.kind===EntityKind.INBOX&&entry.id===id&&document(entry).source==="operational"){
    const v=object(document(entry).operational);
    if(session||interaction||entry.sessionId||entry.projectId||!isEntityId(text(document(entry).source_id))||!/^([1-9][0-9]*)$/.test(text(v.sequence))||!Number.isFinite(Date.parse(text(v.observed_at))))return false;
    const k=text(v.kind);
    if(k==="worker-unavailable"||k==="worker-available")return isEntityId(text(v.machine_id))&&isEntityId(text(v.instance_id))&&!v.account_id&&!v.occurrence_id&&(!view.machine||view.machine.kind===EntityKind.MACHINE&&view.machine.id===v.machine_id)&&!view.account&&!view.occurrence;
    if(k==="quota-exhausted")return isEntityId(text(v.account_id))&&isEntityId(text(v.connection_id))&&!v.machine_id&&!v.occurrence_id&&(!view.account||view.account.kind===EntityKind.ACCOUNT&&view.account.id===v.account_id)&&!view.machine&&!view.occurrence;
    if(["schedule-start-failed","schedule-server-offline","schedule-worker-offline"].includes(k))return isEntityId(text(v.occurrence_id))&&!v.machine_id&&!v.account_id&&(!view.occurrence||view.occurrence.kind===EntityKind.OCCURRENCE&&view.occurrence.id===v.occurrence_id)&&!view.machine&&!view.account;
    return false;
  }
 if (entry?.kind===EntityKind.INBOX && entry.id===id && document(entry).source==="subscription-recovery") { const recovery=object(document(entry).recovery);return !session && !interaction && !entry.sessionId && !entry.projectId && view.account?.kind===EntityKind.ACCOUNT && view.account.id===recovery.account_id && isEntityId(text(recovery.connection_id)); }
  if (!entry || entry.kind !== EntityKind.INBOX || entry.id !== id || !isEntityId(entry.sessionId) || !session || session.kind !== EntityKind.SESSION || session.id !== entry.sessionId || entry.projectId !== session.projectId) return false;
  const data = document(entry);
  if (data.source === "execution-terminal") return !interaction;
  return data.source === "interaction" && Boolean(interaction && interaction.kind === EntityKind.INTERACTION && interaction.id === data.source_id && interaction.sessionId === entry.sessionId && interaction.projectId === entry.projectId);
}

export function inboxResponseCurrent(view: InboxView): boolean {
  const session = document(view.session), interaction = document(view.interaction);
  return Boolean(view.entry && currentInboxSource(view, view.entry.id) && session.archive === "active" && session.dispatch === "claimed" && session.recovery === "none" && isEntityId(text(session.active_execution_id)) && session.active_execution_id === interaction.execution_id);
}
