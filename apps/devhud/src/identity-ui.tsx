import { createContext, use, useEffect, useEffectEvent, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type Ref } from "react";
import type { Copy } from "./localization";
import { GitHubSettings } from "./github-settings-ui.tsx";
import type { GitHubProvider } from "./github-provider.ts";
import { nativeBridge, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary";
import { browserShell, LanguagePreference, normalizeApiOrigin, ThemePreference, type ExternalLinkTarget } from "./shell";
import { parseDevHudSettings, type DevHudSettingsV1 } from "./settings-contract";
import type { SettingsDiffEntry } from "./settings-diff";
import { findMappingOverlaps, type UrlRepositoryMapping } from "./url-mapping";

interface ApiEditorProps {
  readonly copy: Copy;
  readonly value: string;
  readonly inputRef?: Ref<HTMLInputElement>;
  readonly autoFocus?: boolean;
  readonly onApply: (value: string) => Promise<void>;
}

export function ApiOriginEditor({ copy, value, inputRef, autoFocus = false, onApply }: ApiEditorProps) {
  const [draft, setDraft] = useState(value);
  const [error, setError] = useState(false);
  useEffect(() => setDraft(value), [value]);
  const apply = async () => {
    const normalized = normalizeApiOrigin(draft);
    if (normalized === null) { setError(true); return; }
    setError(false);
    setDraft(normalized);
    await onApply(normalized);
  };
  return <div className="api-origin-editor">
    <label>{copy.apiOrigin}<input ref={inputRef} autoFocus={autoFocus} value={draft} onChange={(event) => setDraft(event.target.value)} aria-describedby="api-origin-security-warning api-origin-validation" /></label>
    <button type="button" onClick={() => void apply()} disabled={normalizeApiOrigin(draft) === normalizeApiOrigin(value)}>{copy.applyApiOrigin}</button>
    <p id="api-origin-security-warning" className="notice">{copy.customApiWarning}</p>
    <p>{copy.apiOriginHint}</p>
    {error && <p id="api-origin-validation" role="alert">{copy.invalidApiOrigin}</p>}
  </div>;
}

interface IdentityProps {
  readonly copy: Copy;
  readonly apiOrigin: string;
  readonly onApiOrigin: (value: string) => Promise<void>;
  readonly onComplete: () => void;
}

export function FirstRunIdentity({ copy, apiOrigin, onApiOrigin, onComplete }: IdentityProps) {
  const identity = useIdentitySettings();
  const [actionError, setActionError] = useState(false);
  useEffect(() => {
    if (identity.status === "authenticated" || identity.status === "blocked" || identity.status === "deletion-pending") onComplete();
  }, [identity.status, onComplete]);
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h1>{copy.accountTitle}</h1>
    <p>{copy.firstRunSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} autoFocus onApply={onApiOrigin} />
    <div className="actions">
      <button onClick={() => { setActionError(false); void identity.signIn().catch(() => setActionError(true)); }} disabled={identity.status === "starting" || identity.bootstrap === null || identity.signInPending}>{copy.signIn}</button>
      <button onClick={identity.continueLocally}>{copy.continueLocally}</button>
    </div>
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
    {actionError && <p role="alert">{copy.signInFailed}</p>}
  </>;
}

interface AccountIdentityProps {
  readonly copy: Copy;
  readonly apiOrigin: string;
  readonly inputRef: Ref<HTMLInputElement>;
  readonly onApiOrigin: (value: string) => Promise<void>;
}

export function AccountIdentity({ copy, apiOrigin, inputRef, onApiOrigin }: AccountIdentityProps) {
  const identity = useIdentitySettings();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [actionError, setActionError] = useState(false);
  const deleteTrigger = useRef<HTMLButtonElement>(null);
  const deleteDialog = useRef<HTMLElement>(null);
  const cancelDelete = useRef<HTMLButtonElement>(null);
  const invoke = (action: () => Promise<void>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const closeDeleteConfirmation = () => {
    setConfirmDelete(false);
    requestAnimationFrame(() => deleteTrigger.current?.focus());
  };
  useEffect(() => {
    if (!confirmDelete) return;
    cancelDelete.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") closeDeleteConfirmation(); };
    addEventListener("keydown", closeOnEscape);
    return () => removeEventListener("keydown", closeOnEscape);
  }, [confirmDelete]);
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h2>{copy.accountTitle}</h2>
    <p>{copy.accountSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} inputRef={inputRef} onApply={onApiOrigin} />
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
    {(identity.status === "signed-out" || identity.status === "guest") && <button onClick={() => invoke(identity.signIn)} disabled={identity.bootstrap === null || identity.signInPending}>{copy.signIn}</button>}
    {identity.status === "authenticated" && identity.accountError && <section className="notice" role="alert"><p>{copy.accountLoadFailed}</p><code>{`account-connect-${identity.accountError.code}`}</code>{identity.accountError.correlationId && <> {copy.correlationId}: <code>{identity.accountError.correlationId}</code></>}<button onClick={() => void identity.retryAccount()}>{copy.retry}</button></section>}
    {identity.status === "authenticated" && !identity.accountError && identity.account === null && <p role="status">{copy.loadingAccount}</p>}
    {identity.status === "authenticated" && !identity.accountError && identity.account !== null && <section className="account-session" aria-label={copy.signedInSession}>
      <p>{identity.account.displayName || identity.account.email || copy.signedIn}</p>
      <div className="actions"><button onClick={() => invoke(identity.logout)}>{copy.logout}</button><button ref={deleteTrigger} className="danger" onClick={() => setConfirmDelete(true)}>{copy.deleteAccount}</button></div>
    </section>}
    {identity.status === "blocked" && <section className="notice" role="status"><h3>{copy.blockedTitle}</h3><p>{copy.blockedSummary}</p><p>{copy.blockedLocalHint}</p><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></section>}
    {identity.status === "deletion-pending" && <section className="notice" role="status"><h3>{copy.deletionPendingTitle}</h3><p>{copy.deletionPendingSummary}</p>{identity.account?.recoverableUntil && <p>{copy.recoverableUntil}: {new Date(Number(identity.account.recoverableUntil.seconds) * 1000).toLocaleString()}</p>}<div className="actions"><button onClick={() => invoke(identity.restoreAccount)}>{copy.restoreAccount}</button><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></div></section>}
    {identity.status === "deletion-pending" && identity.deletionCleanupFailed && <section className="notice" role="alert"><p>{copy.accountActionFailed}</p><button onClick={() => void identity.retryDeletionCleanup()}>{copy.retry}</button></section>}
    {confirmDelete && identity.status === "authenticated" && !identity.accountError && identity.account !== null && <section ref={deleteDialog} className="confirmation" role="alertdialog" aria-modal="true" aria-labelledby="delete-account-title" onKeyDown={(event) => trapDialogFocus(event, deleteDialog.current)}><h3 id="delete-account-title">{copy.deleteAccountConfirmTitle}</h3><p>{copy.deleteAccountConfirmSummary}</p><div className="actions"><button className="danger" onClick={() => { closeDeleteConfirmation(); invoke(identity.deleteAccount); }}>{copy.deleteAccount}</button><button ref={cancelDelete} onClick={closeDeleteConfirmation}>{copy.cancel}</button></div></section>}
    {actionError && <p role="alert">{copy.accountActionFailed}</p>}
  </>;
}

export function SynchronizedAppearanceBoundary({ onAppearance }: { readonly onAppearance: (appearance: DevHudSettingsV1["appearance"]) => void }) {
  const identity = useIdentitySettings();
  const applyAppearance = useEffectEvent(onAppearance);
  useEffect(() => {
    applyAppearance(identity.settings.appearance);
  }, [identity.settings.appearance.language, identity.settings.appearance.theme]);
  return null;
}

interface UrlMappingDraftValue {
  readonly draft: UrlRepositoryMapping[];
  readonly setDraft: (draft: UrlRepositoryMapping[] | ((current: UrlRepositoryMapping[]) => UrlRepositoryMapping[])) => void;
  readonly invalid: boolean;
  readonly setInvalid: (invalid: boolean) => void;
  readonly saved: boolean;
  readonly setSaved: (saved: boolean) => void;
  readonly dirty: boolean;
  readonly setDirty: (dirty: boolean) => void;
  readonly saving: boolean;
  readonly setSaving: (saving: boolean) => void;
  readonly priorityDrafts: Record<string, string>;
  readonly setPriorityDrafts: (drafts: Record<string, string> | ((current: Record<string, string>) => Record<string, string>)) => void;
  readonly reset: () => void;
}

const UrlMappingDraftContext = createContext<UrlMappingDraftValue | null>(null);

export function UrlMappingDraftProvider({ children }: { readonly children: ReactNode }) {
  const identity = useIdentitySettings();
  const [draft, setDraft] = useState<UrlRepositoryMapping[]>(() => [...identity.settings.urlMappings]);
  const [invalid, setInvalid] = useState(false);
  const [saved, setSaved] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [priorityDrafts, setPriorityDrafts] = useState<Record<string, string>>({});
  useEffect(() => { if (!dirty) setDraft([...identity.settings.urlMappings]); }, [dirty, identity.settings.urlMappings]);
  const reset = () => {
    setDraft([...identity.settings.urlMappings]);
    setDirty(false);
    setSaved(false);
    setInvalid(false);
    setPriorityDrafts({});
  };
  return <UrlMappingDraftContext value={{ draft, setDraft, invalid, setInvalid, saved, setSaved, dirty, setDirty, saving, setSaving, priorityDrafts, setPriorityDrafts, reset }}>{children}</UrlMappingDraftContext>;
}

export function SynchronizedSettingsBoundary(props: { readonly copy: Copy; readonly bridge?: NativeBridgeV1; readonly githubProvider?: GitHubProvider; readonly onOpenExternal?: (target: ExternalLinkTarget) => Promise<void> }) {
  const mappingDraft = use(UrlMappingDraftContext);
  return mappingDraft === null ? <UrlMappingDraftProvider><SynchronizedSettingsContent {...props} /></UrlMappingDraftProvider> : <SynchronizedSettingsContent {...props} />;
}

function SynchronizedSettingsContent({ copy, bridge = nativeBridge, githubProvider, onOpenExternal = (target) => browserShell.openExternal(target, "") }: { readonly copy: Copy; readonly bridge?: NativeBridgeV1; readonly githubProvider?: GitHubProvider; readonly onOpenExternal?: (target: ExternalLinkTarget) => Promise<void> }) {
  const identity = useIdentitySettings();
  const mappingDraft = use(UrlMappingDraftContext);
  if (mappingDraft === null) throw new Error("URL mapping draft provider is required");
  const [actionError, setActionError] = useState(false);
  const invoke = (action: () => Promise<unknown>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const replaceAppearance = (appearance: Partial<DevHudSettingsV1["appearance"]>) => invoke(() => identity.replaceSettings((current) => ({
    ...current,
    appearance: { ...current.appearance, ...appearance },
  })));
  return <>
    <label>{copy.theme}<select value={identity.settings.appearance.theme} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ theme: event.target.value as DevHudSettingsV1["appearance"]["theme"] })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></label>
    <label>{copy.language}<select value={identity.settings.appearance.language} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ language: event.target.value as DevHudSettingsV1["appearance"]["language"] })}><option value={LanguagePreference.System}>{copy.system}</option><option value={LanguagePreference.English}>{copy.english}</option><option value={LanguagePreference.Korean}>{copy.korean}</option></select></label>
    <UrlMappingSettings copy={copy} />
    {(identity.status === "guest" || identity.status === "signed-out" || identity.status === "starting") && <p className="notice">{copy.guestSettingsLocal}</p>}
    {identity.status === "blocked" && <p className="notice">{copy.blockedLocalHint}</p>}
    {identity.status === "deletion-pending" && <p className="notice">{copy.deletionPendingSummary}</p>}
    {identity.status === "authenticated" && <section className="synchronized-settings" aria-label={copy.synchronizedSettings}>
      <h3>{copy.synchronizedSettings}</h3>
      {identity.offline && <p className="notice" role="status">{copy.offlineSettingsReadOnly}</p>}
      {!identity.offline && <p>{copy.settingsRevision}: {identity.revision.toString()}</p>}
    {identity.importDiff && <SnapshotChoice key="import" choiceId="import" copy={copy} entries={identity.importDiff} title={copy.importSettingsTitle} summary={copy.importSettingsSummary} primary={copy.uploadLocal} secondary={copy.replaceLocal} onPrimary={() => invoke(identity.uploadLocal)} onSecondary={() => { if (identity.replaceLocal()) mappingDraft.reset(); }} />}
    {identity.conflict && <SnapshotChoice key="conflict" choiceId="conflict" copy={copy} entries={identity.conflict.diff} title={copy.conflictTitle} summary={copy.conflictSummary} primary={copy.reapplyLocal} secondary={copy.adoptServer} onPrimary={() => invoke(async () => { if (await identity.reapplyConflictLocal()) mappingDraft.reset(); })} onSecondary={() => { identity.adoptConflictServer(); mappingDraft.reset(); }} />}
    {(actionError || identity.error?.startsWith("settings-") || identity.settingsError) && <section className="notice" role="alert"><p>{copy.settingsActionFailed}{identity.error?.startsWith("settings-") && <> <code>{identity.error}</code></>}{identity.settingsError && <> <code>{`settings-connect-${identity.settingsError.code}`}</code>{identity.settingsError.correlationId && <> {copy.correlationId}: <code>{identity.settingsError.correlationId}</code></>}</>}</p><button onClick={() => invoke(identity.retrySettings)}>{copy.retry}</button></section>}
    </section>}
    <GitHubSettings copy={copy} bridge={bridge} provider={githubProvider} openExternal={onOpenExternal} />
  </>;
}

function UrlMappingSettings({ copy }: { readonly copy: Copy }) {
  const identity = useIdentitySettings();
  const mappingDraft = use(UrlMappingDraftContext);
  if (mappingDraft === null) throw new Error("URL mapping draft provider is required");
  const { draft, setDraft, invalid, setInvalid, saved, setSaved, dirty, setDirty, saving, setSaving, priorityDrafts, setPriorityDrafts } = mappingDraft;
  const overlaps = safeOverlaps(draft);
  const change = (id: string, field: keyof UrlRepositoryMapping, value: string | number | null) => {
    setSaved(false); setInvalid(false); setDirty(true);
    setDraft((current) => current.map((mapping) => mapping.id === id ? { ...mapping, [field]: value } : mapping));
  };
  const changeRepository = (id: string, field: "owner" | "name", value: string) => {
    setSaved(false); setInvalid(false); setDirty(true);
    setDraft((current) => current.map((mapping) => mapping.id === id ? { ...mapping, repository: { ...mapping.repository, [field]: value } } : mapping));
  };
  const changePriority = (id: string, value: string) => {
    setSaved(false); setInvalid(false); setDirty(true);
    setPriorityDrafts((current) => ({ ...current, [id]: value }));
  };
  const add = () => {
    const timestamp = new Date().toISOString();
    setSaved(false); setInvalid(false); setDirty(true);
    setDraft((current) => [...current, { id: uuidV7(), pattern: "https://example.com/**", repository: { owner: "owner", name: "repository" }, credentialProfileRef: "github.default", priority: 0, chromeOrigin: null, updatedAt: timestamp }]);
  };
  const save = () => {
    try {
      if (Object.values(priorityDrafts).some((value) => value === "" || !Number.isInteger(Number(value)))) throw new TypeError("priority must be an integer");
      const previous = new Map(identity.settings.urlMappings.map((mapping) => [mapping.id, mapping]));
      const now = new Date().toISOString();
      const mappings = draft.map((mapping) => {
        const withPriority = priorityDrafts[mapping.id] === undefined ? mapping : { ...mapping, priority: Number(priorityDrafts[mapping.id]) };
        const existing = previous.get(withPriority.id);
        const unchanged = existing !== undefined && JSON.stringify({ ...existing, updatedAt: "" }) === JSON.stringify({ ...withPriority, updatedAt: "" });
        return unchanged ? withPriority : { ...withPriority, updatedAt: now };
      });
      const next = parseDevHudSettings({ ...identity.settings, urlMappings: mappings });
      setInvalid(false); setSaved(false); setSaving(true);
      void identity.replaceSettings(next).then((applied) => {
        if (!applied) return;
        setDraft(mappings);
        setDirty(false);
        setPriorityDrafts({});
        setSaved(true);
      }).catch(() => undefined).finally(() => setSaving(false));
    } catch { setInvalid(true); }
  };
  return <section className="url-mappings" aria-labelledby="url-mappings-title">
    <h3 id="url-mappings-title">{copy.urlMappingsTitle}</h3><p>{copy.urlMappingsSummary}</p><p id="url-mapping-hint">{copy.mappingPatternHint}</p>
    {draft.map((mapping, index) => <fieldset key={mapping.id} disabled={identity.readOnly || saving} aria-label={`${copy.urlMappingsTitle} ${index + 1}`}>
      <legend>{`${mapping.repository.owner}/${mapping.repository.name}`}</legend>
      <label>{copy.urlPattern}<input value={mapping.pattern} aria-describedby="url-mapping-hint" onChange={(event) => change(mapping.id, "pattern", event.target.value)} /></label>
      <label>{copy.repositoryOwner}<input value={mapping.repository.owner} onChange={(event) => changeRepository(mapping.id, "owner", event.target.value)} /></label>
      <label>{copy.repositoryName}<input value={mapping.repository.name} onChange={(event) => changeRepository(mapping.id, "name", event.target.value)} /></label>
      <label>{copy.credentialProfile}<input value={mapping.credentialProfileRef} onChange={(event) => change(mapping.id, "credentialProfileRef", event.target.value)} /></label>
      <label>{copy.mappingPriority}<input type="number" value={priorityDrafts[mapping.id] ?? String(mapping.priority)} onChange={(event) => changePriority(mapping.id, event.target.value)} /></label>
      <label>{copy.chromeOrigin}<input value={mapping.chromeOrigin ?? ""} onChange={(event) => change(mapping.id, "chromeOrigin", event.target.value || null)} /></label>
      <button type="button" onClick={() => { setSaved(false); setDirty(true); setPriorityDrafts((current) => { const { [mapping.id]: _removed, ...remaining } = current; return remaining; }); setDraft((current) => current.filter((item) => item.id !== mapping.id)); }}>{copy.removeUrlMapping}</button>
    </fieldset>)}
    <div className="actions"><button type="button" disabled={identity.readOnly || saving} onClick={add}>{copy.addUrlMapping}</button><button type="button" disabled={identity.readOnly || saving} onClick={save}>{copy.saveUrlMappings}</button></div>
    {invalid && <p role="alert">{copy.mappingInvalid}</p>}
    {overlaps.length > 0 && <p role="status">{copy.mappingOverlap}</p>}
    {saved && <p role="status">{copy.mappingSaved}</p>}
  </section>;
}

function safeOverlaps(mappings: readonly UrlRepositoryMapping[]) {
  try { return findMappingOverlaps(mappings); } catch { return []; }
}

function uuidV7(): string {
  const random = new Uint8Array(10); crypto.getRandomValues(random);
  const time = Date.now().toString(16).padStart(12, "0");
  const tail = Array.from(random, (byte) => byte.toString(16).padStart(2, "0")).join("");
  const variant = (8 + (random[0]! & 3)).toString(16);
  return `${time.slice(0, 8)}-${time.slice(8)}-7${tail.slice(0, 3)}-${variant}${tail.slice(3, 6)}-${tail.slice(6, 18)}`;
}

function SnapshotChoice({ choiceId, copy, entries, title, summary, primary, secondary, onPrimary, onSecondary }: { readonly choiceId: string; readonly copy: Copy; readonly entries: readonly SettingsDiffEntry[]; readonly title: string; readonly summary: string; readonly primary: string; readonly secondary: string; readonly onPrimary: () => void; readonly onSecondary: () => void }) {
  const [open, setOpen] = useState(true);
  const dialog = useRef<HTMLElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef<HTMLElement | null>(null);
  const close = () => {
    setOpen(false);
    requestAnimationFrame(() => restoreFocus.current?.focus());
  };
  const choose = (action: () => void) => {
    action();
    requestAnimationFrame(() => restoreFocus.current?.focus());
  };
  useEffect(() => {
    if (!open) return;
    restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButton.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    addEventListener("keydown", closeOnEscape);
    return () => removeEventListener("keydown", closeOnEscape);
  }, [open]);
  if (!open) return <section className="notice"><p>{summary}</p><button onClick={() => setOpen(true)}>{title}</button></section>;
  const titleId = `snapshot-choice-${choiceId}-title`;
  return <section ref={dialog} className="snapshot-choice" role="dialog" aria-modal="true" aria-labelledby={titleId} onKeyDown={(event) => trapDialogFocus(event, dialog.current)}>
    <button ref={closeButton} type="button" onClick={close}>{copy.close}</button>
    <h4 id={titleId}>{title}</h4><p>{summary}</p>
    <table><caption>{copy.completeSnapshotDiff}</caption><thead><tr><th scope="col">{copy.settingPath}</th><th scope="col">{copy.localValue}</th><th scope="col">{copy.serverValue}</th></tr></thead><tbody>{entries.length === 0 ? <tr><td colSpan={3}>{copy.noDifferences}</td></tr> : entries.map((entry) => <tr key={`${entry.path}:${entry.kind}`}><th scope="row">{entry.path}</th><td><code>{printValue(entry.local)}</code></td><td><code>{printValue(entry.server)}</code></td></tr>)}</tbody></table>
    <div className="actions"><button onClick={() => choose(onPrimary)}>{primary}</button><button onClick={() => choose(onSecondary)}>{secondary}</button></div>
  </section>;
}

function trapDialogFocus(event: ReactKeyboardEvent<HTMLElement>, dialog: HTMLElement | null): void {
  if (event.key !== "Tab" || dialog === null) return;
  const focusable = dialog.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), [href]");
  if (focusable.length === 0) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function printValue(value: unknown): string {
  if (value === undefined) return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}
