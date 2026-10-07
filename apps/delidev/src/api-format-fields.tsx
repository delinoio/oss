// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { APIFormatId, APIAuthenticationId, apiFormat, apiFormatLabels, providerAPIFormats, accountAPIProfile, EntityKind, ProviderInventoryCapability, ProviderQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { document, object, text, type Document } from "./documents";
import { Problem } from "./ui";

interface Props { data: Document; change: (data: Document) => void; active: boolean; initial?: Resource; saveBlocked?: (blocked: boolean) => void }

function useFormatCapability(active: boolean) {
  const inventory = useQuery(ProviderQuery.listProviderInventory, { pageSize: 1 }, { enabled: active, retry: false });
  return { inventory, ready: Boolean(inventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && !inventory.error && !inventory.isFetching) };
}

export function ProviderAPIFormatFields({ data, change, active, initial, saveBlocked }: Props) {
  useLocale();
  const { inventory, ready } = useFormatCapability(active);
  const accounts = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 200 }, providerId: initial?.id ?? "" }, { enabled: active && Boolean(initial), retry: false });
  const referencesReady = !initial || Boolean(accounts.data && !accounts.error && !accounts.isFetching && !accounts.data.nextPageToken);
  const profiles = Array.isArray(data.api_formats) ? data.api_formats.map(object) : providerAPIFormats(data).map(profile => ({ ...profile }));
  const blocked = !ready || !referencesReady || profiles.length === 0;
  useEffect(() => { saveBlocked?.(blocked); return () => saveBlocked?.(false); }, [blocked, saveBlocked]);
  const original = initial ? document(initial) : undefined;
  const protectedFormats = new Set((accounts.data?.resources ?? []).map(row => text(document(row).api_protocol) || text(original?.protocol)));
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
    <Problem error={inventory.error || accounts.error} />
    {inventory.error || accounts.error ? <button type="button" onClick={() => { void inventory.refetch(); if (initial) void accounts.refetch(); }}>{copy("configuration-fields.retryApiFormats")}</button> : null}
  </section>;
}

export function AccountAPIFormatField({ data, change, active, initial, saveBlocked }: Props) {
  useLocale();
  const { inventory, ready } = useFormatCapability(active);
  const provider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: text(data.provider_id) }, { enabled: active && Boolean(data.provider_id), retry: false });
  const metadata = document(provider.data?.resource);
  const previous = initial ? document(initial) : data;
  const originalProfile = accountAPIProfile(metadata, previous);
  const profile = accountAPIProfile(metadata, data);
  const changed = previous.api_protocol !== data.api_protocol;
  const canChange = ready && Boolean(originalProfile) && !provider.error && !provider.isFetching && !data.connection && !data.removal && data.health === "disconnected";
  const blocked = changed && (!canChange || !profile || (originalProfile?.authentication === APIAuthenticationId.Keyless) !== (profile.authentication === APIAuthenticationId.Keyless));
  useEffect(() => { saveBlocked?.(blocked); return () => saveBlocked?.(false); }, [blocked, saveBlocked]);
  const formats = providerAPIFormats(metadata).filter(profile => (profile.authentication === APIAuthenticationId.Keyless) === (originalProfile?.authentication === APIAuthenticationId.Keyless));
  const selected = apiFormat(data.api_protocol) ?? originalProfile?.protocol ?? "";
  return <><label>{copy("configuration-fields.apiFormat")}<select value={selected} disabled={!canChange} onChange={event => { const protocol = apiFormat(event.target.value); if (protocol) change({ ...data, api_protocol: protocol }); }}>{!selected ? <option value="">{copy("configuration-fields.loadingApiFormats")}</option> : null}{selected && !formats.some(profile => profile.protocol === selected) ? <option value={selected}>{apiFormatLabels[selected]}</option> : null}{formats.map(profile => <option key={profile.protocol} value={profile.protocol}>{apiFormatLabels[profile.protocol]}</option>)}</select></label>
    <p>{copy("configuration-fields.accountFormatChangeHelp")}</p><p>{copy("configuration-fields.accountCredentialClassHelp")}</p>
    {!ready ? <p role="status">{inventory.isFetching ? copy("configuration-fields.loadingApiFormats") : copy("configuration-fields.apiFormatsUnavailable")}</p> : null}<Problem error={inventory.error || provider.error} />
  </>;
}
