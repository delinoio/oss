import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountQuery, EntityKind, ProviderQuery, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text, type Document } from "./documents";
import { Authentication } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

function Observation({ label, value }: { label: string; value: unknown }) {
  const observation = object(value), problem = object(observation.problem);
  return <div><p>{label}: {text(observation.state) || "Unknown"}{text(observation.observed_at) ? ` · ${text(observation.observed_at)}` : ""}</p>{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}</div>;
}
export function AccountConnection({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
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
  const disconnected = data.health === "disconnected" && !data.connection && !data.removal;
  useEffect(() => { if (!active || !disconnected) setKey(""); }, [active, disconnected]);
  const mutation = () => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  const removal = object(data.removal);
  const retryRemoval = () => {
    if (!text(removal.request_id) || !Number.isSafeInteger(removal.expected_revision)) return;
    void disconnect.send({ mutation: { id: initial.id, requestId: text(removal.request_id), expectedRevision: BigInt(removal.expected_revision as number) } });
  };
  return <section><header><h3>{resourceName(current)}</h3><button onClick={close}>Back to accounts</button></header><p>Health: {text(data.health)} · {data.connection ? "Credential connected" : "Disconnected"}</p><p>Provider: {resourceName(provider.data?.resource)} · {text(metadata.authentication)}</p>
    {data.type === "subscription" ? <p>Subscription login is not implemented yet. No existing system login will be used.</p> : disconnected ? <form onSubmit={(event) => {
      event.preventDefault(); if (blocked || !provider.data?.resource || (!keyless && !/^[!-~]{1,8192}$/.test(key))) return;
      const input = { mutation: mutation(), keyless, apiKey: keyless ? new Uint8Array() : new TextEncoder().encode(key) };
      void connect.send(input); setKey("");
    }}><fieldset disabled={blocked || !provider.data?.resource}>{keyless ? <p>Explicitly connect this keyless local endpoint on the server computer.</p> : <label>API key<input type="password" autoComplete="off" spellCheck={false} maxLength={8192} value={key} onChange={(event) => setKey(event.target.value)} /></label>}<p>The selected server stores credentials in its protected vault. Connecting does not yet verify the provider or enable execution.</p><button className="primary" disabled={!keyless && !/^[!-~]{1,8192}$/.test(key)}>Connect account</button></fieldset></form> : null}
    {data.connection ? <div className="actions"><button disabled={blocked} onClick={() => void validate.send({ mutation: mutation() })}>Validate account</button><button disabled={blocked || metadata.discovery !== true} onClick={() => void discover.send({ mutation: mutation() })}>Refresh models</button><button disabled={blocked} onClick={() => setConfirm(true)}>Disconnect account</button></div> : null}
    {confirm ? <div className="notice"><p>Disconnecting cancels this account's active executions and removes its protected credentials. Other accounts remain connected. Worker cleanup is confirmed separately.</p><button disabled={blocked} onClick={() => void disconnect.send({ mutation: mutation() })}>Confirm disconnection</button><button disabled={blocked} onClick={() => setConfirm(false)}>Keep account connected</button></div> : null}
    {data.removal ? <p>Disconnected · credential cleanup is pending. <button disabled={blocked || !Number.isSafeInteger(removal.expected_revision)} onClick={retryRemoval}>Retry original credential cleanup</button></p> : null}
    {cleanup ? <p role="alert">{text(cleanup.message)} {text(cleanup.guidance)}</p> : null}
    <Observation label="Validation" value={data.validation} /><Observation label="Model discovery" value={data.catalog} />
    <Problem error={result.error || provider.error} />{operations.map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "connection" : index === 1 ? "disconnection" : index === 2 ? "validation" : "model refresh"}</button> : null}</div>)}
  </section>;
}
