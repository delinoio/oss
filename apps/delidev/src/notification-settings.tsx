import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { SettingsTaskDismissButton } from "./settings-task";
import { ownedMessage } from "./localization";
import { copy, useLocale } from "./localization";
import { SettingsTaskContext } from "./settings-task-context";
import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize } from "./settings-task";
import { useContext, useEffect, useId, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { InboxQuery, newRequestId, type NotificationPreferences } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeNotificationSettings } from "./notification-presentation";
import { useSettingsOpening } from "./settings-lifetime";
import { useSidebarDrawerOpen } from "./sidebar-context";
import { ToastKind, useNotifications } from "./toast-notifications";
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
  useLocale();
  const opening = useSettingsOpening();
  const drawerOpen = useSidebarDrawerOpen();
  const ids = useId();
  const form = useRef<HTMLElement>(null), editorForm = useRef<HTMLFormElement>(null);
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
    const ownsFocus = editorForm.current?.contains(focused) || (workflowFocus.current && (focused === document.body || focused === document.documentElement));
    focusIntent.current = focusAvailable.current && ownsFocus && editorForm.current && !covered(editorForm.current) ? FocusTarget.Edit : undefined;
    setDraft(undefined);
  };
  const value = current.data?.preferences;
  useEffect(() => {
    const discardOnFocus = (event: FocusEvent) => {
      if (event.target !== document.body && event.target !== document.documentElement) {
        focusIntent.current = undefined;
        workflowFocus.current = Boolean(event.target instanceof Node && (editorForm.current?.contains(event.target) || form.current?.contains(event.target)));
      }
    };
    const discardOnPointer = (event: Event) => {
      if (focusIntent.current === FocusTarget.Edit && event.target !== edit.current) focusIntent.current = undefined;
      if (!(event.target instanceof Node && (editorForm.current?.contains(event.target) || form.current?.contains(event.target)))) workflowFocus.current = false;
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
  }, [active, opening, drawerOpen, draft, current.error, current.isFetching]);
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(draft));
    return () => onWorkflowReadyChange?.(false);
  }, [draft, onWorkflowReadyChange]);
  const preferences = <section aria-labelledby={`${ids}-preferences`} data-settings-search-target="notification-preferences" data-settings-search-pending={current.isPending && !current.error ? "true" : undefined} id={`${ids}-form`} ref={form} className="notification-preferences">
      <div className="notification-section-heading"><h2 id={`${ids}-preferences`}>{copy("notification-settings.notifyThisClientAbout_db8955")}</h2>{value ? <button ref={edit} type="button" disabled={Boolean(current.error) || current.isFetching} onClick={() => { focusIntent.current = FocusTarget.FirstCheckbox; setDraft({ ...value }); }}>{copy("notification-settings.editNotificationPreferences_b2aceb")}</button> : null}</div>
      <Problem error={current.error} actions={current.error ? <button type="button" disabled={!active || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} />
      {current.error && current.data?.preferences ? <p role="status">{copy("notification-settings.notificationPreferencesCouldNotBeRefreshed_4e5bdf")}</p> : null}
      {!value ? <>{current.isPending && !current.error ? <p role="status">{copy("notification-settings.loadingNotificationPreferences_960e8d")}</p> : null}<p>{copy("notification-settings.notificationPreferencesAreUnavailableUntilThis_5ff119")}</p></> : <fieldset aria-labelledby={`${ids}-preferences`}>
        <div data-settings-search-target="notification-questions" className="notification-row"><div><label>{copy("notification-settings.questionsAndApprovalRequests_e6c1b4")}</label><p id={`${ids}-interaction-help`}>{copy("notification-settings.whenASessionNeedsYourAnswer_b52af9")}</p></div><span className="notification-value">{value.interactions ? copy("notification-settings.enabled_92c1cd") : copy("notification-settings.disabled_75081b")}</span></div>
        <div data-settings-search-target="notification-outcomes" className="notification-row"><div><label>{copy("notification-settings.executionCompletionFailureAndInterruption_275b47")}</label><p id={`${ids}-terminal-help`}>{copy("notification-settings.whenAnExecutionSucceedsFailsOr_fae45f")}</p></div><span className="notification-value">{value.terminals ? copy("notification-settings.enabled_92c1cd") : copy("notification-settings.disabled_75081b")}</span></div>
      </fieldset>}
    </section>;
  return <section className="notification-settings">{showCategoryIntro ? <div className="notification-intro"><h1>{copy("notification-settings.notifications_788011")}</h1><p>{copy("notification-settings.chooseWhichUpdatesThisClientReceives_16bc3a")}</p><p className="notification-scope">{copy("notification-settings.forThisClientOnTheSelected_c0d140")}</p></div> : null}
    <NativeNotificationSettings active={active} />
    {preferences}
    {draft ? <SettingsTaskDialog title={copy("notification-settings.editNotificationPreferences_b2aceb")} size={SettingsDialogSize.Form} close={finishEdit}><NotificationPreferencesEditor initial={draft} active={active} ids={`${ids}-editor`} form={editorForm} firstCheckbox={firstCheckbox} finishEdit={finishEdit} /></SettingsTaskDialog> : null}
    <aside className="notification-inbox-guidance"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 4h16l2 12v4H2v-4L4 4Zm-2 12h6l2 3h4l2-3h6" /></svg><div><p>{copy("notification-settings.inboxRequestsStayAvailableEvenWhen_09c08f")}</p><p>{copy("notification-settings.openingANotificationNeverMarksAn_4f219c")}</p></div></aside>
    <Disclosure density={DisclosureDensity.Settings} className="notification-delivery"><DisclosureSummary data-settings-search-target="notification-delivery">{copy("notification-settings.aboutNotificationDelivery_e8b4e9")}</DisclosureSummary><p>{copy("notification-settings.aSubmittedNotificationDoesNotProve_0edca6")}</p><p>{copy("notification-settings.readingAnInboxItemNeverAnswers_3d1331")}</p></Disclosure>
  </section>;
}

function NotificationPreferencesEditor({ initial, active, ids, form, firstCheckbox, finishEdit }: {
  initial: NotificationPreferences; active: boolean; ids: string;
  form: RefObject<HTMLFormElement | null>; firstCheckbox: RefObject<HTMLInputElement | null>; finishEdit: () => void;
}) {
  useLocale();
  const client = useQueryClient(), notifications = useNotifications();
  const current = useQuery(InboxQuery.getNotificationPreferences, {}, { enabled: active, refetchInterval: active ? 5000 : false });
  const [draft, setDraft] = useState(initial);
  const task = useContext(SettingsTaskContext), focused = useRef(false);
  useLayoutEffect(() => {
    if (!focused.current && task?.actions && firstCheckbox.current) { focused.current = true; firstCheckbox.current.focus(); }
  }, [task?.actions, firstCheckbox]);
  const mutation = useRetainedMutation("notification-preferences", InboxQuery.setNotificationPreferences, (_result, request) => { void client.invalidateQueries({ refetchType: "active" }); finishEdit(); notifications.notify({ kind: ToastKind.Success, message: ownedMessage("notification-settings.savedToast"), id: request.requestId }); });
  const stale = Boolean(draft && current.data?.preferences && current.data.preferences.revision !== draft.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const value = draft ?? current.data?.preferences;
  return <form id={`${ids}-form`} ref={form} className="notification-preferences" onSubmit={(event) => { event.preventDefault(); if (!draft || blocked || stale || current.error || current.isFetching) return; void mutation.send({ requestId: newRequestId(), preferences: draft }); }}>
      <div className="notification-section-heading"><h2 id={`${ids}-preferences`}>{copy("notification-settings.notifyThisClientAbout_db8955")}</h2></div>
      <Problem error={current.error} actions={current.error ? <button type="button" disabled={!active || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} /><Problem error={mutation.error} />
      {current.error && current.data?.preferences ? <p role="status">{copy("notification-settings.notificationPreferencesCouldNotBeRefreshed_4e5bdf")}</p> : null}
      {!value ? <>{current.isPending && !current.error ? <p role="status">{copy("notification-settings.loadingNotificationPreferences_960e8d")}</p> : null}<p>{copy("notification-settings.notificationPreferencesAreUnavailableUntilThis_5ff119")}</p></> : <fieldset disabled={blocked} aria-labelledby={`${ids}-preferences`}>
        <div className="notification-row"><div><label htmlFor={draft ? `${ids}-interactions` : undefined}>{copy("notification-settings.questionsAndApprovalRequests_e6c1b4")}</label><p id={`${ids}-interaction-help`}>{copy("notification-settings.whenASessionNeedsYourAnswer_b52af9")}</p></div>{draft ? <input ref={firstCheckbox} id={`${ids}-interactions`} type="checkbox" aria-describedby={`${ids}-interaction-help`} checked={draft.interactions} onChange={(event) => setDraft({ ...draft, interactions: event.target.checked })} /> : <span className="notification-value">{value.interactions ? copy("notification-settings.enabled_92c1cd") : copy("notification-settings.disabled_75081b")}</span>}</div>
        <div className="notification-row"><div><label htmlFor={draft ? `${ids}-terminals` : undefined}>{copy("notification-settings.executionCompletionFailureAndInterruption_275b47")}</label><p id={`${ids}-terminal-help`}>{copy("notification-settings.whenAnExecutionSucceedsFailsOr_fae45f")}</p></div>{draft ? <input id={`${ids}-terminals`} type="checkbox" aria-describedby={`${ids}-terminal-help`} checked={draft.terminals} onChange={(event) => setDraft({ ...draft, terminals: event.target.checked })} /> : <span className="notification-value">{value.terminals ? copy("notification-settings.enabled_92c1cd") : copy("notification-settings.disabled_75081b")}</span>}</div>
      </fieldset>}
      {mutation.busy ? <p role="status">{copy("notification-settings.savingNotificationPreferences_e709d9")}</p> : null}
      {stale ? <p role="alert">{copy("notification-settings.thesePreferencesChangedElsewhereYourDraft_eb11d4")}</p> : null}
      <SettingsTaskActions form={`${ids}-form`}>{draft ? <><button className="primary" disabled={blocked || stale || Boolean(current.error) || current.isFetching}>{copy("notification-settings.saveNotificationPreferences_c2c2b6")}</button><SettingsTaskDismissButton type="button" data-settings-task-cancel disabled={blocked} onClick={finishEdit}>{copy("notification-settings.cancelNotificationEdit_d3de52")}</SettingsTaskDismissButton></> : null}
        {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("notification-settings.retryTheSameNotificationPreferences_944448")}</button> : null}
      </SettingsTaskActions>
    </form>;
}
