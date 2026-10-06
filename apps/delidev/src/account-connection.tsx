import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskDismiss, useCloseSettingsTask, useInSettingsTask, useRetainSettingsTask } from "./settings-task-context";
import { ManagedSubscriptionAccount, serviceAccount } from "./subscription-accounts";
import { useEffect, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountQuery, EntityKind, ProviderQuery, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text, type Document } from "./documents";
import { Authentication } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { accountRemovalMutation } from "./account-removal";
import { Problem } from "./ui";
import "./api-account.css";

function Observation({ label, value }: { label: string; value: unknown }) {
  const observation = object(value), problem = object(observation.problem);
  return <div><p>{label}: {text(observation.state) || "Unknown"}{text(observation.observed_at) ? ` · ${text(observation.observed_at)}` : ""}</p>{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}</div>;
}
export function AccountConnection(props: { initial: Resource; active: boolean; close: () => void }) {
  if (serviceAccount(props.initial)) return <ManagedSubscriptionAccount {...props} />;
  return <ApiAccountConnection {...props} />;
}
function ApiAccountConnection({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  const formId = useId(), closeTask = useCloseSettingsTask(close), inTask = useInSettingsTask();
  const result = useQuery(AccountQuery.getAccountStatus, { id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.account, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current);
  const isApi = data.type === "api";
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
  const providerEnabled = provider.data?.resource !== undefined && metadata.enabled !== false;
  const disconnected = data.health === "disconnected" && !data.connection && !data.removal;
  useEffect(() => { if (!active || !disconnected) setKey(""); }, [active, disconnected]);
  useSettingsTaskDismiss(() => setKey(""));
  const mutation = () => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  const removal = current.id === initial.id ? accountRemovalMutation(current) : undefined;
  useRetainSettingsTask(Boolean(data.removal));
  const retryRemoval = () => {
    if (blocked || !removal) return;
    void disconnect.send({ mutation: removal });
  };
  return <section className={isApi ? "api-entry-workflow" : undefined}>{isApi ? <><button className="api-entry-back" onClick={closeTask}>Back to AI API Keys</button><header className="api-entry-heading"><h2 hidden={inTask}>Manage connection</h2><p>{resourceName(current)}</p><p className="api-entry-scope">Saved on the selected server.</p></header></> : <header><h3>{resourceName(current)}</h3><button onClick={closeTask}>Back to accounts</button></header>}<p>Health: {text(data.health)} · {data.connection ? "Credential connected" : "Disconnected"}</p><p>Provider: {resourceName(provider.data?.resource)} · {text(metadata.authentication)} · {provider.data?.resource ? providerEnabled ? "Enabled" : "Off" : "Unavailable"}</p>
    {data.type === "subscription" ? <p>Subscription login is not implemented yet. No existing system login will be used.</p> : disconnected ? <><form id={formId} onSubmit={(event) => {
      event.preventDefault(); if (blocked || !providerEnabled || !provider.data?.resource || (!keyless && !/^[!-~]{1,8192}$/.test(key))) return;
      const input = { mutation: mutation(), keyless, apiKey: keyless ? new Uint8Array() : new TextEncoder().encode(key) };
      void connect.send(input); setKey("");
    }}><fieldset disabled={blocked || !providerEnabled}>{keyless ? <p>Explicitly connect this keyless local endpoint on the server computer.</p> : <label>API key<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={key} onChange={(event) => setKey(event.target.value)} /></label>}<p>The selected server stores credentials in its protected vault. Connecting does not yet verify the provider or enable execution.</p></fieldset><SettingsTaskActions form={formId}><button className="primary" disabled={blocked || !providerEnabled || (!keyless && !/^[!-~]{1,8192}$/.test(key))}>{isApi ? keyless ? "Connect local endpoint" : "Connect API key" : "Connect account"}</button></SettingsTaskActions></form>{provider.data?.resource && !providerEnabled ? <p role="status">This provider is off. Enable it in API Providers before connecting. {isApi ? "Existing connection validation and cleanup remain available." : "Existing account validation and cleanup remain available."}</p> : null}</> : null}
    {data.connection && provider.data?.resource && !providerEnabled ? <p role="status">This provider is off. {isApi ? "Existing credentials and entry status remain available; enable the provider before refreshing models." : "Existing credentials and account status remain available; enable the provider before refreshing models."}</p> : null}
    {data.type !== "subscription" && data.connection ? <SettingsTaskActions className=""><button disabled={blocked} onClick={() => void validate.send({ mutation: mutation() })}>{isApi ? "Validate connection" : "Validate account"}</button><button disabled={blocked || !providerEnabled || metadata.discovery !== true} onClick={() => void discover.send({ mutation: mutation() })}>Refresh models</button><button disabled={blocked} onClick={() => setConfirm(true)}>{isApi ? "Disconnect" : "Disconnect account"}</button></SettingsTaskActions> : null}
    {confirm && data.type !== "subscription" ? <SettingsTaskDialog title="Disconnect account" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setConfirm(false)}><div className="notice"><p>{isApi ? "Disconnecting cancels this entry's active executions and removes its protected credentials. Other entries remain connected. Worker cleanup is confirmed separately." : "Disconnecting cancels this account's active executions and removes its protected credentials. Other accounts remain connected. Worker cleanup is confirmed separately."}</p><SettingsTaskActions><button disabled={blocked} onClick={() => void disconnect.send({ mutation: mutation() })}>Confirm disconnection</button><button data-settings-task-cancel disabled={blocked} onClick={() => setConfirm(false)}>{isApi ? "Keep entry connected" : "Keep account connected"}</button></SettingsTaskActions></div></SettingsTaskDialog> : null}
    {data.type === "subscription" && data.removal ? <p role="status">Credential cleanup pending. Subscription credential cleanup is not supported by this server.</p> : null}
    {data.type !== "subscription" && data.removal ? <p>Disconnected · credential cleanup is pending. <button disabled={blocked || !removal} onClick={retryRemoval}>Retry original credential cleanup</button></p> : null}
    {cleanup ? <p role="alert">{text(cleanup.message)} {text(cleanup.guidance)}</p> : null}
    <Observation label="Validation" value={data.validation} /><Observation label="Model discovery" value={data.catalog} />
    <Problem error={result.error || provider.error} />{data.type !== "subscription" ? operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "connection" : index === 1 ? "disconnection" : index === 2 ? "validation" : "model refresh"}</button> : null}</div>) : null}
  </section>;
}
