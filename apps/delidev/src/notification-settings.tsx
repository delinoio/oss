import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { InboxQuery, newRequestId, type NotificationPreferences } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeNotificationSettings } from "./notification-presentation";
import { useSidebarDrawerOpen } from "./sidebar-context";

enum NotificationFocus { Editor, Return }

export function NotificationSettings({ active, showCategoryIntro = true, onWorkflowReadyChange }: { active: boolean; showCategoryIntro?: boolean; onWorkflowReadyChange?: (active: boolean) => void }) {
  const client = useQueryClient();
  const current = useQuery(InboxQuery.getNotificationPreferences, {}, { enabled: active, refetchInterval: active ? 5000 : false });
  const [draft, setDraft] = useState<NotificationPreferences>();
  const root = useRef<HTMLElement>(null), firstCheckbox = useRef<HTMLInputElement>(null), editButton = useRef<HTMLButtonElement>(null);
  const focus = useRef<NotificationFocus | undefined>(undefined), ownsFocus = useRef(false);
  const drawerOpen = useSidebarDrawerOpen();
  const finish = () => { focus.current = ownsFocus.current ? NotificationFocus.Return : undefined; setDraft(undefined); };
  const mutation = useRetainedMutation("notification-preferences", InboxQuery.setNotificationPreferences, () => { finish(); void client.invalidateQueries({ refetchType: "active" }); });
  const stale = Boolean(draft && current.data?.preferences && current.data.preferences.revision !== draft.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const value = draft ?? current.data?.preferences;
  useEffect(() => {
    // A return intent can wait for refetch, but never survive a deliberate
    // transfer to another control, navigation, dialog/window or inactive category.
    const discard = () => { focus.current = undefined; ownsFocus.current = false; };
    const transferred = (event: Event) => {
      if (!root.current?.contains(event.target as Node) || (focus.current === NotificationFocus.Return && event.target !== editButton.current)) discard();
    };
    document.addEventListener("focusin", transferred);
    document.addEventListener("pointerdown", transferred);
    window.addEventListener("blur", discard);
    return () => { discard(); document.removeEventListener("focusin", transferred); document.removeEventListener("pointerdown", transferred); window.removeEventListener("blur", discard); };
  }, []);
  useLayoutEffect(() => {
    if (!active || drawerOpen || root.current?.closest("[inert], [hidden]")) { focus.current = undefined; ownsFocus.current = false; return; }
    const target = focus.current === NotificationFocus.Editor ? firstCheckbox.current : focus.current === NotificationFocus.Return ? editButton.current : undefined;
    if (target && !target.disabled) { focus.current = undefined; target.focus(); }
  });
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(draft || mutation.busy || mutation.uncertain));
    return () => onWorkflowReadyChange?.(false);
  }, [draft, mutation.busy, mutation.uncertain, onWorkflowReadyChange]);
  return <section className="notification-settings" ref={root}>{showCategoryIntro ? <><h2>Desktop notifications</h2><p>These preferences belong to this client on the selected server. Inbox requests stay available when notifications are disabled or cannot be delivered.</p></> : null}
    <NativeNotificationSettings active={active} />
    <Problem error={current.error || mutation.error} />
    {current.error && current.data ? <p role="status">Refresh failed. Showing the last successfully loaded preferences.</p> : null}
    {current.isPending && !current.data ? <p role="status">Loading notification preferences…</p> : null}
    {value ? <form className="notification-preferences" onSubmit={(event) => { event.preventDefault(); if (!draft || blocked || stale || current.error || current.isFetching) return; void mutation.send({ requestId: newRequestId(), preferences: draft }); }}>
      {draft ? <fieldset disabled={blocked}><legend>Notify this client about</legend>
        <label className="checkbox"><input ref={firstCheckbox} type="checkbox" checked={value.interactions} onChange={(event) => setDraft({ ...value, interactions: event.target.checked })} />Questions and approval requests</label>
        <label className="checkbox"><input type="checkbox" checked={value.terminals} onChange={(event) => setDraft({ ...value, terminals: event.target.checked })} />Execution completion, failure and interruption</label>
      </fieldset> : <section aria-label="Saved notification preferences"><h2>Notify this client about</h2><dl>
        <div className="notification-saved-row"><dt>Questions and approval requests</dt><dd>{value.interactions ? "Enabled" : "Disabled"}</dd></div>
        <div className="notification-saved-row"><dt>Execution completion, failure and interruption</dt><dd>{value.terminals ? "Enabled" : "Disabled"}</dd></div>
      </dl></section>}
      {stale ? <p role="alert">These preferences changed elsewhere. Your draft is retained. Cancel this edit and reopen the current preferences before saving.</p> : null}
      <div className="actions">{draft ? <><button className="primary" disabled={blocked || stale || Boolean(current.error) || current.isFetching}>Save notification preferences</button><button type="button" disabled={blocked} onClick={finish}>Cancel notification edit</button></> : <button ref={editButton} className="primary" type="button" disabled={Boolean(current.error) || current.isFetching} onClick={() => { ownsFocus.current = true; focus.current = NotificationFocus.Editor; setDraft({ ...value }); }}>Edit notification preferences</button>}
        {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same notification preferences</button> : null}
      </div>
    </form> : !current.isPending ? <p>Notification preferences are unavailable until this server can be read.</p> : null}
    <div className="notification-guidance"><p>Inbox requests remain available when notifications are off.</p><p>Opening or reading a request never approves or resumes a session.</p></div>
    <details><summary>About notification delivery</summary><p>Reading an inbox item never answers it. A notification only opens its current request; it cannot approve or resume a session.</p><p>Permission is shared by DeliDev windows on this computer. Notification preferences below belong to this server connection. A submitted notification does not prove that its banner was displayed.</p></details>
  </section>;
}
