import { formatTimestamp } from "./localization";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskDismiss, useCloseSettingsTask, useInSettingsTask } from "./settings-task-context";
import { ManagedSubscriptionAccount, serviceAccount } from "./subscription-accounts";
import { useEffect, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountQuery, EntityKind, ProviderQuery, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text, type Document } from "./documents";
import { Authentication } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { accountRemovalMutation } from "./account-removal";
import { ServiceProblem, Problem  } from "./ui";
import "./api-account.css";

function Observation({ label, value }: { label: string; value: unknown }) {
  useLocale();
  const observation = object(value), problem = object(observation.problem);
  return <div><p>{label}: {statusLabel(text(observation.state)) || copy("account-connection.extra.b764cdc0eab7")}{text(observation.observed_at) ? copy("account-connection.message_2fa20b", { v0: formatTimestamp(text(observation.observed_at)) }) : ""}</p>{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}</div>;
}
export function AccountConnection(props: { initial: Resource; active: boolean; close: () => void }) {
  useLocale();
  const close = useCloseSettingsTask(props.close);
  if (props.initial.kind === EntityKind.ACCOUNT && props.initial.schemaVersion === 1 && document(props.initial).type === "api") return <ApiAccountConnection {...props} />;
  if (serviceAccount(props.initial)) return <ManagedSubscriptionAccount {...props} />;
  return <section><button onClick={close}>{copy("account-connection.backToAccounts_68d8e7")}</button><ServiceProblem code="unsupported"><p>{copy("account-connection.unsupportedAccountConfiguration")}</p></ServiceProblem></section>;
}
function ApiAccountConnection({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  useLocale();
  const formId = useId(), closeTask = useCloseSettingsTask(close), inTask = useInSettingsTask();
  const result = useQuery(AccountQuery.getAccountStatus, { id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.account, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current);
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: text(data.provider_id) }, { enabled: active });
  const [key, setKey] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [cleanup, setCleanup] = useState<Document>();
  const changed = (row?: Resource) => { if (row) setAcknowledged(row); void result.refetch(); };
  const connect = useRetainedMutation(`account-connect:${initial.id}`, AccountQuery.connectAccount, (value) => { setKey(""); changed(value.account); });
  const disconnect = useRetainedMutation(`account-disconnect:${initial.id}`, AccountQuery.disconnectAccount, (value) => {
    changed(value.account); setConfirm(false);
    try { setCleanup(value.cleanupProblemJson.byteLength ? object(JSON.parse(new TextDecoder().decode(value.cleanupProblemJson))) : undefined); } catch { setCleanup({ message: "Credential cleanup needs inspection." }); }
  });
  const validate = useRetainedMutation(`account-validate:${initial.id}`, AccountQuery.validateAccount, (value) => changed(value.account));
  const discover = useRetainedMutation(`account-discover:${initial.id}`, ProviderQuery.discoverModels, (value) => changed(value.account));
  const operations = [connect, disconnect, validate, discover];
  const blocked = operations.some((operation) => operation.busy || operation.uncertain);
  const metadata = document(provider.data?.resource), keyless = metadata.authentication === Authentication.Keyless;
  const providerEnabled = provider.data?.resource !== undefined && metadata.enabled === true;
  const disconnected = data.health === "disconnected" && !data.connection && !data.removal;
  useEffect(() => { if (!active || !disconnected) setKey(""); }, [active, disconnected]);
  useSettingsTaskDismiss(() => setKey(""));
  const mutation = () => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  const removal = current.id === initial.id ? accountRemovalMutation(current) : undefined;
  const retryRemoval = () => {
    if (blocked || !removal) return;
    void disconnect.send({ mutation: removal });
  };
  return <section className="api-entry-workflow">{<><button className="api-entry-back" onClick={closeTask}>{copy("account-connection.backToAiApiKeys_2d6214")}</button><header className="api-entry-heading"><h2 hidden={inTask}>{copy("account-connection.manageConnection_ad2892")}</h2><p>{resourceName(current)}</p><p className="api-entry-scope">{copy("account-connection.savedOnTheSelectedServer_93dbee")}</p></header></>}<p><LocalizedText id="account-connection.health_842f5a" components={{ s0: <>{statusLabel(text(data.health))}</>, s1: <>{data.connection ? copy("account-connection.credentialConnected_eed6f1") : copy("account-connection.disconnected_04dfac")}</> }} /></p><p><LocalizedText id="account-connection.provider_0a5bc7" components={{ s0: <>{resourceName(provider.data?.resource)}</>, s1: <>{text(metadata.authentication)}</>, s2: <>{provider.data?.resource ? providerEnabled ? copy("account-connection.enabled_92c1cd") : copy("account-connection.off_ca7981") : copy("account-connection.unavailable_ca1844")}</> }} /></p>
    {disconnected ? <><form id={formId} onSubmit={(event) => {
      event.preventDefault(); if (blocked || !providerEnabled || !provider.data?.resource || (!keyless && !/^[!-~]{1,8192}$/.test(key))) return;
      const input = { mutation: mutation(), keyless, apiKey: keyless ? new Uint8Array() : new TextEncoder().encode(key) };
      void connect.send(input); setKey("");
    }}><fieldset disabled={blocked || !providerEnabled}>{keyless ? <p>{copy("account-connection.explicitlyConnectThisKeylessLocalEndpoint_6ce987")}</p> : <label>{copy("account-connection.apiKey_16f0ee")}<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={key} onChange={(event) => setKey(event.target.value)} /></label>}<p>{copy("account-connection.theSelectedServerStoresCredentialsIn_1b5894")}</p></fieldset><SettingsTaskActions form={formId}><button className="primary" disabled={blocked || !providerEnabled || (!keyless && !/^[!-~]{1,8192}$/.test(key))}>{keyless ? copy("account-connection.connectLocalEndpoint_f37d68") : copy("account-connection.connectApiKey_0f97e9")}</button></SettingsTaskActions></form>{provider.data?.resource && !providerEnabled ? <p role="status"><LocalizedText id="account-connection.thisProviderIsOffEnableIt_7818fd" components={{ s0: <>{copy("account-connection.existingConnectionValidationAndCleanupRemain_be51c1")}</> }} /></p> : null}</> : null}
    {data.connection && provider.data?.resource && !providerEnabled ? <p role="status"><LocalizedText id="account-connection.thisProviderIsOff_b23c53" components={{ s0: <>{copy("account-connection.existingCredentialsAndEntryStatusRemain_15a735")}</> }} /></p> : null}
    {data.connection ? <SettingsTaskActions className=""><button disabled={blocked} onClick={() => void validate.send({ mutation: mutation() })}>{copy("account-connection.validateConnection_8ee4a4")}</button><button disabled={blocked || !providerEnabled || metadata.discovery !== true} onClick={() => void discover.send({ mutation: mutation() })}>{copy("account-connection.refreshModels_049030")}</button><button disabled={blocked} onClick={() => setConfirm(true)}>{copy("account-connection.disconnect_acfc5b")}</button></SettingsTaskActions> : null}
    {confirm ? <SettingsTaskDialog title={copy("account-connection.disconnect_acfc5b")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setConfirm(false)}><div className="notice"><p>{copy("account-connection.disconnectingCancelsThisEntrySActive_a31581")}</p><SettingsTaskActions><button disabled={blocked} onClick={() => void disconnect.send({ mutation: mutation() })}>{copy("account-connection.confirmDisconnection_d61f53")}</button><button data-settings-task-cancel disabled={blocked} onClick={() => setConfirm(false)}>{copy("account-connection.keepEntryConnected_269a70")}</button></SettingsTaskActions></div></SettingsTaskDialog> : null}
    {data.removal ? <p>{copy("account-connection.disconnectedCredentialCleanupIsPending_66cdef")}<button disabled={blocked || !removal} onClick={retryRemoval}>{copy("account-connection.retryOriginalCredentialCleanup_bb5e5d")}</button></p> : null}
    {cleanup ? <ServiceProblem code={text(cleanup.code) || text(cleanup.problem_code)}><p role="alert">{text(cleanup.message)} {text(cleanup.guidance)}</p></ServiceProblem> : null}
    <Observation label={copy("account-connection.validation_68e1ca")} value={data.validation} /><Observation label={copy("account-connection.modelDiscovery_3c49ba")} value={data.catalog} />
    <Problem error={result.error || provider.error} />{operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="account-connection.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("account-connection.connection_b38d9d") : index === 1 ? copy("account-connection.disconnection_4bd886") : index === 2 ? copy("account-connection.validation_98c41d") : copy("account-connection.modelRefresh_795dab")}</> }} /></button> : null}</div>)}
  </section>;
}
