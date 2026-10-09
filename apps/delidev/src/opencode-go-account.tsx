// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountQuery, ConfigurationQuery, EntityKind, newRequestId, SubscriptionServiceId, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { serviceAccount } from "./subscription-resource";
import { copy, useLocale } from "./localization";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { SettingsTaskDialog, SettingsDialogSize, SettingsTaskActions } from "./settings-task";
import { Problem } from "./ui";
import { accountRemovalMutation } from "./account-removal";

/** One explicit create-and-connect intent. A retained account is never replaced
 * after a partial result, and uncertain RPCs retain their exact original bytes. */
export function OpenCodeGoAccount({ initial, active, changed, close, visible, completed }: { initial?: Resource; active: boolean; changed: () => void; close: () => void; visible?: boolean; completed?: () => void }) {
  useLocale();
  const formId = useId();
  const [account, setAccount] = useState(initial), [name, setName] = useState("OpenCode Go"), [key, setKey] = useState(""), [reveal, setReveal] = useState(false), [confirm, setConfirm] = useState(false);
  const submittedKey = useRef<Uint8Array | undefined>(undefined);
  const identity = useRef(newRequestId());
  const keyInput = useRef<HTMLInputElement>(null), focused = useRef(false);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const current = useQuery(AccountQuery.getAccountStatus, { id: account?.id ?? "" }, { enabled: active && Boolean(account) });
  const observed = current.data?.account;
  const owned = serviceAccount(observed, account?.id, SubscriptionServiceId.OpenCodeGo, account?.revision ?? 1n) ? observed : account;
  const data = document(owned), connected = Boolean(text(object(data.connection).id));
  const supported = status.data?.capabilities.includes(SystemCapability.OPENCODE_GO_SUBSCRIPTIONS_V1) === true && !status.error && !status.data.stopping;
  const accepted = (value?: Resource) => serviceAccount(value, undefined, SubscriptionServiceId.OpenCodeGo);
  const connect = useRetainedMutation(`opencode-go:connect:${identity.current}`, AccountQuery.connectAccount, result => {
    setAccount(result.account); setKey(""); submittedKey.current?.fill(0); submittedKey.current = undefined; changed(); close(); completed?.();
  }, (result, request) => result.requestId === request.mutation?.requestId && accepted(result.account) && result.account.id === request.mutation?.id && result.account.revision >= (request.mutation?.expectedRevision ?? 1n));
  const create = useRetainedMutation(`opencode-go:create:${identity.current}`, ConfigurationQuery.saveConfiguration, result => {
    setAccount(result.resource); changed();
    if (active && result.resource && submittedKey.current) void connect.send({ mutation: { id: result.resource.id, expectedRevision: result.resource.revision, requestId: newRequestId() }, apiKey: submittedKey.current });
  }, (result, request) => result.requestId === request.mutation?.requestId && accepted(result.resource));
  const disconnect = useRetainedMutation(`opencode-go:disconnect:${identity.current}`, AccountQuery.disconnectAccount, result => {
    setAccount(result.account); setConfirm(false); changed(); void current.refetch();
  }, (result, request) => result.requestId === request.mutation?.requestId && accepted(result.account) && result.account.id === request.mutation?.id && result.account.revision >= (request.mutation?.expectedRevision ?? 1n));
  const pending = create.busy || create.uncertain || connect.busy || connect.uncertain || disconnect.busy || disconnect.uncertain;
  const validRead = !current.error && (!current.isSuccess || serviceAccount(observed, account?.id, SubscriptionServiceId.OpenCodeGo, account?.revision ?? 1n));
  const ready = active && supported && validRead && !pending && !data.removal;
  useEffect(() => {
    if (visible === false) focused.current = false;
    else if (ready && !connected && !focused.current) { keyInput.current?.focus({ preventScroll: true }); focused.current = true; }
  }, [visible, ready, connected]);
  useEffect(() => () => { submittedKey.current?.fill(0); submittedKey.current = undefined; }, []);
  const submit = () => {
    if (!ready || connected || !key) return;
    submittedKey.current?.fill(0); submittedKey.current = new TextEncoder().encode(key);
    if (owned) void connect.send({ mutation: { id: owned.id, expectedRevision: owned.revision, requestId: newRequestId() }, apiKey: submittedKey.current });
    else void create.send({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: new TextEncoder().encode(JSON.stringify({ alias: name, subscription_service: SubscriptionServiceId.OpenCodeGo, type: "subscription", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false })) });
  };
  const body = <section className="subscription-account-create">
    <p>{copy("opencode-go.guidance")} <a href="https://opencode.ai/auth" target="_blank" rel="noreferrer">{copy("opencode-go.console")}</a></p>
    <p>{copy("opencode-go.storage")}</p>
    <p>{copy("opencode-go.quotaUnavailable")}</p>
    {!supported ? <p role="status">{copy("opencode-go.unsupported")}</p> : null}
    {!connected && !data.removal ? <form id={formId} onSubmit={event => { event.preventDefault(); submit(); }}><fieldset disabled={!ready}>
      {!owned ? <label>{copy("opencode-go.accountName")}<input required value={name} onChange={event => setName(event.target.value)} /></label> : null}
      <label>{copy("opencode-go.apiKey")}<input ref={keyInput} autoFocus required autoComplete="off" spellCheck={false} type={reveal ? "text" : "password"} value={key} onChange={event => setKey(event.target.value)} /></label>
      <button type="button" aria-pressed={reveal} onClick={() => setReveal(value => !value)}>{copy(reveal ? "opencode-go.hideKey" : "opencode-go.revealKey")}</button>
      <SettingsTaskActions><SettingsActionButton icon={SettingsActionIcon.Connect} type="submit" form={formId} disabled={!ready}>{copy("opencode-go.connect")}</SettingsActionButton></SettingsTaskActions>
    </fieldset></form> : connected ? <SettingsActionButton icon={SettingsActionIcon.Delete} type="button" disabled={!ready} onClick={() => setConfirm(true)}>{copy("subscription-settings.disconnect_acfc5b")}</SettingsActionButton> : <p role="status">{copy("account-connection.inline.cleanup")}</p>}
    {confirm && owned ? <div role="group"><p>{copy("opencode-go.disconnectGuidance")}</p><SettingsTaskActions><SettingsActionButton icon={SettingsActionIcon.Delete} type="button" disabled={!ready} onClick={() => void disconnect.send({ mutation: { id: owned.id, expectedRevision: owned.revision, requestId: newRequestId() } })}>{copy("account-connection.confirmDisconnection_d61f53")}</SettingsActionButton><SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" onClick={() => setConfirm(false)}>{copy("account-connection.keepAccountConnected_00ae06")}</SettingsActionButton></SettingsTaskActions></div> : null}
    {owned && data.removal ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={pending || !active} onClick={() => { const mutation = accountRemovalMutation(owned); if (mutation) void disconnect.send({ mutation }); }}>{copy("account-connection.retryOriginalCredentialCleanup_bb5e5d")}</SettingsActionButton> : null}
    {[create, connect, disconnect].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={operation.busy || !active} onClick={operation.retry}>{copy("opencode-go.retryOriginal")}</SettingsActionButton> : null}</div>)}
    <Problem error={status.error || current.error} />
  </section>;
  return visible === undefined ? body : visible ? <SettingsTaskDialog title={copy(initial ? "opencode-go.manageTitle" : "opencode-go.title")} size={SettingsDialogSize.Wide} close={close}>{body}</SettingsTaskDialog> : null;
}

export function OpenCodeGoManagement({ initial, active, close, visible, completed }: { initial: Resource | { id: string; revision: bigint }; active: boolean; close: () => void; visible?: boolean; completed?: () => void }) {
  const query = useQuery(AccountQuery.getAccountStatus, { id: initial.id }, { enabled: active });
  const resource = query.data?.account;
  const verified = serviceAccount(resource, initial.id, SubscriptionServiceId.OpenCodeGo, initial.revision);
  return verified ? <OpenCodeGoAccount initial={resource} visible={visible} completed={completed} active={active} changed={() => void query.refetch()} close={close} /> : <><p role="status">{copy("subscription-settings.loadingSubscriptions_d98d84")}</p><Problem error={query.error} /></>;
}
