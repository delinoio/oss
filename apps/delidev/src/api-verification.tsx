// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { AccountQuery, ProviderQuery, EntityKind, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { copy, formatNumber, formatTimestamp, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

function currentObservation(value: unknown, connection: string) {
  const result = object(value);
  return connection && result.connection_id === connection ? result : {};
}

export function ApiVerification({ row, provider, active, changed }: { row: Resource; provider?: Resource; active: boolean; changed: () => void }) {
  useLocale();
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = acknowledged?.id === row.id && acknowledged.revision > row.revision ? acknowledged : row;
  const data = document(current), metadata = document(provider), connection = text(object(data.connection).id);
  const latest = useRef({ row: current, provider, active }); latest.current = { row: current, provider, active };
  const validation = currentObservation(data.validation, connection), catalog = currentObservation(data.catalog, connection);
  const discover = useRetainedMutation(`api-check-models:${row.id}`, ProviderQuery.discoverModels, result => { if (result.account) setAcknowledged(result.account); changed(); });
  const validate = useRetainedMutation(`api-check-auth:${row.id}`, AccountQuery.validateAccount, result => {
    if (result.account) setAcknowledged(result.account);
    changed();
    const next = result.account, now = latest.current, providerData = document(now.provider);
    // An acknowledged old connection cannot authorize the next operation. An
    // uncertain validation retains its exact request and never reaches here.
    if (!next || !now.active || !now.provider || providerData.discovery !== true || providerData.enabled === false || !supportsResourceSchema(now.provider) || !supportsResourceSchema(next)) return;
    const nextData = document(next);
    if (text(object(nextData.connection).id) !== text(object(document(now.row).connection).id) || nextData.provider_id !== document(now.row).provider_id || nextData.removal || nextData.enabled !== true) return;
    const input = { mutation: { id: next.id, expectedRevision: next.revision, requestId: newRequestId() } };
    const connectionId = text(object(nextData.connection).id);
    void discover.send(input, response => Boolean(response.requestId === input.mutation.requestId && response.account?.id === next.id && response.account.kind === EntityKind.ACCOUNT && document(response.account).type === "api" && document(response.account).provider_id === nextData.provider_id && supportsResourceSchema(response.account) && response.account.revision >= next.revision && text(object(document(response.account).connection).id) === connectionId));
  });
  const busy = validate.busy || discover.busy, uncertain = validate.uncertain || discover.uncertain;
  const authority = current.kind === EntityKind.ACCOUNT && data.type === "api" && supportsResourceSchema(current) && Boolean(provider && provider.kind === EntityKind.PROVIDER && provider.id === data.provider_id && supportsResourceSchema(provider));
  const available = active && authority && metadata.enabled !== false && data.enabled === true && Boolean(connection) && !data.removal;
  const check = () => {
    if (!available || busy || uncertain) return;
    const input = { mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() } };
    void validate.send(input, result => {
      const observed = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.validationJson)));
      return Boolean(result.requestId === input.mutation.requestId && result.account?.id === current.id && result.account.kind === EntityKind.ACCOUNT && document(result.account).type === "api" && document(result.account).provider_id === data.provider_id && supportsResourceSchema(result.account) && result.account.revision >= current.revision && observed.request_id === input.mutation.requestId && observed.connection_id === connection);
    });
  };
  const verified = validation.state === "observed" && ["credential-accepted", "keyless-endpoint"].includes(text(validation.authentication));
  const authLabel = data.removal ? "api-verification.cleanup" : !connection ? "api-verification.disconnected" : verified ? validation.authentication === "keyless-endpoint" ? "api-verification.keyless" : "api-verification.verified" : validation.state === "failed" ? "api-verification.failed" : validation.state === "unsupported" ? "api-verification.unsupported" : "api-verification.awaiting";
  const modelsLabel = metadata.discovery === false ? "api-verification.discoveryDisabled" : catalog.state === "failed" || catalog.state === "unsupported" ? "api-verification.modelsFailed" : catalog.state === "observed" ? catalog.received === 0 ? "api-verification.modelsEmpty" : "api-verification.modelsSynced" : "api-verification.modelsAwaiting";
  const observedAt = [text(validation.observed_at), text(catalog.observed_at)].filter(value => Number.isFinite(Date.parse(value))).sort((a, b) => Date.parse(b) - Date.parse(a))[0];
  return <section className="api-verification" aria-label={copy("api-verification.heading")}>
    <div role="status"><p>{copy(authLabel)}</p><p>{copy(modelsLabel)}{catalog.state === "observed" && typeof catalog.received === "number" && Number.isSafeInteger(catalog.received) && catalog.received >= 0 ? copy("api-verification.count", { count: catalog.received, v0: formatNumber(catalog.received) }) : null}</p>
      {observedAt ? <p>{copy("api-verification.checked", { v0: formatTimestamp(observedAt) })}</p> : null}
      {catalog.state !== "observed" && text(catalog.last_success_at) ? <p>{copy("api-verification.retained", { v0: formatTimestamp(text(catalog.last_success_at)) })}</p> : null}
      {busy ? <p>{copy("api-verification.checking")}</p> : null}
      {uncertain ? <p>{copy("api-verification.uncertain")}</p> : null}
    </div>
    <button type="button" disabled={!available || busy || uncertain} onClick={check}>{copy("api-verification.check")}</button>
    {validate.uncertain ? <button type="button" disabled={!active || busy} onClick={validate.retry}>{copy("api-verification.retryAuth")}</button> : null}
    {discover.uncertain ? <button type="button" disabled={!active || busy} onClick={discover.retry}>{copy("api-verification.retryModels")}</button> : null}
    <Problem error={validate.error ?? discover.error} />
    {!authority ? <small>{copy("api-verification.unavailable")}</small> : null}
    <small>{copy("api-verification.limit")}</small>
  </section>;
}
