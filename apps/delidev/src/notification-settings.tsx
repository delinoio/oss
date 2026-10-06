import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize } from "./settings-task";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { InboxQuery, newRequestId, type NotificationPreferences } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeNotificationSettings } from "./notification-presentation";
import { useSettingsOpening } from "./settings-lifetime";
import { useSidebarDrawerOpen } from "./sidebar-context";
import "./notification-settings.css";

enum FocusTarget { FirstCheckbox, Edit }

function covered(node: HTMLElement) {
  return Boolean(node.closest("[hidden], [inert]")) || Array.from(document.querySelectorAll('dialog[open]:not([role="region"]), [role="dialog"]:not(dialog)')).some((dialog) => {
    if (dialog.contains(node)) return false;
    const style = getComputedStyle(dialog);
    return !dialog.closest("[hidden], [inert]") && style.display !== "none" && style.visibility !== "hidden";
  });
}

export function NotificationSettings({ active, showCategoryIntro = true, onWorkflowReadyChange }: { active: boolean; showCategoryIntro?: boolean; onWorkflowReadyChange?: (active: boolean) => void }) {
  const client = useQueryClient();
  const opening = useSettingsOpening();
  const drawerOpen = useSidebarDrawerOpen();
  const ids = useId();
  const form = useRef<HTMLFormElement>(null);
  const firstCheckbox = useRef<HTMLInputElement>(null);
  const edit = useRef<HTMLButtonElement>(null);
  const focusIntent = useRef<FocusTarget | undefined>(undefined);
  const workflowFocus = useRef(false);
  const focusAvailable = useRef(false);
  focusAvailable.current = active && !drawerOpen && !opening?.disposed;
  const current = useQuery(InboxQuery.getNotificationPreferences, {}, { enabled: active, refetchInterval: active ? 5000 : false });
  const [draft, setDraft] = useState<NotificationPreferences>();
  const finishEdit = () => {
    // Capture workflow ownership before React removes the focused form control.
    // A later refetch may enable Edit, but must never reclaim deliberately moved focus.
    const focused = document.activeElement;
    const ownsFocus = form.current?.contains(focused) || (workflowFocus.current && (focused === document.body || focused === document.documentElement));
    focusIntent.current = focusAvailable.current && ownsFocus && form.current && !covered(form.current) ? FocusTarget.Edit : undefined;
    setDraft(undefined);
  };
  const mutation = useRetainedMutation("notification-preferences", InboxQuery.setNotificationPreferences, () => { finishEdit(); void client.invalidateQueries({ refetchType: "active" }); });
  const stale = Boolean(draft && current.data?.preferences && current.data.preferences.revision !== draft.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const value = draft ?? current.data?.preferences;
  useEffect(() => {
    const discardOnFocus = (event: FocusEvent) => {
      if (event.target !== document.body && event.target !== document.documentElement) {
        focusIntent.current = undefined;
        workflowFocus.current = Boolean(event.target instanceof Node && form.current?.contains(event.target));
      }
    };
    const discardOnPointer = (event: Event) => {
      if (focusIntent.current === FocusTarget.Edit && event.target !== edit.current) focusIntent.current = undefined;
      if (!(event.target instanceof Node && form.current?.contains(event.target))) workflowFocus.current = false;
    };
    const discardOnBlur = () => { focusIntent.current = undefined; workflowFocus.current = false; };
    const discardWhenCovered = () => { if (form.current && covered(form.current)) { focusIntent.current = undefined; workflowFocus.current = false; } };
    const observer = new MutationObserver(discardWhenCovered);
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["open", "hidden", "inert", "class", "role"] });
    document.addEventListener("focusin", discardOnFocus);
    document.addEventListener("pointerdown", discardOnPointer);
    window.addEventListener("blur", discardOnBlur);
    return () => { focusIntent.current = undefined; observer.disconnect(); document.removeEventListener("focusin", discardOnFocus); document.removeEventListener("pointerdown", discardOnPointer); window.removeEventListener("blur", discardOnBlur); };
  }, []);
  useLayoutEffect(() => {
    const intent = focusIntent.current;
    if (intent === undefined) return;
    const node = intent === FocusTarget.FirstCheckbox ? firstCheckbox.current : edit.current;
    if (!active || opening?.disposed || drawerOpen || !form.current?.isConnected || covered(form.current)) { focusIntent.current = undefined; return; }
    if (!node || node.disabled) return;
    focusIntent.current = undefined;
    node.focus({ preventScroll: true });
  }, [active, opening, drawerOpen, draft, blocked, current.error, current.isFetching]);
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(draft || mutation.busy || mutation.uncertain));
    return () => onWorkflowReadyChange?.(false);
  }, [draft, mutation.busy, mutation.uncertain, onWorkflowReadyChange]);
  const preferences = <form id={`${ids}-form`} ref={form} className="notification-preferences" onSubmit={(event) => { event.preventDefault(); if (!draft || blocked || stale || current.error || current.isFetching) return; void mutation.send({ requestId: newRequestId(), preferences: draft }); }}>
      <div className="notification-section-heading"><h2 id={`${ids}-preferences`}>Notify this client about</h2>{value && !draft ? <button ref={edit} type="button" disabled={blocked || Boolean(current.error) || current.isFetching} onClick={() => { focusIntent.current = FocusTarget.FirstCheckbox; setDraft({ ...value }); }}>Edit notification preferences</button> : null}</div>
      <Problem error={current.error} /><Problem error={mutation.error} />
      {current.error && current.data?.preferences ? <p role="status">Notification preferences could not be refreshed. The displayed values may be out of date.</p> : null}
      {!value ? <>{current.isPending && !current.error ? <p role="status">Loading notification preferences…</p> : null}<p>Notification preferences are unavailable until this server can be read.</p></> : <fieldset disabled={blocked} aria-labelledby={`${ids}-preferences`}>
        <div className="notification-row"><div><label htmlFor={draft ? `${ids}-interactions` : undefined}>Questions and approval requests</label><p id={`${ids}-interaction-help`}>When a session needs your answer or approval.</p></div>{draft ? <input ref={firstCheckbox} id={`${ids}-interactions`} type="checkbox" aria-describedby={`${ids}-interaction-help`} checked={draft.interactions} onChange={(event) => setDraft({ ...draft, interactions: event.target.checked })} /> : <span className="notification-value">{value.interactions ? "Enabled" : "Disabled"}</span>}</div>
        <div className="notification-row"><div><label htmlFor={draft ? `${ids}-terminals` : undefined}>Execution completion, failure and interruption</label><p id={`${ids}-terminal-help`}>When an execution succeeds, fails or stops.</p></div>{draft ? <input id={`${ids}-terminals`} type="checkbox" aria-describedby={`${ids}-terminal-help`} checked={draft.terminals} onChange={(event) => setDraft({ ...draft, terminals: event.target.checked })} /> : <span className="notification-value">{value.terminals ? "Enabled" : "Disabled"}</span>}</div>
      </fieldset>}
      {mutation.busy ? <p role="status">Saving notification preferences…</p> : null}
      {stale ? <p role="alert">These preferences changed elsewhere. Your draft is retained. Cancel this edit and reopen the current preferences before saving.</p> : null}
      <SettingsTaskActions form={`${ids}-form`}>{draft ? <><button className="primary" disabled={blocked || stale || Boolean(current.error) || current.isFetching}>Save notification preferences</button><button type="button" disabled={blocked} onClick={finishEdit}>Cancel notification edit</button></> : null}
        {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same notification preferences</button> : null}
      </SettingsTaskActions>
    </form>;
  return <section className="notification-settings">{showCategoryIntro ? <div className="notification-intro"><h1>Notifications</h1><p>Choose which updates this client receives.</p><p className="notification-scope">For this client on the selected server</p></div> : null}
    <NativeNotificationSettings active={active} />
    {draft ? <><section aria-label="Saved notification preferences"><h2>Notify this client about</h2><div className="notification-row"><div><p>Questions and approval requests</p><p>When a session needs your answer or approval.</p></div><span className="notification-value">{current.data?.preferences?.interactions ? "Enabled" : "Disabled"}</span></div><div className="notification-row"><div><p>Execution completion, failure and interruption</p><p>When an execution succeeds, fails or stops.</p></div><span className="notification-value">{current.data?.preferences?.terminals ? "Enabled" : "Disabled"}</span></div></section><SettingsTaskDialog title="Edit notification preferences" size={SettingsDialogSize.Form} retained={blocked} close={finishEdit}>{preferences}</SettingsTaskDialog></> : preferences}
    <aside className="notification-inbox-guidance"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 4h16l2 12v4H2v-4L4 4Zm-2 12h6l2 3h4l2-3h6" /></svg><div><p>Inbox requests stay available even when notifications are off.</p><p>Opening a notification never marks an item read, answers a request, approves work or resumes a session.</p></div></aside>
    <details className="notification-delivery"><summary>About notification delivery</summary><p>A submitted notification does not prove that its banner was displayed.</p><p>Reading an inbox item never answers it.</p></details>
  </section>;
}
