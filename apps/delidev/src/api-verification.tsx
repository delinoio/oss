// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { Timestamp, TimestampText } from "./timestamp-display";
import { useQuery } from "@connectrpc/connect-query";
import { useRef, useState } from "react";
import { AccountQuery, ResourceQuery, EntityKind, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { copy, formatNumber, useLocale } from "./localization";
import { useEndpointModelHints } from "./endpoint-model-hints";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

function currentObservation(value: unknown, connection: string) {
  const result = object(value);
  return connection && result.connection_id === connection ? result : {};
}

export function ApiVerification({ row, provider: providedProvider, active, changed }: { row: Resource; provider?: Resource; active: boolean; changed: () => void }) {
  useLocale();
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = acknowledged?.id === row.id && acknowledged.revision > row.revision ? acknowledged : row;
  const data = document(current), providerId = text(data.provider_id), connection = text(object(data.connection).id);
  const providerMatches = (value?: Resource) => Boolean(value && value.id === providerId && value.kind === EntityKind.PROVIDER && supportsResourceSchema(value));
  const resolveProvider = active && current.kind === EntityKind.ACCOUNT && data.type === "api" && supportsResourceSchema(current) && Boolean(providerId) && !providerMatches(providedProvider);
  // Inventory summaries are bounded. A referenced provider outside that page
  // needs its own exact read; never infer authority from a display name.
  const providerRead = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: providerId }, { enabled: resolveProvider, retry: false, gcTime: 0 });
  const provider = providerMatches(providedProvider) ? providedProvider : providerMatches(providerRead.data?.resource) ? providerRead.data?.resource : undefined;
  const metadata = document(provider);
  const latest = useRef({ row: current, provider, active }); latest.current = { row: current, provider, active };
  const validation = currentObservation(data.validation, connection);
  const hints=useEndpointModelHints(current,provider,active);
  const catalog: Document = hints.result ? {state:"observed",received:hints.result.models.length,observed_at:new Date(Number(hints.result.observedAtUnixMs)).toISOString()} : hints.error ? {state:"failed"} : {};
  const validate = useRetainedMutation(`api-check-auth:${row.id}`, AccountQuery.validateAccount, result => {
    if (result.account) setAcknowledged(result.account);
    changed();
    const next = result.account, now = latest.current, providerData = document(now.provider);
    // An acknowledged old connection cannot authorize the next operation. An
    // uncertain validation retains its exact request and never reaches here.
    if (!next || !now.active || !now.provider || providerData.enabled === false || !supportsResourceSchema(now.provider) || !supportsResourceSchema(next)) return;
    const nextData = document(next);
    const receipt = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.validationJson)));
    const published = object(nextData.validation);
    // Replay returns the immutable old receipt alongside a mutable current account.
    // Only the same published validation can admit the next endpoint read.
    if (receipt.request_id !== result.requestId || published.request_id !== receipt.request_id || published.connection_id !== receipt.connection_id || receipt.connection_id !== text(object(nextData.connection).id)) return;
    if (text(object(nextData.connection).id) !== text(object(document(now.row).connection).id) || nextData.provider_id !== document(now.row).provider_id || nextData.removal || nextData.enabled !== true) return;
    void hints.read({account:next,provider:now.provider});
  });
  const busy = validate.busy || hints.pending, uncertain = validate.uncertain;
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
  const modelsLabel = catalog.state === "failed" || catalog.state === "unsupported" ? "api-verification.modelsFailed" : catalog.state === "observed" ? catalog.received === 0 ? "api-verification.modelsEmpty" : "api-verification.modelsSynced" : "api-verification.modelsAwaiting";
  const observedAt = [text(validation.observed_at), text(catalog.observed_at)].filter(value => Number.isFinite(Date.parse(value))).sort((a, b) => Date.parse(b) - Date.parse(a))[0];
  return <section className="api-verification" aria-label={copy("api-verification.heading")}>
    <div className="api-verification-band">
      <div className="api-verification-status" role="status">
        <div className="api-verification-group"><span className="api-verification-icon" aria-hidden="true">{authLabel === "api-verification.verified" || authLabel === "api-verification.keyless" ? "✓" : "·"}</span><p>{copy(authLabel)}</p></div>
        <div className="api-verification-group"><span className="api-verification-icon" aria-hidden="true">{modelsLabel === "api-verification.modelsSynced" || modelsLabel === "api-verification.modelsEmpty" ? "✓" : "·"}</span><p>{copy(modelsLabel)}{catalog.state === "observed" && typeof catalog.received === "number" && Number.isSafeInteger(catalog.received) && catalog.received >= 0 ? copy("api-verification.count", { count: catalog.received, v0: formatNumber(catalog.received) }) : null}</p></div>
        <div className="api-verification-group api-verification-times">
          {observedAt ? <p><TimestampText id="api-verification.checked" values={{ v0: <Timestamp value={observedAt} /> }} /></p> : null}
          {catalog.state !== "observed" && text(catalog.last_success_at) ? <p><TimestampText id="api-verification.retained" values={{ v0: <Timestamp value={text(catalog.last_success_at)} /> }} /></p> : null}
        </div>
        {busy ? <p className="api-verification-progress">{copy("api-verification.checking")}</p> : null}
        {uncertain ? <p className="api-verification-progress">{copy("api-verification.uncertain")}</p> : null}
      </div>
      <div className="api-verification-actions">
        <SettingsActionButton icon={SettingsActionIcon.Confirm} type="button" disabled={!available || busy || uncertain} onClick={check}>{copy("api-verification.check")}</SettingsActionButton>
        {validate.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || busy} onClick={validate.retry}>{copy("api-verification.retryAuth")}</SettingsActionButton> : null}

      </div>
      <div className="api-verification-problems">
        <Problem error={validate.error ?? hints.error} />
        {resolveProvider ? <><Problem error={providerRead.error} />{providerRead.error || providerRead.data && !provider ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || providerRead.isFetching} onClick={() => void providerRead.refetch()}>{copy("api-verification.retryProvider")}</SettingsActionButton> : null}</> : null}
        {!authority ? <small>{copy("api-verification.unavailable")}</small> : null}
      </div>
    </div>
    <small className="api-verification-disclaimer">{copy("api-verification.limit")}</small>
  </section>;
}
