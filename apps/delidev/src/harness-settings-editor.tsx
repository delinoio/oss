// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  APIFormatId,
  apiFormatMatchesHarness,
  apiFormatLabels,
  EntityKind,
  ResourceQuery,
  SubscriptionServiceId,
  SystemCapability,
  SystemQuery,
  supportsResourceSchema,
  providerAPIFormats,
  isEntityId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { document, items, object, type Document } from "./documents";
import { copy, useLocale, type MessageKey } from "./localization";
import { Problem } from "./ui";
import {
  codexScope,
  claudeScope,
  configuredHarnessValue,
  HarnessScopeSource,
  HarnessSettingMode,
  HarnessSettingsOwner,
  harnessScopeKey,
  matchingHarnessSettings,
  scopedHarnessSettings,
  setHarnessSelector,
  updateHarnessDefaults,
  type HarnessScope,
  type HarnessSettingsLayer,
  type HarnessRoutePreview,
} from "./harness-settings-model";
import "./harness-settings-editor.css";
import { NativeConfigurationImport } from "./native-configuration-import";

interface SettingField {
  key: string;
  label: MessageKey;
  choices?: readonly string[];
  capability?: SystemCapability;
  unsupported?: boolean;
  numeric?: boolean;
}
const codexFields: readonly SettingField[] = [
  {
    key: "web_search",
    label: "harness-settings.webSearch",
    choices: ["disabled", "cached", "live"],
    capability: SystemCapability.CODEX_WEB_SEARCH_V1,
  },
  { key: "model", label: "harness-settings.model" },
  { key: "effort", label: "harness-settings.effort" },
  {
    key: "permission",
    label: "harness-settings.permission",
    choices: ["default", "read-only", "workspace-write", "full-access"],
  },
  {
    key: "approval_policy",
    label: "harness-settings.approval",
    choices: ["", "untrusted", "on-failure", "on-request", "never"],
  },
  {
    key: "approvals_reviewer",
    label: "harness-settings.reviewer",
    choices: ["", "user", "auto_review"],
    capability: SystemCapability.CODEX_APPROVAL_REVIEW_V1,
  },
  {
    key: "subagent_model",
    label: "harness-settings.subagentModel",
    capability: SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1,
  },
  {
    key: "subagent_effort",
    label: "harness-settings.subagentEffort",
    capability: SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1,
  },
  {
    key: "max_concurrency",
    label: "harness-settings.concurrency",
    capability: SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1,
    numeric: true,
  },
  {
    key: "service_tier",
    label: "harness-settings.tier",
  },
  {
    key: "approval_review_model",
    label: "harness-settings.reviewModel",
    unsupported: true,
  },
];
const claudeFields: readonly SettingField[] = [
  { key: "model", label: "harness-settings.model" },
  { key: "effort", label: "harness-settings.effort" },
  {
    key: "claude_permission",
    label: "harness-settings.claudePermission",
    choices: [
      "default",
      "plan",
      "acceptEdits",
      "dontAsk",
      "bypassPermissions",
      "auto",
    ],
  },
];
const ownerLabels: Record<HarnessSettingsOwner, MessageKey> = {
  [HarnessSettingsOwner.Server]: "harness-settings.server",
  [HarnessSettingsOwner.Project]: "harness-settings.project",
  [HarnessSettingsOwner.Agent]: "harness-settings.agent",
};
const sourceLabels: Record<HarnessScopeSource, MessageKey> = {
  [HarnessScopeSource.Any]: "harness-settings.allSources",
  [HarnessScopeSource.Api]: "harness-settings.api",
  [HarnessScopeSource.Subscription]: "harness-settings.subscription",
};

export function HarnessSettingsEditor({
  data,
  change,
  owner,
  active,
  revision,
  routes = [],
  harness = "codex",
  saveBlocked,
}: {
  data: Document;
  change: (data: Document) => void;
  owner: HarnessSettingsOwner;
  active: boolean;
  revision?: string;
  routes?: HarnessRoutePreview[];
  harness?: HarnessScope["harness"];
  saveBlocked?: (blocked: boolean) => void;
}) {
  useLocale();
  const id = useId();
  const initialScope = harness === "claude-code" ? claudeScope : codexScope;
  const fields = harness === "claude-code" ? claudeFields : codexFields;
  const service =
    harness === "claude-code"
      ? SubscriptionServiceId.Claude
      : SubscriptionServiceId.ChatGPT;
  const serviceLabel = harness === "claude-code" ? "Claude" : "ChatGPT";
  // Scope selection is presentation state; preserve each harness preview without
  // allowing a category switch to write into the previously selected harness.
  const [scopes, setScopes] = useState<
    Partial<Record<HarnessScope["harness"], HarnessScope>>
  >({});
  const scope = scopes[harness] ?? initialScope;
  const setScope = (next: HarnessScope) =>
    setScopes((current) => ({ ...current, [harness]: next }));
  const [routeIndex, setRouteIndex] = useState(0);
  const route =
    owner === HarnessSettingsOwner.Agent
      ? (routes[routeIndex] ?? routes[0])
      : undefined;
  const selectedScope = route
    ? {
        ...route.scope,
        ...(scope.provider_id === route.scope.provider_id && scope.api_protocol
          ? { api_protocol: scope.api_protocol }
          : {}),
      }
    : scope;
  const [previewProject, setPreviewProject] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported =
    !status.error &&
    status.data?.capabilities.includes(SystemCapability.HARNESS_DEFAULTS_V1) ===
      true;
  const project = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.PROJECT, id: previewProject?.id ?? "" },
    {
      enabled: active && supported && Boolean(previewProject),
      refetchInterval: active ? 5000 : false,
      retry: false,
    },
  );
  const projectRow = project.data?.resource;
  const projectComplete =
    !previewProject ||
    Boolean(
      !project.error &&
      projectRow &&
      projectRow.id === previewProject.id &&
      projectRow.kind === EntityKind.PROJECT &&
      supportsResourceSchema(projectRow) &&
      projectRow.revision > 0n,
    );
  const server = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.SETTINGS, pageSize: 2 } },
    {
      enabled: active && supported && owner !== HarnessSettingsOwner.Server,
      refetchInterval: active ? 5000 : false,
    },
  );
  // Only a complete, supported singleton read can supply ancestor defaults.
  // Native defaults need execution-owned evidence and are never inferred here.
  const serverRows = server.data?.resources ?? [];
  const serverComplete = Boolean(
    server.data &&
    !server.error &&
    !server.data.nextPageToken &&
    serverRows.length <= 1 &&
    serverRows.every(
      (row) =>
        row.kind === EntityKind.SETTINGS &&
        isEntityId(row.id) &&
        supportsResourceSchema(row) &&
        row.revision > 0n,
    ),
  );
  const provider = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.PROVIDER, id: selectedScope.provider_id ?? "" },
    {
      enabled: active && supported && Boolean(selectedScope.provider_id),
      retry: false,
    },
  );
  const providerRow = provider.data?.resource;
  const providerComplete =
    !selectedScope.provider_id ||
    Boolean(
      !provider.error &&
      providerRow &&
      providerRow.id === selectedScope.provider_id &&
      providerRow.kind === EntityKind.PROVIDER &&
      supportsResourceSchema(providerRow) &&
      providerRow.revision > 0n,
    );
  const formats =
    !provider.error &&
    providerRow &&
    providerRow.id === selectedScope.provider_id &&
    providerRow.kind === EntityKind.PROVIDER &&
    supportsResourceSchema(providerRow)
      ? providerAPIFormats(document(providerRow))
          .filter((profile) =>
            apiFormatMatchesHarness(profile.protocol, harness),
          )
          .map((profile) => profile.protocol)
      : [];
  const defaults = object(data.harness_defaults);
  const settings =
    owner === HarnessSettingsOwner.Agent
      ? object(data.harness_settings)
      : scopedHarnessSettings(defaults, selectedScope);
  const layers: HarnessSettingsLayer[] = [
    {
      owner,
      revision,
      settings:
        owner === HarnessSettingsOwner.Agent
          ? [{ ...settings, ...(route ? { model: route.modelSelection } : {}) }]
          : matchingHarnessSettings(defaults, selectedScope),
    },
  ];
  if (
    owner === HarnessSettingsOwner.Agent &&
    previewProject &&
    projectComplete &&
    projectRow
  )
    layers.push({
      owner: HarnessSettingsOwner.Project,
      revision: String(projectRow.revision),
      settings: matchingHarnessSettings(
        document(projectRow).harness_defaults,
        selectedScope,
      ),
    });
  if (owner !== HarnessSettingsOwner.Server && serverComplete)
    layers.push({
      owner: HarnessSettingsOwner.Server,
      revision: serverRows[0] ? String(serverRows[0].revision) : undefined,
      settings: matchingHarnessSettings(
        document(serverRows[0]).harness_defaults,
        selectedScope,
      ),
    });
  const ancestryUnavailable =
    owner !== HarnessSettingsOwner.Server &&
    (!serverComplete || !projectComplete);
  const profileUnavailable = Boolean(
    route?.scope.provider_id && !selectedScope.api_protocol,
  );
  const malformed =
    (defaults.version !== undefined && defaults.version !== 1) ||
    (settings.version !== undefined && settings.version !== 1);
  const readsBlocked =
    !active ||
    !supported ||
    malformed ||
    ancestryUnavailable ||
    !providerComplete ||
    Boolean(status.error || server.error || provider.error || project.error);
  // The form owns Save and Discard. A failed or incomplete inheritance read
  // must also block submission of an already dirty draft until a read retry.
  useEffect(() => {
    saveBlocked?.(readsBlocked);
    return () => saveBlocked?.(false);
  }, [readsBlocked, saveBlocked]);
  const entries = items(defaults.entries)
    .map(object)
    .filter((entry) => object(entry.scope).harness === harness);
  const update = (next: Document) =>
    change(
      owner === HarnessSettingsOwner.Agent
        ? { ...data, harness_settings: next }
        : {
            ...data,
            harness_defaults: updateHarnessDefaults(
              defaults,
              selectedScope,
              next,
            ),
          },
    );
  return (
    <section
      className="harness-settings-editor"
      data-settings-search-target={`${harness}-harness`}
      aria-labelledby={`${id}-title`}
      data-settings-search-pending={
        !status.data && !status.error ? "true" : undefined
      }
    >
      <h3 id={`${id}-title`}>
        {copy(
          harness === "claude-code"
            ? "harness-settings.claude"
            : "harness-settings.codex",
        )}
      </h3>
      <p>
        {copy(
          harness === "claude-code"
            ? "harness-settings.claudeHelp"
            : "harness-settings.help",
        )}
      </p>
      <p>{copy("harness-settings.changesApply")}</p>
      <NativeConfigurationImport active={active} initialScope={selectedScope} />
      <Problem
        error={status.error || server.error || provider.error || project.error}
      />
      {status.error || server.error || provider.error || project.error ? (
        <button
          type="button"
          onClick={() => {
            if (status.error) void status.refetch();
            else if (server.error) void server.refetch();
            else if (provider.error) void provider.refetch();
            else void project.refetch();
          }}
        >
          {copy("harness-settings.retryRead")}
        </button>
      ) : null}
      {!supported || malformed ? (
        <p role="status">{copy("harness-settings.updateRequired")}</p>
      ) : null}
      {owner === HarnessSettingsOwner.Agent ? (
        <p>{copy("harness-settings.agentScopeHelp")}</p>
      ) : null}
      <fieldset disabled={readsBlocked} className="harness-scope">
        <legend>
          {copy(
            owner === HarnessSettingsOwner.Agent
              ? "harness-settings.previewScope"
              : "harness-settings.sourceScope",
          )}
        </legend>
        {owner !== HarnessSettingsOwner.Agent && entries.length > 0 ? (
          <label>
            {copy("harness-settings.savedScopes")}
            <select
              value={
                entries.some(
                  (entry) =>
                    harnessScopeKey(
                      object(entry.scope) as unknown as HarnessScope,
                    ) === harnessScopeKey(scope),
                )
                  ? harnessScopeKey(scope)
                  : ""
              }
              onChange={(event) => {
                const selected = entries.find(
                  (entry) =>
                    harnessScopeKey(
                      object(entry.scope) as unknown as HarnessScope,
                    ) === event.target.value,
                );
                if (selected)
                  setScope(object(selected.scope) as unknown as HarnessScope);
              }}
            >
              <option value="">{copy("harness-settings.newScope")}</option>
              {entries.map((entry, index) => {
                const saved = object(entry.scope) as unknown as HarnessScope;
                return (
                  <option
                    key={harnessScopeKey(saved)}
                    value={harnessScopeKey(saved)}
                  >
                    {copy(
                      saved.source === HarnessScopeSource.Any &&
                        harness === "claude-code"
                        ? "harness-settings.claudeAllSources"
                        : sourceLabels[saved.source],
                    )}{" "}
                    · {index + 1}
                    {saved.api_protocol
                      ? ` · ${apiFormatLabels[saved.api_protocol as APIFormatId]}`
                      : ""}
                  </option>
                );
              })}
            </select>
          </label>
        ) : null}
        {route ? (
          <label>
            {copy("harness-settings.source")}
            <select
              value={routeIndex}
              onChange={(event) => {
                setRouteIndex(Number(event.target.value));
                setScope(initialScope);
              }}
            >
              {routes.map((entry, index) => (
                <option key={index} value={index}>
                  {copy("harness-settings.routeSource", {
                    position: index + 1,
                  })}{" "}
                  ·{" "}
                  {entry.label ??
                    (entry.scope.source === HarnessScopeSource.Api
                      ? copy("harness-settings.api")
                      : serviceLabel)}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <label>
            {copy("harness-settings.source")}
            <select
              value={scope.source}
              onChange={(event) =>
                setScope({
                  harness,
                  source: event.target.value as HarnessScopeSource,
                })
              }
            >
              {Object.values(HarnessScopeSource).map((source) => (
                <option key={source} value={source}>
                  {copy(
                    source === HarnessScopeSource.Any &&
                      harness === "claude-code"
                      ? "harness-settings.claudeAllSources"
                      : sourceLabels[source],
                  )}
                </option>
              ))}
            </select>
          </label>
        )}
        {selectedScope.source === HarnessScopeSource.Api ? (
          <>
            {!route ? (
              <ResourceChoice
                label={copy("harness-settings.provider")}
                resourceLabel={copy("harness-settings.provider")}
                emptyLabel={copy("harness-settings.allProviders")}
                kind={EntityKind.PROVIDER}
                value={scope.provider_id ?? ""}
                active={active}
                change={(provider_id) =>
                  setScope({
                    harness,
                    source: HarnessScopeSource.Api,
                    ...(provider_id ? { provider_id } : {}),
                  })
                }
              />
            ) : null}
            {selectedScope.provider_id ? (
              <label>
                {copy("harness-settings.protocol")}
                <select
                  value={selectedScope.api_protocol ?? ""}
                  onChange={(event) => {
                    const next = { ...selectedScope };
                    if (event.target.value)
                      next.api_protocol = event.target.value;
                    else delete next.api_protocol;
                    setScope(next);
                  }}
                >
                  <option value="">
                    {copy("harness-settings.allProtocols")}
                  </option>
                  {selectedScope.api_protocol &&
                  !formats.includes(
                    selectedScope.api_protocol as APIFormatId,
                  ) ? (
                    <option value={selectedScope.api_protocol}>
                      {selectedScope.api_protocol} ·{" "}
                      {copy("harness-settings.unsupported")}
                    </option>
                  ) : null}
                  {formats.map((protocol) => (
                    <option key={protocol} value={protocol}>
                      {apiFormatLabels[protocol]}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
          </>
        ) : null}
        {!route && scope.source === HarnessScopeSource.Subscription ? (
          <label>
            {copy("harness-settings.service")}
            <select
              value={scope.subscription_service ?? ""}
              onChange={(event) =>
                setScope({
                  harness,
                  source: HarnessScopeSource.Subscription,
                  ...(event.target.value
                    ? { subscription_service: event.target.value }
                    : {}),
                })
              }
            >
              <option value="">
                {copy(
                  harness === "claude-code"
                    ? "harness-settings.claudeAllServices"
                    : "harness-settings.allServices",
                )}
              </option>
              {scope.subscription_service &&
              scope.subscription_service !== service ? (
                <option value={scope.subscription_service}>
                  {scope.subscription_service} ·{" "}
                  {copy("harness-settings.unsupported")}
                </option>
              ) : null}
              <option value={service}>{serviceLabel}</option>
            </select>
          </label>
        ) : null}
        {owner === HarnessSettingsOwner.Agent ? (
          <ResourceChoice
            label={copy("harness-settings.previewProject")}
            resourceLabel={copy("harness-settings.project")}
            emptyLabel={copy("harness-settings.noProject")}
            kind={EntityKind.PROJECT}
            value={previewProject?.id ?? ""}
            active={active}
            change={(_id, _data, resource) =>
              setPreviewProject(
                resource &&
                  resource.kind === EntityKind.PROJECT &&
                  isEntityId(resource.id) &&
                  supportsResourceSchema(resource) &&
                  resource.revision > 0n
                  ? resource
                  : undefined,
              )
            }
          />
        ) : null}
      </fieldset>
      <p>{copy("harness-settings.precedence")}</p>
      {profileUnavailable ? (
        <p role="status">{copy("harness-settings.profileRequired")}</p>
      ) : null}
      {ancestryUnavailable ? (
        <p role="status">{copy("harness-settings.ancestryUnavailable")}</p>
      ) : null}
      <fieldset disabled={readsBlocked} className="harness-setting-fields">
        <legend>{copy(ownerLabels[owner])}</legend>
        {fields.map((field) => {
          const selector = object(settings[field.key]);
          const overridden = selector.mode === HarnessSettingMode.Override;
          const configured = configuredHarnessValue(field.key, layers);
          // A missing higher layer cannot be skipped to claim a lower effective value.
          const effective =
            configured &&
            (!profileUnavailable ||
              configured.owner === HarnessSettingsOwner.Agent) &&
            (projectComplete || configured.owner === owner) &&
            (!ancestryUnavailable ||
              configured.owner === owner ||
              configured.owner === HarnessSettingsOwner.Project)
              ? configured
              : undefined;
          const available =
            !field.unsupported &&
            (!field.capability ||
              status.data?.capabilities.includes(field.capability) === true);
          const routeModel =
            owner === HarnessSettingsOwner.Agent && field.key === "model";
          const value = overridden ? selector.value : effective?.value;
          const controlValue = field.numeric
            ? typeof value === "number"
              ? value
              : ""
            : typeof value === "string"
              ? value
              : "";
          const unsupportedValue =
            overridden &&
            field.choices &&
            !field.choices.includes(String(controlValue));
          const webSearch = field.key === "web_search";
          const nativeSearchSource =
            selectedScope.source === HarnessScopeSource.Subscription &&
            (!selectedScope.subscription_service ||
              selectedScope.subscription_service ===
                SubscriptionServiceId.ChatGPT);
          const settingId = `${id}-${field.key}`;
          const set = (
            mode: HarnessSettingMode,
            nextValue: string | number = field.numeric && controlValue === ""
              ? 0
              : (field.key === "permission" ||
                    field.key === "claude_permission") &&
                  controlValue === ""
                ? "default"
                : controlValue,
          ) => {
            if (
              readsBlocked ||
              routeModel ||
              (mode === HarnessSettingMode.Override && !available)
            )
              return;
            update(setHarnessSelector(settings, field.key, mode, nextValue));
          };
          const heading: MessageKey | undefined = webSearch
            ? "harness-settings.webSearch"
            : field.key === "model"
              ? "harness-settings.modelSection"
              : field.key === "claude_permission"
                ? "harness-settings.claudePermission"
                : field.key === "permission"
                  ? "harness-settings.permissionSection"
                  : field.key === "subagent_model"
                    ? "harness-settings.subagentSection"
                    : field.key === "service_tier"
                      ? "harness-settings.tier"
                      : undefined;
          return (
            <section
              key={field.key}
              className="harness-setting"
              data-settings-search-target={`${harness}-${field.key}`}
            >
              {heading ? <h4>{copy(heading)}</h4> : null}
              {webSearch ? null : <h5>{copy(field.label)}</h5>}
              {webSearch ? (
                <label htmlFor={settingId}>
                  {copy("harness-settings.webSearch")}
                  <select
                    id={settingId}
                    aria-label={copy("harness-settings.webSearch")}
                    value={overridden ? String(controlValue) : "inherit"}
                    disabled={!available}
                    onChange={(event) => {
                      const value = event.target.value;
                      if (value === "inherit") set(HarnessSettingMode.Inherit);
                      else if (
                        value === "disabled" ||
                        (nativeSearchSource &&
                          ["cached", "live"].includes(value))
                      )
                        set(HarnessSettingMode.Override, value);
                    }}
                  >
                    <option value="inherit">
                      {copy("harness-settings.useInherited")}
                    </option>
                    {unsupportedValue ? (
                      <option value={String(controlValue)} disabled>
                        {String(controlValue)} ·{" "}
                        {copy("harness-settings.unsupported")}
                      </option>
                    ) : null}
                    <option value="disabled">
                      {copy("harness-settings.searchDisabled")}
                    </option>
                    <option value="cached" disabled={!nativeSearchSource}>
                      {copy("harness-settings.searchCached")}
                    </option>
                    <option value="live" disabled={!nativeSearchSource}>
                      {copy("harness-settings.searchLive")}
                    </option>
                  </select>
                  {!nativeSearchSource ? (
                    <p>{copy("harness-settings.searchSourceUnavailable")}</p>
                  ) : (
                    <p>{copy("harness-settings.searchPolicy")}</p>
                  )}
                </label>
              ) : routeModel ? (
                <p>{copy("harness-settings.routeModel")}</p>
              ) : (
                <div className="harness-setting-actions">
                  <label>
                    {copy("harness-settings.selection")}
                    <select
                      aria-label={`${copy(field.label)}: ${copy("harness-settings.selection")}`}
                      value={
                        overridden
                          ? HarnessSettingMode.Override
                          : HarnessSettingMode.Inherit
                      }
                      disabled={!available}
                      onChange={(event) =>
                        set(event.target.value as HarnessSettingMode)
                      }
                    >
                      <option value={HarnessSettingMode.Inherit}>
                        {copy("harness-settings.inherit")}
                      </option>
                      <option value={HarnessSettingMode.Override}>
                        {copy("harness-settings.override")}
                      </option>
                    </select>
                  </label>
                  <button
                    type="button"
                    disabled={!overridden}
                    onClick={() => set(HarnessSettingMode.Inherit)}
                  >
                    {copy("harness-settings.reset")}
                  </button>
                </div>
              )}
              {!routeModel && !webSearch ? (
                <label htmlFor={settingId}>
                  {copy("harness-settings.value")}
                  {field.choices ? (
                    <select
                      id={settingId}
                      aria-label={`${copy(field.label)}: ${copy("harness-settings.value")}`}
                      value={controlValue}
                      disabled={!available || !overridden}
                      onChange={(event) =>
                        set(HarnessSettingMode.Override, event.target.value)
                      }
                    >
                      {!overridden &&
                      controlValue === "" &&
                      !field.choices.includes("") ? (
                        <option value="">
                          {copy("harness-settings.inherit")}
                        </option>
                      ) : null}
                      {unsupportedValue ? (
                        <option value={String(controlValue)}>
                          {String(controlValue)} ·{" "}
                          {copy("harness-settings.unsupported")}
                        </option>
                      ) : null}
                      {field.choices.map((choice) => (
                        <option key={choice} value={choice}>
                          {choice || copy("harness-settings.empty")}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      id={settingId}
                      aria-label={`${copy(field.label)}: ${copy("harness-settings.value")}`}
                      type={field.numeric ? "number" : "text"}
                      maxLength={field.numeric ? undefined : 256}
                      min={field.numeric ? 0 : undefined}
                      max={field.numeric ? 4294967295 : undefined}
                      step={field.numeric ? 1 : undefined}
                      value={controlValue}
                      disabled={!available || !overridden}
                      onChange={(event) =>
                        set(
                          HarnessSettingMode.Override,
                          field.numeric
                            ? Number(event.target.value)
                            : event.target.value,
                        )
                      }
                    />
                  )}
                </label>
              ) : null}
              {field.key === "approvals_reviewer" ? (
                <p>{copy("harness-settings.reviewerHelp")}</p>
              ) : null}
              {!available || unsupportedValue ? (
                <p role="status">{copy("harness-settings.unsupportedHelp")}</p>
              ) : null}
              <p className="harness-setting-effective">
                {effective
                  ? copy("harness-settings.effective", {
                      value:
                        effective.value === ""
                          ? copy("harness-settings.empty")
                          : String(effective.value),
                      source: copy(ownerLabels[effective.owner]),
                      revision: effective.revision ?? "—",
                    })
                  : copy(
                      ancestryUnavailable || profileUnavailable
                        ? "harness-settings.unknownEffective"
                        : "harness-settings.nativeUnverified",
                    )}
              </p>
            </section>
          );
        })}
      </fieldset>
      <section aria-labelledby={`${id}-hooks`}>
        <h4 id={`${id}-hooks`}>{copy("harness-settings.hooks")}</h4>
        <p>{copy("harness-settings.hooksUnavailable")}</p>
      </section>
      <p>{copy("harness-settings.executionGate")}</p>
    </section>
  );
}
