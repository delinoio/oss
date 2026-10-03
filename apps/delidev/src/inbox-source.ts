import { EntityKind, isEntityId, type InboxView } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";

export function currentInboxSource(view: InboxView, id: string): boolean {
  const { entry, session, interaction } = view;
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
