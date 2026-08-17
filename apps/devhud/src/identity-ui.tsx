import { useEffect, useState, type Ref } from "react";
import type { Copy } from "./localization";
import { useIdentitySettings } from "./service-boundary";
import { isValidApiOrigin } from "./shell";
import type { SettingsDiffEntry } from "./settings-diff";

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
    if (!isValidApiOrigin(draft)) { setError(true); return; }
    setError(false);
    await onApply(draft);
  };
  return <div className="api-origin-editor">
    <label>{copy.apiOrigin}<input ref={inputRef} autoFocus={autoFocus} value={draft} onChange={(event) => setDraft(event.target.value)} aria-describedby="api-origin-security-warning api-origin-validation" /></label>
    <button type="button" onClick={() => void apply()} disabled={draft === value}>{copy.applyApiOrigin}</button>
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
      <button onClick={() => { setActionError(false); void identity.signIn().catch(() => setActionError(true)); }} disabled={identity.status === "starting" || identity.bootstrap === null}>{copy.signIn}</button>
      <button onClick={identity.continueLocally}>{copy.continueLocally}</button>
    </div>
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <p role="alert">{copy.bootstrapFailed}</p>}
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
  const invoke = (action: () => Promise<void>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h2>{copy.accountTitle}</h2>
    <p>{copy.accountSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} inputRef={inputRef} onApply={onApiOrigin} />
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {(identity.status === "signed-out" || identity.status === "guest") && <button onClick={() => invoke(identity.signIn)} disabled={identity.bootstrap === null}>{copy.signIn}</button>}
    {identity.status === "authenticated" && <section className="account-session" aria-label={copy.signedInSession}>
      <p>{identity.account?.displayName || identity.account?.email || copy.signedIn}</p>
      <div className="actions"><button onClick={() => invoke(identity.logout)}>{copy.logout}</button><button className="danger" onClick={() => setConfirmDelete(true)}>{copy.deleteAccount}</button></div>
    </section>}
    {identity.status === "blocked" && <section className="notice" role="status"><h3>{copy.blockedTitle}</h3><p>{copy.blockedSummary}</p><p>{copy.blockedLocalHint}</p><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></section>}
    {identity.status === "deletion-pending" && <section className="notice" role="status"><h3>{copy.deletionPendingTitle}</h3><p>{copy.deletionPendingSummary}</p>{identity.account?.recoverableUntil && <p>{copy.recoverableUntil}: {new Date(Number(identity.account.recoverableUntil.seconds) * 1000).toLocaleString()}</p>}<div className="actions"><button onClick={() => invoke(identity.restoreAccount)}>{copy.restoreAccount}</button><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></div></section>}
    {confirmDelete && <section className="confirmation" role="alertdialog" aria-modal="true" aria-labelledby="delete-account-title"><h3 id="delete-account-title">{copy.deleteAccountConfirmTitle}</h3><p>{copy.deleteAccountConfirmSummary}</p><div className="actions"><button className="danger" onClick={() => { setConfirmDelete(false); invoke(identity.deleteAccount); }}>{copy.deleteAccount}</button><button onClick={() => setConfirmDelete(false)}>{copy.cancel}</button></div></section>}
    {actionError && <p role="alert">{copy.accountActionFailed}</p>}
  </>;
}

export function SynchronizedSettingsBoundary({ copy }: { readonly copy: Copy }) {
  const identity = useIdentitySettings();
  const [actionError, setActionError] = useState(false);
  const invoke = (action: () => Promise<void>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  if (identity.status === "guest" || identity.status === "signed-out" || identity.status === "starting") return <p className="notice">{copy.guestSettingsLocal}</p>;
  if (identity.status === "blocked") return <p className="notice">{copy.blockedLocalHint}</p>;
  if (identity.status === "deletion-pending") return <p className="notice">{copy.deletionPendingSummary}</p>;
  return <section className="synchronized-settings" aria-label={copy.synchronizedSettings}>
    <h3>{copy.synchronizedSettings}</h3>
    {identity.offline && <p className="notice" role="status">{copy.offlineSettingsReadOnly}</p>}
    {!identity.offline && <p>{copy.settingsRevision}: {identity.revision.toString()}</p>}
    {identity.importDiff && <SnapshotChoice copy={copy} entries={identity.importDiff} title={copy.importSettingsTitle} summary={copy.importSettingsSummary} primary={copy.uploadLocal} secondary={copy.replaceLocal} onPrimary={() => invoke(identity.uploadLocal)} onSecondary={identity.replaceLocal} />}
    {identity.conflict && <SnapshotChoice copy={copy} entries={identity.conflict.diff} title={copy.conflictTitle} summary={copy.conflictSummary} primary={copy.reapplyLocal} secondary={copy.adoptServer} onPrimary={() => invoke(identity.reapplyConflictLocal)} onSecondary={identity.adoptConflictServer} />}
    {actionError && <p role="alert">{copy.settingsActionFailed}{identity.error && <> <code>{identity.error}</code></>}</p>}
  </section>;
}

function SnapshotChoice({ copy, entries, title, summary, primary, secondary, onPrimary, onSecondary }: { readonly copy: Copy; readonly entries: readonly SettingsDiffEntry[]; readonly title: string; readonly summary: string; readonly primary: string; readonly secondary: string; readonly onPrimary: () => void; readonly onSecondary: () => void }) {
  return <section className="snapshot-choice" role="dialog" aria-modal="true" aria-labelledby="snapshot-choice-title">
    <h4 id="snapshot-choice-title">{title}</h4><p>{summary}</p>
    <table><caption>{copy.completeSnapshotDiff}</caption><thead><tr><th scope="col">{copy.settingPath}</th><th scope="col">{copy.localValue}</th><th scope="col">{copy.serverValue}</th></tr></thead><tbody>{entries.length === 0 ? <tr><td colSpan={3}>{copy.noDifferences}</td></tr> : entries.map((entry) => <tr key={`${entry.path}:${entry.kind}`}><th scope="row">{entry.path}</th><td><code>{printValue(entry.local)}</code></td><td><code>{printValue(entry.server)}</code></td></tr>)}</tbody></table>
    <div className="actions"><button onClick={onPrimary}>{primary}</button><button onClick={onSecondary}>{secondary}</button></div>
  </section>;
}

function printValue(value: unknown): string {
  if (value === undefined) return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}
