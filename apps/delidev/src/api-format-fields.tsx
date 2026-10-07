// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import { useInfiniteQuery, useQuery } from "@connectrpc/connect-query";
import { APIFormatId, APIAuthenticationId, apiFormat, apiFormatLabels, apiFormatToWire, providerAPIFormats, accountAPIProfile, AccountTypeFilter, EntityKind, ProviderInventoryCapability, ProviderQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { document, object, text, type Document } from "./documents";
import { Problem } from "./ui";

interface Props { data: Document; change: (data: Document) => void; active: boolean; initial?: Resource; keepsFormatKey?: (ready: boolean) => void; saveBlocked?: (blocked: boolean) => void }

function useFormatCapability(active: boolean) {
  const inventory = useQuery(ProviderQuery.listProviderInventory, { pageSize: 1 }, { enabled: active, retry: false });
  return { inventory, keepsKey: Boolean(inventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_FORMAT_CHANGE_V1) && !inventory.error && !inventory.isFetching), ready: Boolean(inventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && !inventory.error && !inventory.isFetching) };
}

function useFormatReferences(protocol: APIFormatId, providerId: string, active: boolean) {
  // The server filters before pagination, including legacy account defaults.
  // One row proves a reference without loading an entire provider's accounts.
  const query = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 1 }, providerId, accountType: AccountTypeFilter.API, apiProtocol: apiFormatToWire(protocol) }, { enabled: active && Boolean(providerId), retry: false });
  return { protocol, query };
}

export function ProviderAPIFormatFields({ data, change, active, initial, saveBlocked }: Props) {
  useLocale();
  const { inventory, ready, keepsKey } = useFormatCapability(active);
  // Current-format filters intentionally exclude historical generations. Read all
  // provider accounts before unlocking profiles on servers retaining generations.
  const generations = useInfiniteQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 200 }, providerId: initial?.id ?? "", accountType: AccountTypeFilter.API }, {
    enabled: active && ready && keepsKey && Boolean(initial), retry: false,
    pageParamKey: "filter", getNextPageParam: page => page.nextPageToken ? { kind: EntityKind.ACCOUNT, pageSize: 200, pageToken: page.nextPageToken } : undefined,
  });
  useEffect(() => { if (generations.hasNextPage && !generations.isFetching && !generations.error) void generations.fetchNextPage(); }, [generations.hasNextPage, generations.isFetching, generations.error, generations.fetchNextPage]);
  const generationsReady = !keepsKey || Boolean(generations.data && !generations.hasNextPage && !generations.error && !generations.isFetching);
  const references = [useFormatReferences(APIFormatId.Responses, initial?.id ?? "", active && ready), useFormatReferences(APIFormatId.ChatCompletions, initial?.id ?? "", active && ready), useFormatReferences(APIFormatId.Messages, initial?.id ?? "", active && ready)];
  const referencesReady = !initial || (generationsReady && references.every(({ query }) => query.data && !query.error && !query.isFetching));
  const profiles = Array.isArray(data.api_formats) ? data.api_formats.map(object) : providerAPIFormats(data).map(profile => ({ ...profile }));
  const blocked = !ready || !referencesReady || profiles.length === 0;
  useEffect(() => { saveBlocked?.(blocked); return () => saveBlocked?.(false); }, [blocked, saveBlocked]);
  const protectedFormats = new Set(references.filter(({ query }) => Boolean(query.data?.resources.length)).map(({ protocol }) => protocol));
  for (const page of generations.data?.pages ?? []) for (const resource of page.resources) {
    const body = document(resource);
    for (const retained of Array.isArray(body.retained_connections) ? body.retained_connections : []) {
      const protocol = apiFormat(object(object(object(retained).connection).api_format).protocol);
      if (protocol) protectedFormats.add(protocol);
    }
  }
  const referencesError = generations.error || references.find(({ query }) => query.error)?.query.error;
  const update = (next: Document[]) => {
    const legacy = !initial && next[0] ? { protocol: next[0].protocol, endpoint: next[0].endpoint, authentication: next[0].authentication } : {};
    change({ ...data, ...legacy, api_formats: next });
  };
  return <section className="provider-api-formats"><h4>{copy("configuration-fields.supportedApiFormats")}</h4><p>{copy("configuration-fields.apiFormatsHelp")}</p>
    {Object.values(APIFormatId).map(protocol => {
      const profile = profiles.find(profile => profile.protocol === protocol);
      const locked = Boolean(initial && (!referencesReady || protectedFormats.has(protocol)));
      return <fieldset key={protocol} className="provider-api-format-card" disabled={!ready}><label className="checkbox"><input type="checkbox" checked={Boolean(profile)} disabled={locked} onChange={event => update(event.target.checked ? [...profiles, { protocol, endpoint: "", authentication: APIAuthenticationId.Bearer }] : profiles.filter(profile => profile.protocol !== protocol))} />{apiFormatLabels[protocol]}</label>
        {profile ? <><label>{copy("configuration-fields.apiBaseUrl_a45474")}<input required maxLength={4096} value={text(profile.endpoint)} disabled={locked} onChange={event => update(profiles.map(value => value.protocol === protocol ? { ...value, endpoint: event.target.value } : value))} /></label><label>{copy("configuration-fields.authentication_66880d")}<select value={text(profile.authentication)} disabled={locked} onChange={event => update(profiles.map(value => value.protocol === protocol ? { ...value, authentication: event.target.value } : value))}>{Object.values(APIAuthenticationId).map(auth => <option key={auth} value={auth}>{auth === APIAuthenticationId.Bearer ? "Bearer" : auth === APIAuthenticationId.APIKey ? "API key" : copy("configuration-fields.keylessAuthentication")}</option>)}</select></label>{locked ? <p>{copy("configuration-fields.referencedApiProfile")}</p> : null}</> : null}
      </fieldset>;
    })}
    {profiles.length === 0 ? <p role="status">{copy("configuration-fields.atLeastOneApiFormat")}</p> : null}
    {!ready ? <p role="status">{inventory.isFetching ? copy("configuration-fields.loadingApiFormats") : copy("configuration-fields.apiFormatsUnavailable")}</p> : null}
    {initial && !referencesReady ? <p role="status">{copy("configuration-fields.apiProfileReferencesUnavailable")}</p> : null}
    <Problem error={inventory.error || referencesError} />
    {inventory.error || referencesError ? <button type="button" onClick={() => { void inventory.refetch(); if (initial) { references.forEach(({ query }) => { void query.refetch(); }); if (keepsKey) void generations.refetch(); } }}>{copy("configuration-fields.retryApiFormats")}</button> : null}
  </section>;
}

export function AccountAPIFormatField({ data, change, active, initial, saveBlocked, keepsFormatKey }: Props) {
  useLocale();
  const { inventory, ready, keepsKey } = useFormatCapability(active);
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: text(data.provider_id) }, { enabled: active && Boolean(data.provider_id), retry: false });
  const metadata = document(provider.data?.resource);
  useEffect(() => { keepsFormatKey?.(ready && keepsKey); return () => keepsFormatKey?.(false); }, [ready, keepsKey, keepsFormatKey]);
  const previous = initial ? document(initial) : data;
  const originalProfile = accountAPIProfile(metadata, previous);
  const profile = accountAPIProfile(metadata, data);
  const changed = previous.api_protocol !== data.api_protocol;
  const canChange = ready && Boolean(originalProfile) && !provider.error && !provider.isFetching && !data.removal && (keepsKey || (!data.connection && data.health === "disconnected"));
  const blocked = changed && (!canChange || !profile || (originalProfile?.authentication === APIAuthenticationId.Keyless) !== (profile.authentication === APIAuthenticationId.Keyless));
  useEffect(() => { saveBlocked?.(blocked); return () => saveBlocked?.(false); }, [blocked, saveBlocked]);
  const formats = providerAPIFormats(metadata).filter(profile => (profile.authentication === APIAuthenticationId.Keyless) === (originalProfile?.authentication === APIAuthenticationId.Keyless));
  const selected = apiFormat(data.api_protocol) ?? originalProfile?.protocol ?? "";
  return <><label>{copy("configuration-fields.apiFormat")}<select value={selected} disabled={!canChange} onChange={event => { const protocol = apiFormat(event.target.value); if (protocol) change({ ...data, api_protocol: protocol }); }}>{!selected ? <option value="">{copy("configuration-fields.loadingApiFormats")}</option> : null}{selected && !formats.some(profile => profile.protocol === selected) ? <option value={selected}>{apiFormatLabels[selected]}</option> : null}{formats.map(profile => <option key={profile.protocol} value={profile.protocol}>{apiFormatLabels[profile.protocol]}</option>)}</select></label>
    <p>{copy(keepsKey ? "configuration-fields.accountFormatKeepsKey" : "configuration-fields.accountFormatChangeHelp")}</p>{keepsKey ? <p>{copy("configuration-fields.accountFormatFutureExecutions")}</p> : null}<p>{copy("configuration-fields.accountCredentialClassHelp")}</p>
    {!ready ? <p role="status">{inventory.isFetching ? copy("configuration-fields.loadingApiFormats") : copy("configuration-fields.apiFormatsUnavailable")}</p> : null}<Problem error={inventory.error || provider.error} />
  </>;
}
