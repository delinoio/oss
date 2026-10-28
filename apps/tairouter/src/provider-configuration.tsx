// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { create } from "@bufbuild/protobuf";
import {
  ResponsesItemIdRepairSchema,
  ProviderConfigurationSchema,
  ProviderPolicyOverridesSchema,
  ProviderCustomHeadersSchema,
  ProviderWire,
  type ProviderConnection,
  type ProviderConfiguration,
  type ProviderPolicyOverrides,
} from "@tairouter/client";
import { Button, Field, Notice } from "./ui";
export enum ConfigurationMode {
  Preserve = "preserve",
  Replace = "replace",
  Reset = "reset",
}
export enum HeaderMode {
  Preserve = "preserve",
  Replace = "replace",
  Clear = "clear",
}
export type ConfigurationDraft = {
  mode: ConfigurationMode;
  configuration: ProviderConfiguration;
  models: { id: string; wire: ProviderWire }[];
  serviceTiers: { id: string; supported: boolean }[];
  floor: string;
  headerMode: HeaderMode;
  headers: { name: string; value: string }[];
};
export function configurationDraft(
  connection?: ProviderConnection,
): ConfigurationDraft {
  const configuration = create(
    ProviderConfigurationSchema,
    connection?.configuration,
  );
  return {
    mode: ConfigurationMode.Preserve,
    configuration,
    models: Object.entries(configuration.modelWires).map(([id, wire]) => ({
      id,
      wire,
    })),
    serviceTiers: Object.entries(
      configuration.policy?.modelSupportsServiceTier ?? {},
    ).map(([id, supported]) => ({ id, supported })),
    floor: configuration.minMaxOutputTokens?.toString() ?? "",
    headerMode: HeaderMode.Preserve,
    headers: [],
  };
}
export function configurationValues(draft: ConfigurationDraft) {
  const { mode, headerMode } = draft;
  const modelWires: Record<string, ProviderWire> = Object.create(null);
  if (mode === ConfigurationMode.Replace)
    for (const row of draft.models) {
      const id = row.id.trim();
      if (!id || Object.hasOwn(modelWires, id))
        throw new Error(translate("modelIdsMustBeNonemptyAndUnique"));
      if (row.wire !== ProviderWire.CHAT && row.wire !== ProviderWire.RESPONSES)
        throw new Error(
          translate("modelOverridesSupportOnlyChatCompletionsOr"),
        );
      modelWires[id] = row.wire;
    }
  const modelSupportsServiceTier: Record<string, boolean> = Object.create(null);
  const modelNames = new Set<string>();
  if (mode === ConfigurationMode.Replace)
    for (const row of draft.serviceTiers) {
      if (!row.id.trim() || modelNames.has(row.id.toLowerCase()))
        throw new Error(translate("message_36682276f6"));
      modelNames.add(row.id.toLowerCase());
      modelSupportsServiceTier[row.id] = row.supported;
    }
  const values: Record<string, string> = Object.create(null);
  const names = new Set<string>();
  if (headerMode === HeaderMode.Replace)
    for (const row of draft.headers) {
      const name = row.name.trim();
      if (
        !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) ||
        names.has(name.toLowerCase()) ||
        /[\r\n]/.test(row.value)
      )
        throw new Error(translate("headerNamesMustBeValidAndUnique"));
      names.add(name.toLowerCase());
      values[name] = row.value;
    }
  const floor = draft.floor.trim();
  if (
    mode === ConfigurationMode.Replace &&
    floor &&
    (!/^\d+$/.test(floor) || BigInt(floor) < 1n || BigInt(floor) > 2147483647n)
  )
    throw new Error(translate("theAzureOutputTokenFloorMustBe"));
  return {
    configuration:
      mode === ConfigurationMode.Replace
        ? create(ProviderConfigurationSchema, {
            ...draft.configuration,
            modelWires,
            policy:
              draft.configuration.policy || draft.serviceTiers.length
                ? create(ProviderPolicyOverridesSchema, {
                    ...create(
                      ProviderPolicyOverridesSchema,
                      draft.configuration.policy,
                    ),
                    modelSupportsServiceTier,
                  })
                : undefined,
            minMaxOutputTokens: floor ? BigInt(floor) : undefined,
          })
        : undefined,
    resetConfiguration: mode === ConfigurationMode.Reset,
    customHeaders:
      headerMode === HeaderMode.Preserve
        ? undefined
        : create(ProviderCustomHeadersSchema, { values }),
  };
}
const wires = () =>
  [
    [ProviderWire.CHAT, translate("chatCompletions")],
    [ProviderWire.RESPONSES, "Responses"],
    [ProviderWire.ANTHROPIC, "Anthropic"],
    [ProviderWire.GOOGLE, "Google"],
    [ProviderWire.JEV_DECISION, translate("jevDecisionService")],
  ] as const;
const policies = () =>
  [
    ["foldDeveloperRoleToSystem", translate("foldDeveloperRoleIntoSystem")],
    ["dropResponsesReasoningItems", translate("dropResponsesReasoningItems")],
    [
      "preserveResponsesReasoningContent",
      translate("preserveResponsesReasoningContent"),
    ],
    [
      "requiresAdjacentResponsesToolResults",
      translate("requireAdjacentResponsesToolResults"),
    ],
    [
      "requiresPairedResponsesToolResults",
      translate("requirePairedResponsesToolResults"),
    ],
    ["statelessResponses", translate("statelessResponses")],
    ["annotateEmptyToolOutputs", translate("annotateEmptyToolOutputs")],
    ["parallelToolCalls", translate("parallelToolCalls")],
    ["promptCacheKey", translate("promptCacheKey")],
    ["chatServiceTier", translate("chatServiceTier")],
    ["fastEnabled", translate("fastModePolicy")],
    ["supportsServiceTier", translate("serviceTierSupport")],
    ["supportsVerbosity", translate("verbositySupport")],
    ["supportsResponsesCustomTools", translate("responsesCustomTools")],
    ["supportsOpenAiWebSearchToolFields", translate("openaiWebSearchFields")],
    ["showThinkingSummary", translate("showThinkingSummary")],
    ["escapeBuiltinToolNames", translate("escapeBuiltInToolNames")],
  ] as const satisfies readonly (readonly [
    keyof ProviderPolicyOverrides,
    string,
  ])[];
export function ConfigurationFields({
  draft,
  onChange,
  providerId,
  connection,
}: {
  draft: ConfigurationDraft;
  onChange: (draft: ConfigurationDraft) => void;
  providerId: string;
  connection?: ProviderConnection;
}) {
  useLocale();
  const config = draft.configuration;
  const repair = config.policy?.responsesItemIdRepair;
  const update = (patch: Partial<ProviderConfiguration>) =>
    onChange({
      ...draft,
      mode: ConfigurationMode.Replace,
      configuration: create(ProviderConfigurationSchema, {
        ...config,
        ...patch,
      }),
    });
  return (
    <>
      <Field
        label={translate("apiFormat")}
        hint={translate("providerDefaultIsChatForCustomConnections")}
      >
        <select
          value={config.wire ?? ""}
          disabled={draft.mode === ConfigurationMode.Reset}
          onChange={(e) =>
            update({
              wire: e.target.value === "" ? undefined : Number(e.target.value),
            })
          }
        >
          <option value="">{translate("providerDefault")}</option>
          {wires().map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      </Field>
      <details className="advanced-settings">
        <summary>{translate("advancedConnectionSettings")}</summary>
        {connection && (
          <Field label={translate("configurationChanges")}>
            <select
              value={draft.mode}
              onChange={(e) =>
                onChange({
                  ...draft,
                  mode: e.target.value as ConfigurationMode,
                })
              }
            >
              <option value={ConfigurationMode.Preserve}>
                {translate("keepSavedConfiguration")}
              </option>
              <option value={ConfigurationMode.Replace}>
                {translate("replaceConfigurationWithTheseValues")}
              </option>
              <option value={ConfigurationMode.Reset}>
                {translate("resetConfigurationToProviderDefaults")}
              </option>
            </select>
          </Field>
        )}
        {draft.mode === ConfigurationMode.Reset ? (
          <Notice>
            {translate("resetRemovesConfigurationOverridesCustomHeadersAre")}
          </Notice>
        ) : (
          <>
            <p>
              {translate("changingASettingReplacesTheCompleteConfiguration")}
            </p>
            <details className="advanced-disclosure">
              <summary>{translate("endpointsAndProviderSettings")}</summary>
              <Field
                label={translate("responsesEndpointPath")}
                hint={translate("originRelativePathForExampleV1Responses")}
              >
                <input
                  value={config.responsesPath ?? ""}
                  onChange={(e) =>
                    update({ responsesPath: e.target.value || undefined })
                  }
                />
              </Field>
              <Field label={translate("chatCompletionsEndpointPath")}>
                <input
                  value={config.chatCompletionsPath ?? ""}
                  onChange={(e) =>
                    update({ chatCompletionsPath: e.target.value || undefined })
                  }
                />
              </Field>
              {providerId === "google-vertex" && (
                <fieldset>
                  <legend>{translate("vertexAi")}</legend>
                  <p>
                    {translate("oauthAndServiceAccountCredentialsRequireAn")}
                  </p>
                  {(
                    [
                      ["vertexProject", translate("vertexProject")],
                      ["vertexLocation", translate("vertexLocation")],
                    ] as const
                  ).map(([key, label]) => (
                    <div key={key}>
                      <label>
                        <input
                          type="checkbox"
                          checked={config[key] !== undefined}
                          onChange={(e) =>
                            update({ [key]: e.target.checked ? "" : undefined })
                          }
                        />
                        {translate("override", { value0: label.toLowerCase() })}
                      </label>
                      {config[key] !== undefined && (
                        <Field label={label}>
                          <input
                            value={config[key]}
                            onChange={(e) => update({ [key]: e.target.value })}
                          />
                        </Field>
                      )}
                    </div>
                  ))}
                </fieldset>
              )}
              {providerId === "azure" && (
                <Field
                  label={translate("minimumOutputTokenBudget")}
                  hint={translate("optionalAzureFloorSubjectToRequestLimits")}
                >
                  <input
                    type="number"
                    min="1"
                    max="2147483647"
                    step="1"
                    value={draft.floor}
                    onChange={(e) =>
                      onChange({
                        ...draft,
                        mode: ConfigurationMode.Replace,
                        floor: e.target.value,
                      })
                    }
                  />
                </Field>
              )}
            </details>
            <details className="advanced-disclosure">
              <summary>{translate("modelApiFormats")}</summary>
              <p>{translate("overridesApplyToExactModelIds")}</p>
              {draft.models.map((row, index) => (
                <fieldset key={index}>
                  <legend>
                    {translate("modelOverride", { value0: index + 1 })}
                  </legend>
                  <Field
                    label={translate("modelId_64bc8b", { value0: index + 1 })}
                  >
                    <input
                      required
                      value={row.id}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          mode: ConfigurationMode.Replace,
                          models: draft.models.map((r, i) =>
                            i === index ? { ...r, id: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Field
                    label={translate("modelFormat", { value0: index + 1 })}
                  >
                    <select
                      value={row.wire}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          mode: ConfigurationMode.Replace,
                          models: draft.models.map((r, i) =>
                            i === index
                              ? { ...r, wire: Number(e.target.value) }
                              : r,
                          ),
                        })
                      }
                    >
                      {wires()
                        .filter(
                          ([wire]) =>
                            wire === ProviderWire.CHAT ||
                            wire === ProviderWire.RESPONSES,
                        )
                        .map(([value, label]) => (
                          <option key={value} value={value}>
                            {label}
                          </option>
                        ))}
                    </select>
                  </Field>
                  <Button
                    onClick={() =>
                      onChange({
                        ...draft,
                        mode: ConfigurationMode.Replace,
                        models: draft.models.filter((_, i) => i !== index),
                      })
                    }
                  >
                    {translate("removeModelOverride", { value0: index + 1 })}
                  </Button>
                </fieldset>
              ))}
              <Button
                onClick={() =>
                  onChange({
                    ...draft,
                    mode: ConfigurationMode.Replace,
                    models: [
                      ...draft.models,
                      { id: "", wire: ProviderWire.CHAT },
                    ],
                  })
                }
              >
                {translate("addModelOverride")}
              </Button>
            </details>
            <details className="advanced-disclosure">
              <summary>{translate("requestCompatibilityPolicies")}</summary>
              <p>{translate("inheritUsesTheProviderDefaultDisabledIs")}</p>
              {policies().map(([key, label]) => (
                <Field key={key} label={label}>
                  <select
                    value={
                      config.policy?.[key] === undefined
                        ? ""
                        : String(config.policy[key])
                    }
                    onChange={(e) =>
                      update({
                        policy: create(ProviderPolicyOverridesSchema, {
                          ...create(
                            ProviderPolicyOverridesSchema,
                            config.policy,
                          ),
                          [key]:
                            e.target.value === ""
                              ? undefined
                              : e.target.value === "true",
                        }),
                      })
                    }
                  >
                    <option value="">{translate("inherit")}</option>
                    <option value="true">{translate("enabled")}</option>
                    <option value="false">{translate("disabled")}</option>
                  </select>
                </Field>
              ))}
              <p>{translate("fastModeRequiresASupportedModelAnd")}</p>
              <h3>{translate("modelServiceTierSupport")}</h3>
              <p>{translate("useUpstreamModelIdsNotRouteAliases")}</p>
              {draft.serviceTiers.map((row, index) => (
                <fieldset key={index}>
                  <legend>
                    {translate("serviceTierOverride", { value0: index + 1 })}
                  </legend>
                  <Field label={`Service-tier model ID ${index + 1}`}>
                    <input
                      required
                      value={row.id}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          mode: ConfigurationMode.Replace,
                          serviceTiers: draft.serviceTiers.map((r, i) =>
                            i === index ? { ...r, id: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Field label={`Service-tier support ${index + 1}`}>
                    <select
                      value={String(row.supported)}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          mode: ConfigurationMode.Replace,
                          serviceTiers: draft.serviceTiers.map((r, i) =>
                            i === index
                              ? { ...r, supported: e.target.value === "true" }
                              : r,
                          ),
                        })
                      }
                    >
                      <option value="true">{translate("enabled")}</option>
                      <option value="false">{translate("disabled")}</option>
                    </select>
                  </Field>
                  <Button
                    onClick={() =>
                      onChange({
                        ...draft,
                        mode: ConfigurationMode.Replace,
                        serviceTiers: draft.serviceTiers.filter(
                          (_, i) => i !== index,
                        ),
                      })
                    }
                  >
                    {translate("removeServiceTierOverride", {
                      value0: index + 1,
                    })}
                  </Button>
                </fieldset>
              ))}
              <Button
                onClick={() =>
                  onChange({
                    ...draft,
                    mode: ConfigurationMode.Replace,
                    serviceTiers: [
                      ...draft.serviceTiers,
                      { id: "", supported: false },
                    ],
                  })
                }
              >
                {translate("addServiceTierOverride")}
              </Button>
              <fieldset>
                <legend>{translate("responsesItemIdRepair")}</legend>
                <Field
                  label={translate("itemIdRepairPolicy")}
                  hint={translate("inheritUsesTheProviderPresetReplaceWith")}
                >
                  <select
                    value={repair ? "replace" : "inherit"}
                    onChange={(e) =>
                      update({
                        policy: create(ProviderPolicyOverridesSchema, {
                          ...create(
                            ProviderPolicyOverridesSchema,
                            config.policy,
                          ),
                          responsesItemIdRepair:
                            e.target.value === "inherit"
                              ? undefined
                              : create(ResponsesItemIdRepairSchema),
                        }),
                      })
                    }
                  >
                    <option value="inherit">
                      {translate("inheritProviderPreset")}
                    </option>
                    <option value="replace">
                      {translate("replaceProviderPreset")}
                    </option>
                  </select>
                </Field>
                {repair && (
                  <>
                    {(["message", "reasoning"] as const).map((key) => (
                      <Field
                        key={key}
                        label={
                          key === "message"
                            ? translate("messagePlaceholderIds")
                            : translate("reasoningPlaceholderIds")
                        }
                        hint={translate("onePlaceholderIdPerLineEmptySelects")}
                      >
                        <textarea
                          value={repair[key].join("\n")}
                          onChange={(e) =>
                            update({
                              policy: create(ProviderPolicyOverridesSchema, {
                                ...create(
                                  ProviderPolicyOverridesSchema,
                                  config.policy,
                                ),
                                responsesItemIdRepair: create(
                                  ResponsesItemIdRepairSchema,
                                  {
                                    ...repair,
                                    [key]:
                                      e.target.value === ""
                                        ? []
                                        : e.target.value.split("\n"),
                                  },
                                ),
                              }),
                            })
                          }
                        />
                      </Field>
                    ))}
                    {(
                      [
                        ["repairInvalidIds", translate("repairInvalidIds")],
                        [
                          "repairMissingTerminalIds",
                          translate("repairMissingTerminalIds"),
                        ],
                      ] as const
                    ).map(([key, label]) => (
                      <Field key={key} label={label}>
                        <select
                          value={
                            repair[key] === undefined ? "" : String(repair[key])
                          }
                          onChange={(e) =>
                            update({
                              policy: create(ProviderPolicyOverridesSchema, {
                                ...create(
                                  ProviderPolicyOverridesSchema,
                                  config.policy,
                                ),
                                responsesItemIdRepair: create(
                                  ResponsesItemIdRepairSchema,
                                  {
                                    ...repair,
                                    [key]:
                                      e.target.value === ""
                                        ? undefined
                                        : e.target.value === "true",
                                  },
                                ),
                              }),
                            })
                          }
                        >
                          <option value="">{translate("unspecified")}</option>
                          <option value="true">{translate("enabled")}</option>
                          <option value="false">{translate("disabled")}</option>
                        </select>
                      </Field>
                    ))}
                  </>
                )}
              </fieldset>
            </details>
          </>
        )}
        <details className="advanced-disclosure">
          <summary>{translate("customHeaders")}</summary>
          {!!connection?.customHeaderNames.length && (
            <p>
              {translate("savedNamesValuesAreNeverReturned", {
                value0: connection.customHeaderNames.join(", "),
              })}
            </p>
          )}
          <Field label={translate("headerChanges")}>
            <select
              value={draft.headerMode}
              onChange={(e) =>
                onChange({ ...draft, headerMode: e.target.value as HeaderMode })
              }
            >
              <option value={HeaderMode.Preserve}>
                {translate("keepSavedHeaders")}
              </option>
              <option value={HeaderMode.Replace}>
                {translate("replaceAllHeaders")}
              </option>
              <option value={HeaderMode.Clear}>
                {translate("clearAllHeaders")}
              </option>
            </select>
          </Field>
          {draft.headerMode === HeaderMode.Replace && (
            <>
              <p>{translate("enterEveryHeaderToRetainAuthenticationAnd")}</p>
              {draft.headers.map((row, index) => (
                <fieldset key={index}>
                  <legend>{translate("header", { value0: index + 1 })}</legend>
                  <Field label={translate("headerName", { value0: index + 1 })}>
                    <input
                      required
                      autoComplete="off"
                      value={row.name}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          headers: draft.headers.map((r, i) =>
                            i === index ? { ...r, name: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Field
                    label={translate("headerValue", { value0: index + 1 })}
                  >
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={row.value}
                      onChange={(e) =>
                        onChange({
                          ...draft,
                          headers: draft.headers.map((r, i) =>
                            i === index ? { ...r, value: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Button
                    onClick={() =>
                      onChange({
                        ...draft,
                        headers: draft.headers.filter((_, i) => i !== index),
                      })
                    }
                  >
                    {translate("removeHeader", { value0: index + 1 })}
                  </Button>
                </fieldset>
              ))}
              <Button
                disabled={draft.headers.length >= 64}
                onClick={() =>
                  onChange({
                    ...draft,
                    headers: [...draft.headers, { name: "", value: "" }],
                  })
                }
              >
                {translate("addHeader")}
              </Button>
            </>
          )}
        </details>
      </details>
    </>
  );
}
