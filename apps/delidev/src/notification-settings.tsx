import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { InboxQuery, newRequestId, type NotificationPreferences } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeNotificationSettings } from "./notification-presentation";

export function NotificationSettings({ active, showCategoryIntro = true }: { active: boolean; showCategoryIntro?: boolean }) {
  const client = useQueryClient();
  const current = useQuery(InboxQuery.getNotificationPreferences, {}, { enabled: active, refetchInterval: active ? 5000 : false });
  const [draft, setDraft] = useState<NotificationPreferences>();
  const mutation = useRetainedMutation("notification-preferences", InboxQuery.setNotificationPreferences, () => { setDraft(undefined); void client.invalidateQueries({ refetchType: "active" }); });
  const stale = Boolean(draft && current.data?.preferences && current.data.preferences.revision !== draft.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const value = draft ?? current.data?.preferences;
  return <section>{showCategoryIntro ? <><h2>Desktop notifications</h2><p>These preferences belong to this client on the selected server. Inbox requests stay available when notifications are disabled or cannot be delivered.</p></> : null}
    <NativeNotificationSettings active={active} />
    <Problem error={current.error || mutation.error} />
    {value ? <form onSubmit={(event) => { event.preventDefault(); if (!draft || blocked || stale || current.error || current.isFetching) return; void mutation.send({ requestId: newRequestId(), preferences: draft }); }}>
      <fieldset disabled={!draft || blocked}><legend>Notify this client about</legend>
        <label className="checkbox"><input type="checkbox" checked={value.interactions} onChange={(event) => setDraft({ ...value, interactions: event.target.checked })} />Questions and approval requests</label>
        <label className="checkbox"><input type="checkbox" checked={value.terminals} onChange={(event) => setDraft({ ...value, terminals: event.target.checked })} />Execution completion, failure and interruption</label>
      </fieldset>
      <p>Reading an inbox item never answers it. A notification only opens its current request; it cannot approve or resume a session.</p>
      {stale ? <p role="alert">These preferences changed elsewhere. Your draft is retained. Cancel this edit and reopen the current preferences before saving.</p> : null}
      <div className="actions">{draft ? <><button className="primary" disabled={blocked || stale || Boolean(current.error) || current.isFetching}>Save notification preferences</button><button type="button" disabled={blocked} onClick={() => setDraft(undefined)}>Cancel notification edit</button></> : <button type="button" disabled={Boolean(current.error) || current.isFetching} onClick={() => setDraft({ ...value })}>Edit notification preferences</button>}
        {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same notification preferences</button> : null}
      </div>
    </form> : <p>Notification preferences are unavailable until this server can be read.</p>}
  </section>;
}
