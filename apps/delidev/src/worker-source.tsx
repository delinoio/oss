// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SubscriptionServiceId, SubscriptionServiceIdentity, subscriptionService, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { Problem } from "./ui";
import { copy, useLocale } from "./localization";

export enum SourceKind { Api = "api", Subscription = "subscription" }
export interface Source { kind: SourceKind; id: string }
export const sourceKey = (source?: Source) => source ? `${source.kind}:${source.id}` : "";
export function fromKey(value: string): Source | undefined {
  const [kind, id] = value.split(":");
  return id && (kind === SourceKind.Api || kind === SourceKind.Subscription && subscriptionService(id)) ? { kind: kind as SourceKind, id } : undefined;
}
export function wireService(source?: Source) {
  if (source?.kind !== SourceKind.Subscription) return SubscriptionServiceIdentity.UNSPECIFIED;
  return { [SubscriptionServiceId.ChatGPT]: SubscriptionServiceIdentity.CHATGPT, [SubscriptionServiceId.Claude]: SubscriptionServiceIdentity.CLAUDE, [SubscriptionServiceId.Grok]: SubscriptionServiceIdentity.GROK }[source.id as SubscriptionServiceId];
}
export function sameSource(row: Resource, source?: Source) {
  const data = document(row);
  return row.kind === EntityKind.ACCOUNT && supportsResourceSchema(row) && data.retired !== true && (source?.kind === SourceKind.Subscription
    ? data.type === "subscription" && data.subscription_service === source.id && !data.provider_id
    : source?.kind === SourceKind.Api && data.type === "api" && data.provider_id === source.id);
}
export function modelSource(row: Resource): Source | undefined {
  const data = document(row);
  if (row.kind !== EntityKind.MODEL || data.retired === true || !supportsResourceSchema(row)) return undefined;
  if (data.source_kind === "subscription" && subscriptionService(data.subscription_service)) return { kind: SourceKind.Subscription, id: text(data.subscription_service) };
  return text(data.provider_id) ? { kind: SourceKind.Api, id: text(data.provider_id) } : undefined;
}

// Off-page selections keep their original identity and are read independently
// of the bounded source page. No list fallback can substitute another account.
export function SelectedAccount({ id, active, refresh, read }: { id: string; active: boolean; refresh: number; read: (id: string, row?: Resource) => void }) {
  useLocale();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.ACCOUNT, id }, { enabled: active, refetchInterval: active ? 5000 : false });
  useEffect(() => { if (active && refresh) void current.refetch(); }, [active, refresh, current.refetch]);
  useEffect(() => { if (current.data || current.error) read(id, current.error ? undefined : current.data?.resource); }, [current.data, current.error, id, read]);
  return <><Problem error={current.error} />{!current.data && !current.error ? <p role="status">{copy("agent-worker-wizard.loadingSelectedAccount")}</p> : null}</>;
}
