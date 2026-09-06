import { createContext, use, useEffect, useEffectEvent, useId, useMemo, useRef, useState, type ComponentType, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type Ref } from "react";
import type { Copy, SupportedLanguage } from "./localization";
import { GitHubSettings, githubErrorCopy } from "./github-settings-ui.tsx";
import { createGitHubProvider, GitHubErrorCode, GitHubProviderError, readGitHubCredential, type GitHubProvider } from "./github-provider.ts";
import { NativeBridgeError, NativeBridgeErrorCode, nativeBridge, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary";
import { browserShell, LanguagePreference, normalizeApiOrigin, ThemePreference, type ExternalLinkTarget, type RuntimeCapabilities } from "./shell";
import { parseDevHudSettings, type DevHudSettingsV1 } from "./settings-contract";
import type { SettingsDiffEntry } from "./settings-diff";
import { findMappingOverlaps, type UrlRepositoryMapping } from "./url-mapping";
import { R2Settings } from "./r2-settings-ui.tsx";
import { SettingsSectionId, availableSettingsSections, type SettingsSectionCapabilities } from "./settings-sections";
import { ArrowRightIcon } from "./ui-icons";
import { Button, Card, DataRow, Field, PageHeader, StatusBadge } from "./ui-foundation";

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
  const inputId = useId();
  useEffect(() => setDraft(value), [value]);
  const apply = async () => {
    const normalized = normalizeApiOrigin(draft);
    if (normalized === null) { setError(true); return; }
    setError(false);
    setDraft(normalized);
    await onApply(normalized);
  };
  return <div className="api-origin-editor">
    <Field label={copy.apiOrigin} inputId={inputId} hint={copy.apiOriginHint} error={error ? copy.invalidApiOrigin : undefined}>
      <input id={inputId} ref={inputRef} autoFocus={autoFocus} value={draft} onChange={(event) => setDraft(event.target.value)} aria-describedby={`${inputId}-hint ${error ? `${inputId}-error ` : ""}api-origin-security-warning`} />
    </Field>
    <Button type="button" onClick={() => void apply()} disabled={normalizeApiOrigin(draft) === normalizeApiOrigin(value)}>{copy.applyApiOrigin}</Button>
    <p id="api-origin-security-warning" className="notice">{copy.customApiWarning}</p>
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
  return <Card className="onboarding-card">
    <PageHeader eyebrow={copy.account} title={copy.accountTitle} summary={copy.firstRunSummary} level={1} />
    <ApiOriginEditor copy={copy} value={apiOrigin} autoFocus onApply={onApiOrigin} />
    <div className="actions">
      <Button variant="primary" onClick={() => { setActionError(false); void identity.signIn().catch(() => setActionError(true)); }} disabled={identity.status === "starting" || identity.bootstrap === null || identity.signInPending}>{copy.signIn}</Button>
      <Button onClick={identity.continueLocally}>{copy.continueLocally}</Button>
    </div>
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <Card className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><Button onClick={identity.retryIdentity}>{copy.retry}</Button>{identity.identityResetAvailable && <Button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</Button>}</div></Card>}
    {actionError && <p role="alert">{copy.signInFailed}</p>}
  </Card>;
}

interface AccountIdentityProps {
  readonly copy: Copy;
  readonly apiOrigin: string;
  readonly inputRef: Ref<HTMLInputElement>;
  readonly onApiOrigin: (value: string) => Promise<void>;
  readonly onDeleteConfirmationOpenChange: (open: boolean) => void;
}

export function AccountIdentity({ copy, apiOrigin, inputRef, onApiOrigin, onDeleteConfirmationOpenChange }: AccountIdentityProps) {
  const identity = useIdentitySettings();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [actionError, setActionError] = useState(false);
  const deleteTrigger = useRef<HTMLButtonElement>(null);
  const deleteDialog = useRef<HTMLElement>(null);
  const cancelDelete = useRef<HTMLButtonElement>(null);
  const deleteConfirmationOpen = confirmDelete && identity.status === "authenticated" && !identity.accountError && identity.account !== null;
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
  useEffect(() => {
    onDeleteConfirmationOpenChange(deleteConfirmationOpen);
    return () => {
      if (deleteConfirmationOpen) onDeleteConfirmationOpenChange(false);
    };
  }, [deleteConfirmationOpen, onDeleteConfirmationOpenChange]);
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h2>{copy.accountTitle}</h2>
    <p>{copy.accountSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} inputRef={inputRef} onApply={onApiOrigin} />
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button><button onClick={identity.continueLocally}>{copy.continueLocally}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
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
    {deleteConfirmationOpen && <section ref={deleteDialog} className="confirmation" role="alertdialog" aria-modal="true" aria-labelledby="delete-account-title" onKeyDown={(event) => trapDialogFocus(event, deleteDialog.current)}><h3 id="delete-account-title">{copy.deleteAccountConfirmTitle}</h3><p>{copy.deleteAccountConfirmSummary}</p><div className="actions"><button className="danger" onClick={() => { closeDeleteConfirmation(); invoke(identity.deleteAccount); }}>{copy.deleteAccount}</button><button ref={cancelDelete} onClick={closeDeleteConfirmation}>{copy.cancel}</button></div></section>}
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
  readonly setBaselineMappings: (mappings: UrlRepositoryMapping[]) => void;
  readonly markDraftDirty: () => void;
  readonly invalid: boolean;
  readonly setInvalid: (invalid: boolean) => void;
  readonly saved: boolean;
  readonly setSaved: (saved: boolean) => void;
  readonly dirty: boolean;
  readonly saving: boolean;
  readonly setSaving: (saving: boolean) => void;
  readonly priorityDrafts: Record<string, string>;
  readonly setPriorityDrafts: (drafts: Record<string, string> | ((current: Record<string, string>) => Record<string, string>)) => void;
  readonly baseRevision: bigint;
  readonly credentialOperationPending: boolean;
  readonly runCredentialOperation: <Value>(operation: () => Promise<Value>) => Promise<Value>;
  readonly isCurrentScope: () => boolean;
  readonly reset: () => void;
}

const UrlMappingDraftContext = createContext<UrlMappingDraftValue | null>(null);

export function UrlMappingDraftProvider({ children }: { readonly children: ReactNode }) {
  const identity = useIdentitySettings();
  const accountId = identity.account?.userId?.value ?? identity.account?.logtoSubject ?? "";
  const scope = useRef({ retainsDraft: identity.status === "authenticated" || identity.status === "blocked", accountId, generation: 0 });
  const retainsDraft = identity.status === "authenticated" || identity.status === "blocked";
  const leavesSession = scope.current.retainsDraft && !retainsDraft;
  const changesAuthenticatedAccount = retainsDraft && scope.current.retainsDraft && accountId !== "" && scope.current.accountId !== "" && scope.current.accountId !== accountId;
  if (leavesSession || changesAuthenticatedAccount) {
    scope.current = { retainsDraft, accountId, generation: scope.current.generation + 1 };
  } else {
    scope.current.retainsDraft = retainsDraft;
    // Account and Settings queries resolve independently; learning this account's ID must not discard an editable draft.
    if (accountId !== "") scope.current.accountId = accountId;
  }
  const generation = scope.current.generation;
  return <UrlMappingDraftStateProvider key={generation} identity={identity} isCurrentScope={() => scope.current.generation === generation}>{children}</UrlMappingDraftStateProvider>;
}

function UrlMappingDraftStateProvider({ children, identity, isCurrentScope }: { readonly children: ReactNode; readonly identity: ReturnType<typeof useIdentitySettings>; readonly isCurrentScope: () => boolean }) {
  const [draft, setDraft] = useState<UrlRepositoryMapping[]>(() => [...identity.settings.urlMappings]);
  const [baselineMappings, setBaselineMappings] = useState<UrlRepositoryMapping[]>(() => [...identity.settings.urlMappings]);
  const [invalid, setInvalid] = useState(false);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [priorityDrafts, setPriorityDrafts] = useState<Record<string, string>>({});
  const dirty = !mappingDraftMatchesBaseline(draft, priorityDrafts, baselineMappings);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const markDraftDirty = () => { dirtyRef.current = true; };
  const [baseRevision, setBaseRevision] = useState(identity.revision);
  const credentialOperationTail = useRef(Promise.resolve());
  const credentialOperationCount = useRef(0);
  const [credentialOperationPending, setCredentialOperationPending] = useState(false);
  const runCredentialOperation = async <Value,>(operation: () => Promise<Value>): Promise<Value> => {
    credentialOperationCount.current += 1;
    setCredentialOperationPending(true);
    const previous = credentialOperationTail.current;
    let release: () => void = () => {};
    credentialOperationTail.current = new Promise<void>((resolve) => { release = resolve; });
    await previous;
    try {
      return await operation();
    } finally {
      release();
      credentialOperationCount.current -= 1;
      if (credentialOperationCount.current === 0) setCredentialOperationPending(false);
    }
  };
  useEffect(() => {
    if (!dirtyRef.current) {
      setDraft([...identity.settings.urlMappings]);
      setBaselineMappings([...identity.settings.urlMappings]);
      setBaseRevision(identity.revision);
    }
  }, [dirty, identity.revision, identity.settings.urlMappings]);
  const reset = () => {
    setDraft([...identity.settings.urlMappings]);
    setBaselineMappings([...identity.settings.urlMappings]);
    setBaseRevision(identity.revision);
    setSaved(false);
    setInvalid(false);
    setPriorityDrafts({});
  };
  return <UrlMappingDraftContext value={{ draft, setDraft, setBaselineMappings, markDraftDirty, invalid, setInvalid, saved, setSaved, dirty, saving, setSaving, priorityDrafts, setPriorityDrafts, baseRevision, credentialOperationPending, runCredentialOperation, isCurrentScope, reset }}>{children}</UrlMappingDraftContext>;
}

export interface SettingsSectionContributions {
  readonly Shortcuts?: ComponentType<{ readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly capabilities: RuntimeCapabilities }>;
  readonly LocalAgents?: ComponentType<{ readonly copy: Copy; readonly bridge: NativeBridgeV1 }>;
  readonly Updates?: ComponentType<{ readonly bridge: NativeBridgeV1; readonly language: SupportedLanguage; readonly onApprovalOpenChange?: (open: boolean) => void }>;
}

interface SettingsBoundaryProps {
  readonly copy: Copy;
  readonly bridge?: NativeBridgeV1;
  readonly githubProvider?: GitHubProvider;
  readonly onOpenExternal?: (target: ExternalLinkTarget) => Promise<void>;
  readonly onModalConfirmationOpenChange?: (open: boolean) => void;
  readonly onUpdaterApprovalOpenChange?: (open: boolean) => void;
  readonly mobile?: boolean;
  readonly language?: SupportedLanguage;
  readonly shortcutCapabilities?: RuntimeCapabilities;
  readonly sectionContributions?: SettingsSectionContributions;
  readonly NativeMessagingSettings?: ComponentType<{ readonly copy: Copy }>;
  readonly localAgentsAvailable?: boolean;
  readonly notification?: { readonly permission: string; readonly failed: boolean; readonly onRequest: () => void };
  readonly storeUpdates?: { readonly configured: boolean; readonly failed: boolean; readonly onOpen: () => void };
}

export function SynchronizedSettingsBoundary(props: SettingsBoundaryProps) {
  const mappingDraft = use(UrlMappingDraftContext);
  return mappingDraft === null ? <UrlMappingDraftProvider><SynchronizedSettingsContent {...props} /></UrlMappingDraftProvider> : <SynchronizedSettingsContent {...props} />;
}

function SynchronizedSettingsContent({ copy, bridge = nativeBridge, githubProvider, onOpenExternal = (target) => browserShell.openExternal(target, ""), onModalConfirmationOpenChange, onUpdaterApprovalOpenChange, mobile = false, language = "en", shortcutCapabilities = { available: new Set() }, sectionContributions, NativeMessagingSettings, localAgentsAvailable = false, notification, storeUpdates }: SettingsBoundaryProps) {
  const identity = useIdentitySettings();
  const mappingDraft = use(UrlMappingDraftContext);
  if (mappingDraft === null) throw new Error("URL mapping draft provider is required");
  const [actionError, setActionError] = useState(false);
  const [selected, setSelected] = useState<SettingsSectionId>(SettingsSectionId.Appearance);
  const [mobileDetail, setMobileDetail] = useState(false);
  const panelRefs = useRef<Partial<Record<SettingsSectionId, HTMLElement | null>>>({});
  const openerRefs = useRef<Partial<Record<SettingsSectionId, HTMLButtonElement | null>>>({});
  const capabilities: SettingsSectionCapabilities = {
    mobile,
    shortcuts: sectionContributions?.Shortcuts !== undefined,
    nativeMessaging: NativeMessagingSettings !== undefined,
    localAgents: localAgentsAvailable && sectionContributions?.LocalAgents !== undefined,
    notifications: notification !== undefined,
    updates: mobile ? storeUpdates !== undefined : sectionContributions?.Updates !== undefined,
  };
  const sections = useMemo(() => availableSettingsSections(capabilities), [mobile, capabilities.shortcuts, capabilities.nativeMessaging, capabilities.localAgents, capabilities.notifications, capabilities.updates]);
  const availableIds = sections.map(({ id }) => id);
  const focusHeading = (id: SettingsSectionId) => requestAnimationFrame(() => panelRefs.current[id]?.querySelector<HTMLElement>("h3")?.focus());
  const select = (id: SettingsSectionId) => {
    setSelected(id);
    if (mobile) setMobileDetail(true);
    focusHeading(id);
  };
  const back = () => {
    setMobileDetail(false);
    requestAnimationFrame(() => openerRefs.current[selected]?.focus());
  };
  useEffect(() => {
    if (availableIds.includes(selected)) return;
    setSelected(SettingsSectionId.Appearance);
    focusHeading(SettingsSectionId.Appearance);
  }, [availableIds.join("\u0000"), mobileDetail, selected]);
  const invoke = (action: () => Promise<unknown>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const replaceAppearance = (appearance: Partial<DevHudSettingsV1["appearance"]>) => invoke(() => identity.replaceSettings((current) => ({
    ...current,
    appearance: { ...current.appearance, ...appearance },
  })));
  const content = (id: SettingsSectionId): ReactNode => {
    if (id === SettingsSectionId.Appearance) return <section><h3 tabIndex={-1}>{copy.settingsAppearanceTitle}</h3><p>{copy.settingsAppearanceSummary}</p><Field label={copy.theme} inputId="settings-theme"><select id="settings-theme" value={identity.settings.appearance.theme} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ theme: event.target.value as DevHudSettingsV1["appearance"]["theme"] })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></Field><Field label={copy.language} inputId="settings-language"><select id="settings-language" value={identity.settings.appearance.language} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ language: event.target.value as DevHudSettingsV1["appearance"]["language"] })}><option value={LanguagePreference.System}>{copy.system}</option><option value={LanguagePreference.English}>{copy.english}</option><option value={LanguagePreference.Korean}>{copy.korean}</option></select></Field></section>;
    if (id === SettingsSectionId.Shortcuts && sectionContributions?.Shortcuts) return <section><h3 tabIndex={-1}>{copy.settingsShortcutsTitle}</h3><p>{copy.settingsShortcutsSummary}</p><sectionContributions.Shortcuts copy={copy} bridge={bridge} capabilities={shortcutCapabilities} /></section>;
    if (id === SettingsSectionId.ChromeExtension && NativeMessagingSettings) return <NativeMessagingSettings copy={copy} />;
    if (id === SettingsSectionId.UrlMappings) return <UrlMappingSettings copy={copy} bridge={bridge} githubProvider={githubProvider} />;
    if (id === SettingsSectionId.GitHubCredentials) return <GitHubSettings copy={copy} bridge={bridge} provider={githubProvider} openExternal={onOpenExternal} credentialOperationPending={mappingDraft.credentialOperationPending} runCredentialOperation={mappingDraft.runCredentialOperation} />;
    if (id === SettingsSectionId.CloudflareR2) return <R2Settings copy={copy} bridge={bridge} />;
    if (id === SettingsSectionId.LocalAgents && sectionContributions?.LocalAgents) return <sectionContributions.LocalAgents copy={copy} bridge={bridge} />;
    if (id === SettingsSectionId.Notifications && notification) return <section><h3 tabIndex={-1}>{copy.settingsNotificationsTitle}</h3><p>{copy.settingsNotificationsSummary}</p><Button variant="primary" onClick={notification.onRequest}>{copy.notificationPermission}</Button><output aria-live="polite">{notification.permission}</output>{notification.failed && <p className="native-setting-error" role="alert">{copy.notificationPermissionFailed}</p>}</section>;
    if (id === SettingsSectionId.Updates && mobile && storeUpdates) return <section><h3 tabIndex={-1}>{copy.settingsUpdatesTitle}</h3><p>{copy.updatePolicy}</p>{storeUpdates.configured && <Button variant="primary" onClick={storeUpdates.onOpen}>{copy.openAppStore}</Button>}{storeUpdates.failed && <p className="native-setting-error" role="alert">{copy.storeOpenFailed}</p>}</section>;
    if (id === SettingsSectionId.Updates && sectionContributions?.Updates) return <sectionContributions.Updates bridge={bridge} language={language} onApprovalOpenChange={onUpdaterApprovalOpenChange} />;
    return null;
  };
  return <>
    <PageHeader eyebrow={copy.settings} title={copy.settingsTitle} summary={copy.settingsSummary} />
    <section className="settings-summary" aria-label={copy.synchronizedSettings}>
      {(identity.status === "guest" || identity.status === "signed-out" || identity.status === "starting") && <StatusBadge tone="neutral">{copy.guestSettingsLocal}</StatusBadge>}
      {identity.status === "blocked" && <StatusBadge tone="warning">{copy.blockedLocalHint}</StatusBadge>}
      {identity.status === "deletion-pending" && <StatusBadge tone="warning">{copy.deletionPendingSummary}</StatusBadge>}
      {identity.status === "authenticated" && (identity.offline ? <StatusBadge tone="warning">{copy.offlineSettingsReadOnly}</StatusBadge> : <StatusBadge tone="success">{copy.settingsRevision}: {identity.revision.toString()}</StatusBadge>)}
      {identity.importDiff && <SnapshotChoice key="import" choiceId="import" copy={copy} entries={identity.importDiff} title={copy.importSettingsTitle} summary={copy.importSettingsSummary} primary={copy.uploadLocal} secondary={copy.replaceLocal} onOpenChange={onModalConfirmationOpenChange} onPrimary={() => invoke(async () => { if (await identity.uploadLocal()) mappingDraft.reset(); })} onSecondary={() => invoke(async () => { if (await identity.replaceLocal()) mappingDraft.reset(); })} />}
      {identity.conflict && <SnapshotChoice key="conflict" choiceId="conflict" copy={copy} entries={identity.conflict.diff} title={copy.conflictTitle} summary={copy.conflictSummary} primary={copy.reapplyLocal} secondary={copy.adoptServer} onOpenChange={onModalConfirmationOpenChange} onPrimary={() => invoke(async () => { if (await identity.reapplyConflictLocal()) mappingDraft.reset(); })} onSecondary={() => invoke(async () => { if (await identity.adoptConflictServer()) mappingDraft.reset(); })} />}
      {(actionError || identity.error?.startsWith("settings-") || identity.settingsError) && <section className="notice" role="alert"><p>{copy.settingsActionFailed}{identity.error?.startsWith("settings-") && <> <code>{identity.error}</code></>}{identity.settingsError && <> <code>{`settings-connect-${identity.settingsError.code}`}</code>{identity.settingsError.correlationId && <> {copy.correlationId}: <code>{identity.settingsError.correlationId}</code></>}</>}</p><Button onClick={() => invoke(identity.retrySettings)}>{copy.retry}</Button></section>}
    </section>
    <div className={mobile ? "settings-mobile" : "settings-desktop"}>
      {mobile && !mobileDetail && <Card className="settings-index" aria-label={copy.settingsSections}>{sections.map((section) => { const Icon = section.icon; return <DataRow key={section.id} ref={(element) => { openerRefs.current[section.id] = element; }} icon={<Icon />} title={copy[section.title]} description={copy[section.summary]} trailing={<ArrowRightIcon />} onClick={() => select(section.id)} />; })}</Card>}
      {!mobile && <nav className="settings-toc" aria-label={copy.settingsSections}><Card>{sections.map((section) => { const Icon = section.icon; return <DataRow key={section.id} icon={<Icon />} title={copy[section.title]} description={copy[section.summary]} ariaCurrent={selected === section.id ? "page" : undefined} onClick={() => select(section.id)} />; })}</Card></nav>}
      <div className="settings-panels">
        {sections.map((section) => {
          const active = selected === section.id && (!mobile || mobileDetail);
          return <Card key={section.id} ref={(element) => { panelRefs.current[section.id] = element; }} className="settings-panel" data-settings-section={section.id} hidden={!active} inert={!active}>{mobile && <Button variant="ghost" onClick={back}>{copy.backToSettings}</Button>}{content(section.id)}</Card>;
        })}
      </div>
    </div>
  </>;
}
function UrlMappingSettings({ copy, bridge, githubProvider = createGitHubProvider({ fetch: globalThis.fetch }) }: { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly githubProvider?: GitHubProvider }) {
  const identity = useIdentitySettings();
  const mappingDraft = use(UrlMappingDraftContext);
  if (mappingDraft === null) throw new Error("URL mapping draft provider is required");
  const { draft, setDraft, setBaselineMappings, markDraftDirty, invalid, setInvalid, saved, setSaved, dirty, saving, setSaving, priorityDrafts, setPriorityDrafts, baseRevision, credentialOperationPending, runCredentialOperation, isCurrentScope } = mappingDraft;
  const [validationError, setValidationError] = useState<keyof Copy | null>(null);
  const overlaps = safeOverlaps(draft);
  const change = (id: string, field: keyof UrlRepositoryMapping, value: string | number | null) => {
    markDraftDirty(); setSaved(false); setInvalid(false); setValidationError(null);
    setDraft((current) => current.map((mapping) => mapping.id === id ? { ...mapping, [field]: value } : mapping));
  };
  const changeRepository = (id: string, field: "owner" | "name", value: string) => {
    markDraftDirty(); setSaved(false); setInvalid(false); setValidationError(null);
    setDraft((current) => current.map((mapping) => mapping.id === id ? { ...mapping, repository: { ...mapping.repository, [field]: value } } : mapping));
  };
  const changePriority = (id: string, value: string) => {
    markDraftDirty(); setSaved(false); setInvalid(false); setValidationError(null);
    setPriorityDrafts((current) => ({ ...current, [id]: value }));
  };
  const add = () => {
    const timestamp = new Date().toISOString();
    markDraftDirty(); setSaved(false); setInvalid(false); setValidationError(null);
    setDraft((current) => [...current, { id: uuidV7(), pattern: "https://example.com/**", repository: { owner: "owner", name: "repository" }, credentialProfileRef: "", priority: 0, chromeOrigin: null, updatedAt: timestamp }]);
  };
  const save = async () => {
    if (!dirty) return;
    let mappings: UrlRepositoryMapping[];
    try {
      if (Object.values(priorityDrafts).some((value) => value === "" || !Number.isInteger(Number(value)))) throw new TypeError("priority must be an integer");
      mappings = parseDevHudSettings({ ...identity.settings, urlMappings: withUpdatedMappings(draft, priorityDrafts, identity.settings.urlMappings) }).urlMappings.slice();
    } catch {
      setInvalid(true);
      return;
    }
    setInvalid(false); setValidationError(null); setSaved(false); setSaving(true);
    let validationCompleted = false;
    try {
      let committedMappings = mappings;
      const committed = await runCredentialOperation(async () => {
        await validateChangedMappings(mappings, identity.settings.urlMappings, { ...identity.settings, urlMappings: mappings }, bridge, githubProvider, identity.githubPatScopeId);
        validationCompleted = true;
        if (!isCurrentScope()) return false;
        return identity.replaceSettingsAt((current) => {
          let next: DevHudSettingsV1;
          try {
            next = parseDevHudSettings({ ...current, urlMappings: withUpdatedMappings(draft, priorityDrafts, current.urlMappings) });
          } catch (reason) {
            throw new UrlMappingSaveRebaseError(reason);
          }
          committedMappings = next.urlMappings.slice();
          return next;
        }, baseRevision);
      });
      if (!committed || !isCurrentScope()) return;
      setDraft(committedMappings);
      setBaselineMappings(committedMappings);
      setPriorityDrafts({});
      setSaved(true);
    } catch (error) {
      if (isCurrentScope() && (error instanceof UrlMappingSaveRebaseError || error instanceof Error && error.message === "settings-read-only")) setValidationError("githubSetupFailed");
      else if (!validationCompleted && isCurrentScope()) setValidationError(error instanceof GitHubProviderError ? githubErrorCopy(error.code) : error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.StorageFailure ? "githubErrorSecureStorage" : "githubSetupFailed");
      // The synchronized-settings boundary exposes typed transport failures.
    } finally { if (isCurrentScope()) setSaving(false); }
  };
  return <section className="url-mappings" aria-labelledby="url-mappings-title">
    <h3 id="url-mappings-title" tabIndex={-1}>{copy.urlMappingsTitle}</h3><p>{copy.urlMappingsSummary}</p><p id="url-mapping-hint">{copy.mappingPatternHint}</p>
    {draft.map((mapping, index) => <fieldset key={mapping.id} disabled={identity.readOnly || saving} aria-label={`${copy.urlMappingsTitle} ${index + 1}`}>
      <legend>{`${mapping.repository.owner}/${mapping.repository.name}`}</legend>
      <label>{copy.urlPattern}<input value={mapping.pattern} aria-describedby="url-mapping-hint" onChange={(event) => change(mapping.id, "pattern", event.target.value)} /></label>
      <label>{copy.repositoryOwner}<input value={mapping.repository.owner} onChange={(event) => changeRepository(mapping.id, "owner", event.target.value)} /></label>
      <label>{copy.repositoryName}<input value={mapping.repository.name} onChange={(event) => changeRepository(mapping.id, "name", event.target.value)} /></label>
      <label>{copy.credentialProfile}<select value={mapping.credentialProfileRef} onChange={(event) => change(mapping.id, "credentialProfileRef", event.target.value)}><option value="">{copy.githubSelectProfile}</option>{identity.settings.github.profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>
      <label>{copy.mappingPriority}<input type="number" value={priorityDrafts[mapping.id] ?? String(mapping.priority)} onChange={(event) => changePriority(mapping.id, event.target.value)} /></label>
      <label>{copy.chromeOrigin}<input value={mapping.chromeOrigin ?? ""} onChange={(event) => change(mapping.id, "chromeOrigin", event.target.value || null)} /></label>
      <button type="button" onClick={() => { markDraftDirty(); setSaved(false); setValidationError(null); setPriorityDrafts((current) => { const { [mapping.id]: _removed, ...remaining } = current; return remaining; }); setDraft((current) => current.filter((item) => item.id !== mapping.id)); }}>{copy.removeUrlMapping}</button>
    </fieldset>)}
    <div className="actions"><button type="button" disabled={identity.readOnly || saving || credentialOperationPending || identity.settings.github.profiles.length === 0} onClick={add}>{copy.addUrlMapping}</button><button type="button" disabled={identity.readOnly || saving || credentialOperationPending || !dirty} onClick={save}>{copy.saveUrlMappings}</button></div>
    {invalid && <p role="alert">{copy.mappingInvalid}</p>}
    {validationError !== null && <p role="alert">{copy[validationError]}</p>}
    {overlaps.length > 0 && <p role="status">{copy.mappingOverlap}</p>}
    {saved && <p role="status">{copy.mappingSaved}</p>}
  </section>;
}

function mappingDraftMatchesBaseline(draft: readonly UrlRepositoryMapping[], priorityDrafts: Readonly<Record<string, string>>, baseline: readonly UrlRepositoryMapping[]): boolean {
  if (draft.length !== baseline.length) return false;
  return draft.every((mapping, index) => {
    const priorityDraft = priorityDrafts[mapping.id];
    if (priorityDraft !== undefined && (priorityDraft === "" || !Number.isInteger(Number(priorityDraft)))) return false;
    const effective = priorityDraft === undefined ? mapping : { ...mapping, priority: Number(priorityDraft) };
    const existing = baseline[index];
    return existing !== undefined && existing.id === effective.id && JSON.stringify({ ...existing, updatedAt: "" }) === JSON.stringify({ ...effective, updatedAt: "" });
  });
}

class UrlMappingSaveRebaseError extends Error {
  constructor(reason: unknown) {
    super("URL mapping settings changed during validation");
    this.cause = reason;
  }
}

function withUpdatedMappings(draft: readonly UrlRepositoryMapping[], priorityDrafts: Readonly<Record<string, string>>, previousMappings: readonly UrlRepositoryMapping[]): UrlRepositoryMapping[] {
  const previous = new Map(previousMappings.map((mapping) => [mapping.id, mapping]));
  const now = new Date().toISOString();
  return draft.map((mapping) => {
    const withPriority = priorityDrafts[mapping.id] === undefined ? mapping : { ...mapping, priority: Number(priorityDrafts[mapping.id]) };
    const existing = previous.get(withPriority.id);
    const unchanged = existing !== undefined && JSON.stringify({ ...existing, updatedAt: "" }) === JSON.stringify({ ...withPriority, updatedAt: "" });
    return unchanged ? withPriority : { ...withPriority, updatedAt: now };
  });
}

async function validateChangedMappings(mappings: readonly UrlRepositoryMapping[], previousMappings: readonly UrlRepositoryMapping[], settings: DevHudSettingsV1, bridge: NativeBridgeV1, provider: GitHubProvider, scopeId: Promise<string>): Promise<void> {
  const previous = new Map(previousMappings.map((mapping) => [mapping.id, mapping]));
  const assignments = new Map<string, UrlRepositoryMapping>();
  for (const mapping of mappings) {
    const existing = previous.get(mapping.id);
    if (existing !== undefined && existing.credentialProfileRef === mapping.credentialProfileRef && existing.repository.owner === mapping.repository.owner && existing.repository.name === mapping.repository.name) continue;
    assignments.set(`${mapping.credentialProfileRef}:${mapping.repository.owner.toLowerCase()}/${mapping.repository.name.toLowerCase()}`, mapping);
  }
  if (assignments.size === 0) return;
  const resolvedScopeId = await scopeId;
  const credentials = new Map<string, ReturnType<typeof readGitHubCredential>>();
  await Promise.all([...assignments.values()].map(async (mapping) => {
    const profile = settings.github.profiles.find((candidate) => candidate.id === mapping.credentialProfileRef);
    if (profile === undefined) throw new GitHubProviderError(GitHubErrorCode.MissingToken, "validate-repository");
    let credential = credentials.get(profile.id);
    if (credential === undefined) {
      credential = readGitHubCredential(bridge, profile, resolvedScopeId);
      credentials.set(profile.id, credential);
    }
    await provider.validateRepository(await credential, mapping.repository);
  }));
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

function SnapshotChoice({ choiceId, copy, entries, title, summary, primary, secondary, onOpenChange, onPrimary, onSecondary }: { readonly choiceId: string; readonly copy: Copy; readonly entries: readonly SettingsDiffEntry[]; readonly title: string; readonly summary: string; readonly primary: string; readonly secondary: string; readonly onOpenChange?: (open: boolean) => void; readonly onPrimary: () => void; readonly onSecondary: () => void }) {
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
  useEffect(() => {
    onOpenChange?.(open);
    return () => {
      if (open) onOpenChange?.(false);
    };
  }, [onOpenChange, open]);
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
