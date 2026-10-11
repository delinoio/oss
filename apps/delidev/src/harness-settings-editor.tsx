// SPDX-License-Identifier: Apache-2.0
import { useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  APIFormatId,
  apiFormatLabels,
  EntityKind,
  ResourceQuery,
  SubscriptionServiceId,
  SystemCapability,
  SystemQuery,
  supportsResourceSchema,
  isEntityId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { document, items, object, type Document } from "./documents";
import { copy, useLocale, type MessageKey } from "./localization";
import { Problem } from "./ui";
import {
  codexScope,
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
} from "./harness-settings-model";
import "./harness-settings-editor.css";

interface SettingField {
  key: string;
  label: MessageKey;
  choices?: readonly string[];
  capability?: SystemCapability;
  unsupported?: boolean;
  numeric?: boolean;
}
const fields: readonly SettingField[] = [
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
    choices: ["", "fast"],
  },
  {
    key: "approval_review_model",
    label: "harness-settings.reviewModel",
    unsupported: true,
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
}: {
  data: Document;
  change: (data: Document) => void;
  owner: HarnessSettingsOwner;
  active: boolean;
  revision?: string;
}) {
  useLocale();
  const id = useId();
  const [scope, setScope] = useState<HarnessScope>(codexScope);
  const [previewProject, setPreviewProject] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported =
    status.data?.capabilities.includes(SystemCapability.HARNESS_DEFAULTS_V1) ===
    true;
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
  const defaults = object(data.harness_defaults);
  const settings =
    owner === HarnessSettingsOwner.Agent
      ? object(data.harness_settings)
      : scopedHarnessSettings(defaults, scope);
  const layers: HarnessSettingsLayer[] = [
    {
      owner,
      revision,
      settings:
        owner === HarnessSettingsOwner.Agent
          ? [settings]
          : matchingHarnessSettings(defaults, scope),
    },
  ];
  if (owner === HarnessSettingsOwner.Agent && previewProject)
    layers.push({
      owner: HarnessSettingsOwner.Project,
      revision: String(previewProject.revision),
      settings: matchingHarnessSettings(
        document(previewProject).harness_defaults,
        scope,
      ),
    });
  if (owner !== HarnessSettingsOwner.Server && serverComplete)
    layers.push({
      owner: HarnessSettingsOwner.Server,
      revision: serverRows[0] ? String(serverRows[0].revision) : undefined,
      settings: matchingHarnessSettings(
        document(serverRows[0]).harness_defaults,
        scope,
      ),
    });
  const ancestryUnavailable =
    owner !== HarnessSettingsOwner.Server && !serverComplete;
  const malformed =
    (defaults.version !== undefined && defaults.version !== 1) ||
    (settings.version !== undefined && settings.version !== 1);
  const entries = items(defaults.entries)
    .map(object)
    .filter((entry) => object(entry.scope).harness === "codex");
  const update = (next: Document) =>
    change(
      owner === HarnessSettingsOwner.Agent
        ? { ...data, harness_settings: next }
        : {
            ...data,
            harness_defaults: updateHarnessDefaults(defaults, scope, next),
          },
    );
  return (
    <section
      className="harness-settings-editor"
      data-settings-search-target="codex-harness"
      aria-labelledby={`${id}-title`}
      data-settings-search-pending={
        !status.data && !status.error ? "true" : undefined
      }
    >
      <h3 id={`${id}-title`}>{copy("harness-settings.codex")}</h3>
      <p>{copy("harness-settings.help")}</p>
      <Problem error={status.error || server.error} />
      {!supported || malformed ? (
        <p role="status">{copy("harness-settings.updateRequired")}</p>
      ) : null}
      {owner === HarnessSettingsOwner.Agent ? (
        <p>{copy("harness-settings.agentScopeHelp")}</p>
      ) : null}
      <fieldset disabled={!supported || malformed} className="harness-scope">
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
                    {copy(sourceLabels[saved.source])} · {index + 1}
                    {saved.api_protocol
                      ? ` · ${apiFormatLabels[saved.api_protocol as APIFormatId]}`
                      : ""}
                  </option>
                );
              })}
            </select>
          </label>
        ) : null}
        <label>
          {copy("harness-settings.source")}
          <select
            value={scope.source}
            onChange={(event) =>
              setScope({
                harness: "codex",
                source: event.target.value as HarnessScopeSource,
              })
            }
          >
            {Object.values(HarnessScopeSource).map((source) => (
              <option key={source} value={source}>
                {copy(sourceLabels[source])}
              </option>
            ))}
          </select>
        </label>
        {scope.source === HarnessScopeSource.Api ? (
          <>
            <ResourceChoice
              label={copy("harness-settings.provider")}
              resourceLabel={copy("harness-settings.provider")}
              emptyLabel={copy("harness-settings.allProviders")}
              kind={EntityKind.PROVIDER}
              value={scope.provider_id ?? ""}
              active={active}
              change={(provider_id) =>
                setScope({
                  harness: "codex",
                  source: HarnessScopeSource.Api,
                  ...(provider_id ? { provider_id } : {}),
                })
              }
            />
            {scope.provider_id ? (
              <label>
                {copy("harness-settings.protocol")}
                <select
                  value={scope.api_protocol ?? ""}
                  onChange={(event) => {
                    const next = { ...scope };
                    if (event.target.value)
                      next.api_protocol = event.target.value;
                    else delete next.api_protocol;
                    setScope(next);
                  }}
                >
                  <option value="">
                    {copy("harness-settings.allProtocols")}
                  </option>
                  {Object.values(APIFormatId).map((protocol) => (
                    <option key={protocol} value={protocol}>
                      {apiFormatLabels[protocol]}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
          </>
        ) : null}
        {scope.source === HarnessScopeSource.Subscription ? (
          <label>
            {copy("harness-settings.service")}
            <select
              value={scope.subscription_service ?? ""}
              onChange={(event) =>
                setScope({
                  harness: "codex",
                  source: HarnessScopeSource.Subscription,
                  ...(event.target.value
                    ? { subscription_service: event.target.value }
                    : {}),
                })
              }
            >
              <option value="">{copy("harness-settings.allServices")}</option>
              <option value={SubscriptionServiceId.ChatGPT}>ChatGPT</option>
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
      {ancestryUnavailable ? (
        <p role="status">{copy("harness-settings.ancestryUnavailable")}</p>
      ) : null}
      <fieldset
        disabled={!supported || malformed}
        className="harness-setting-fields"
      >
        <legend>{copy(ownerLabels[owner])}</legend>
        {fields.map((field) => {
          const selector = object(settings[field.key]);
          const overridden = selector.mode === HarnessSettingMode.Override;
          const configured = configuredHarnessValue(field.key, layers);
          // A missing higher layer cannot be skipped to claim a lower effective value.
          const effective =
            configured &&
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
          const settingId = `${id}-${field.key}`;
          const set = (
            mode: HarnessSettingMode,
            nextValue: string | number = field.numeric && controlValue === ""
              ? 0
              : field.key === "permission" && controlValue === ""
                ? "default"
                : controlValue,
          ) => update(setHarnessSelector(settings, field.key, mode, nextValue));
          return (
            <section
              key={field.key}
              className="harness-setting"
              data-settings-search-target={`codex-${field.key}`}
            >
              <h4>{copy(field.label)}</h4>
              {routeModel ? (
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
              {!routeModel ? (
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
                      ancestryUnavailable
                        ? "harness-settings.unknownEffective"
                        : "harness-settings.nativeUnverified",
                    )}
              </p>
            </section>
          );
        })}
      </fieldset>
      <p>{copy("harness-settings.executionGate")}</p>
    </section>
  );
}
