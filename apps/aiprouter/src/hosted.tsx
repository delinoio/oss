// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, enumLabel } from "./i18n";
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { useQuery } from "@connectrpc/connect-query";
import {
  HostedSettingsService,
  HostedSettingsSchema,
  HostedSearchSettingsSchema,
  HostedVisionSettingsSchema,
  HostedImageSettingsSchema,
  HostedHelperTargetSchema,
  HostedXSearchOptionsSchema,
  HostedSearchBackend,
  HostedVisionBackend,
  ReasoningEffort,
  type HostedSettings,
  type HostedHelperTarget,
  type ProviderConnection,
  type Pool,
} from "@aiprouter/client";
import { useApi } from "./api";
import { useAction } from "./actions";
import { usePersonalResources } from "./resources";
import {
  Button,
  Card,
  Dialog,
  ErrorNotice,
  Field,
  Loading,
  Notice,
} from "./ui";
export function HostedHelpers({ organizationId }: { organizationId: string }) {
  useLocale();
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <h2>{translate("hostedHelpers")}</h2>
      <p>{translate("chooseYourOwnAccountsForWebSearch")}</p>
      <Button onClick={() => setOpen(true)}>
        {translate("configureHostedHelpers")}
      </Button>
      {open && (
        <HostedLoader
          organizationId={organizationId}
          onClose={() => setOpen(false)}
        />
      )}
    </Card>
  );
}
function HostedLoader({
  organizationId,
  onClose,
}: {
  organizationId: string;
  onClose: () => void;
}) {
  useLocale();
  const query = useQuery(HostedSettingsService.method.getHostedSettings, {
    organizationId,
  });
  const resources = usePersonalResources(organizationId);
  if (query.isPending)
    return (
      <Dialog title={translate("hostedHelpers")} onClose={onClose}>
        <Loading />
      </Dialog>
    );
  if (query.error)
    return (
      <Dialog title={translate("hostedHelpers")} onClose={onClose}>
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      </Dialog>
    );
  return (
    <HostedEditor
      organizationId={organizationId}
      initial={query.data?.settings}
      connections={resources.connections.data ?? []}
      pools={resources.pools.data ?? []}
      resourcesError={resources.connections.error ?? resources.pools.error}
      resourcesPending={
        resources.connections.isPending || resources.pools.isPending
      }
      onClose={onClose}
    />
  );
}
function OptionalBoolean({
  label,
  value,
  onChange,
}: {
  label: string;
  value: boolean | undefined;
  onChange: (value: boolean | undefined) => void;
}) {
  useLocale();
  return (
    <Field label={label}>
      <select
        value={value === undefined ? "" : String(value)}
        onChange={(e) =>
          onChange(
            e.target.value === "" ? undefined : e.target.value === "true",
          )
        }
      >
        <option value="">{translate("default")}</option>
        <option value="true">{translate("enabled")}</option>
        <option value="false">{translate("disabled")}</option>
      </select>
    </Field>
  );
}
function OptionalNumber({
  label,
  value,
  onChange,
  max,
  min = 0,
  hint,
}: {
  label: string;
  value: number | undefined;
  onChange: (value: number | undefined) => void;
  max: number;
  min?: number;
  hint?: string;
}) {
  useLocale();
  return (
    <Field label={label} hint={hint}>
      <input
        type="number"
        min={min}
        max={max}
        step="1"
        value={value ?? ""}
        onChange={(e) =>
          onChange(e.target.value === "" ? undefined : Number(e.target.value))
        }
      />
    </Field>
  );
}
function EnumField({
  label,
  value,
  values,
  onChange,
}: {
  label: string;
  value: number | undefined;
  values: Record<string, string | number>;
  onChange: (value: number | undefined) => void;
}) {
  useLocale();
  return (
    <Field label={label}>
      <select
        value={value ?? ""}
        onChange={(e) =>
          onChange(e.target.value === "" ? undefined : Number(e.target.value))
        }
      >
        <option value="">{translate("default")}</option>
        {Object.entries(values)
          .filter(([, v]) => typeof v === "number" && v !== 0)
          .map(([name, v]) => (
            <option key={name} value={v}>
              {enumLabel(name)}
            </option>
          ))}
      </select>
    </Field>
  );
}
function TargetFields({
  label,
  target,
  onChange,
  connections,
  pools,
}: {
  label: string;
  target?: HostedHelperTarget;
  onChange: (target: HostedHelperTarget | undefined) => void;
  connections: ProviderConnection[];
  pools: Pool[];
}) {
  useLocale();
  const current = target?.selection.case
    ? `${target.selection.case}:${target.selection.value}`
    : "";
  const options = [
    ...connections.map((c) => ({
      value: translate("connectionid", { value0: c.id }),
      label: translate("connection", { value0: c.name }),
    })),
    ...pools.map((p) => ({
      value: translate("poolid", { value0: p.id }),
      label: translate("pool", { value0: p.name }),
    })),
  ];
  return (
    <>
      <Field
        label={`${label} account`}
        hint={translate("selectAPersonalConnectionOrPoolThe")}
      >
        <select
          value={current}
          onChange={(e) => {
            if (!e.target.value) {
              onChange(undefined);
              return;
            }
            const [kind, ...id] = e.target.value.split(":");
            onChange(
              create(HostedHelperTargetSchema, {
                modelId: target?.modelId ?? "",
                selection:
                  kind === "connectionId"
                    ? { case: "connectionId", value: id.join(":") }
                    : { case: "poolId", value: id.join(":") },
              }),
            );
          }}
        >
          <option value="">{translate("noAccountSelected")}</option>
          {current && !options.some((o) => o.value === current) && (
            <option value={current}>
              {translate("savedAccountUnavailable")}
            </option>
          )}
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      {target && (
        <Field label={translate("modelId", { value0: label })}>
          <input
            value={target.modelId}
            onChange={(e) =>
              onChange(
                create(HostedHelperTargetSchema, {
                  ...target,
                  modelId: e.target.value,
                }),
              )
            }
          />
        </Field>
      )}
    </>
  );
}
function HostedEditor({
  organizationId,
  initial,
  connections,
  pools,
  resourcesError,
  resourcesPending,
  onClose,
}: {
  organizationId: string;
  initial?: HostedSettings;
  connections: ProviderConnection[];
  pools: Pool[];
  resourcesError: unknown;
  resourcesPending: boolean;
  onClose: () => void;
}) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const [draft, setDraft] = useState(() =>
    create(HostedSettingsSchema, initial),
  );
  const search = draft.search,
    vision = draft.vision,
    images = draft.images;
  const targetProps = { connections, pools };
  return (
    <Dialog title={translate("hostedHelpers")} onClose={onClose}>
      <p>{translate("privateToYouInThisOrganizationHelpers")}</p>
      <ErrorNotice error={resourcesError} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void action.run(
            () =>
              api.hosted.updateHostedSettings({
                organizationId,
                settings: draft,
              }),
            onClose,
            translate("hostedHelperSettingsSaved"),
          );
        }}
      >
        <fieldset>
          <legend>{translate("webSearch")}</legend>
          <label className="check">
            <input
              type="checkbox"
              checked={!!search}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  search: e.target.checked
                    ? create(HostedSearchSettingsSchema)
                    : undefined,
                })
              }
            />
            {translate("configureWebSearch")}
          </label>
          {search && (
            <>
              <OptionalBoolean
                label={translate("webSearchAvailability")}
                value={search.enabled}
                onChange={(enabled) =>
                  setDraft({ ...draft, search: { ...search, enabled } })
                }
              />
              <EnumField
                label={translate("searchBackend")}
                value={search.backend}
                values={HostedSearchBackend}
                onChange={(backend) =>
                  setDraft({ ...draft, search: { ...search, backend } })
                }
              />
              <TargetFields
                label={translate("search")}
                {...targetProps}
                target={search.target}
                onChange={(target) =>
                  setDraft({ ...draft, search: { ...search, target } })
                }
              />
              <details>
                <summary>{translate("advancedSearchSettings")}</summary>
                <EnumField
                  label={translate("searchReasoningEffort")}
                  value={search.reasoning}
                  values={ReasoningEffort}
                  onChange={(reasoning) =>
                    setDraft({ ...draft, search: { ...search, reasoning } })
                  }
                />
                <OptionalNumber
                  label={translate("maximumSearchesPerTurn")}
                  value={search.maxSearchesPerTurn}
                  max={32}
                  hint={translate("default3ZeroDisablesSearches")}
                  onChange={(maxSearchesPerTurn) =>
                    setDraft({
                      ...draft,
                      search: { ...search, maxSearchesPerTurn },
                    })
                  }
                />
                <OptionalNumber
                  label={translate("searchTimeoutMs")}
                  value={search.timeoutMs}
                  min={1}
                  max={600000}
                  hint={translate("default60000")}
                  onChange={(timeoutMs) =>
                    setDraft({ ...draft, search: { ...search, timeoutMs } })
                  }
                />
                {(search.backend === HostedSearchBackend.XAI ||
                  search.xSearch) && (
                  <>
                    <label className="check">
                      <input
                        type="checkbox"
                        checked={!!search.xSearch}
                        onChange={(e) =>
                          setDraft({
                            ...draft,
                            search: {
                              ...search,
                              xSearch: e.target.checked
                                ? create(HostedXSearchOptionsSchema)
                                : undefined,
                            },
                          })
                        }
                      />
                      {translate("configureXSearch")}
                    </label>
                    {search.xSearch && (
                      <>
                        <label className="check">
                          <input
                            type="checkbox"
                            checked={search.xSearch.enabled}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                search: {
                                  ...search,
                                  xSearch: {
                                    ...search.xSearch!,
                                    enabled: e.target.checked,
                                  },
                                },
                              })
                            }
                          />
                          {translate("enableXSearch")}
                        </label>
                        <Field
                          label={translate("allowedXHandles")}
                          hint={translate("onePerLineUpTo20Use")}
                        >
                          <textarea
                            value={search.xSearch.allowedXHandles.join("\n")}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                search: {
                                  ...search,
                                  xSearch: {
                                    ...search.xSearch!,
                                    allowedXHandles: e.target.value
                                      ? e.target.value.split("\n")
                                      : [],
                                  },
                                },
                              })
                            }
                          />
                        </Field>
                        <Field label={translate("excludedXHandles")}>
                          <textarea
                            value={search.xSearch.excludedXHandles.join("\n")}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                search: {
                                  ...search,
                                  xSearch: {
                                    ...search.xSearch!,
                                    excludedXHandles: e.target.value
                                      ? e.target.value.split("\n")
                                      : [],
                                  },
                                },
                              })
                            }
                          />
                        </Field>
                        <Field label={translate("xSearchFromDate")}>
                          <input
                            type="date"
                            value={search.xSearch.fromDate ?? ""}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                search: {
                                  ...search,
                                  xSearch: {
                                    ...search.xSearch!,
                                    fromDate: e.target.value || undefined,
                                  },
                                },
                              })
                            }
                          />
                        </Field>
                        <Field label={translate("xSearchToDate")}>
                          <input
                            type="date"
                            value={search.xSearch.toDate ?? ""}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                search: {
                                  ...search,
                                  xSearch: {
                                    ...search.xSearch!,
                                    toDate: e.target.value || undefined,
                                  },
                                },
                              })
                            }
                          />
                        </Field>
                      </>
                    )}
                  </>
                )}
              </details>
            </>
          )}
        </fieldset>
        <fieldset>
          <legend>{translate("imageUnderstanding")}</legend>
          <label className="check">
            <input
              type="checkbox"
              checked={!!vision}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  vision: e.target.checked
                    ? create(HostedVisionSettingsSchema)
                    : undefined,
                })
              }
            />
            {translate("configureImageUnderstanding")}
          </label>
          {vision && (
            <>
              <OptionalBoolean
                label={translate("imageUnderstandingAvailability")}
                value={vision.enabled}
                onChange={(enabled) =>
                  setDraft({ ...draft, vision: { ...vision, enabled } })
                }
              />
              <EnumField
                label={translate("visionBackend")}
                value={vision.backend}
                values={HostedVisionBackend}
                onChange={(backend) =>
                  setDraft({
                    ...draft,
                    vision: {
                      ...vision,
                      backend,
                      selection: { case: undefined },
                    },
                  })
                }
              />
              {vision.backend === HostedVisionBackend.ROUTED ? (
                <Field
                  label={translate("visionRouteModel")}
                  hint={translate("useAnExistingPersonalRoutingModelId")}
                >
                  <input
                    required
                    value={
                      vision.selection.case === "routeModel"
                        ? vision.selection.value
                        : ""
                    }
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        vision: {
                          ...vision,
                          selection: {
                            case: "routeModel",
                            value: e.target.value,
                          },
                        },
                      })
                    }
                  />
                </Field>
              ) : (
                <TargetFields
                  label={translate("vision")}
                  {...targetProps}
                  target={
                    vision.selection.case === "target"
                      ? vision.selection.value
                      : undefined
                  }
                  onChange={(target) =>
                    setDraft({
                      ...draft,
                      vision: {
                        ...vision,
                        selection: target
                          ? { case: "target", value: target }
                          : { case: undefined },
                      },
                    })
                  }
                />
              )}
              <details>
                <summary>{translate("advancedVisionSettings")}</summary>
                <OptionalNumber
                  label={translate("maximumDescriptionsPerTurn")}
                  value={vision.maxDescriptionsPerTurn}
                  max={16}
                  hint={translate("default8ZeroDisablesDescriptions")}
                  onChange={(maxDescriptionsPerTurn) =>
                    setDraft({
                      ...draft,
                      vision: { ...vision, maxDescriptionsPerTurn },
                    })
                  }
                />
                <OptionalNumber
                  label={translate("visionTimeoutMs")}
                  value={vision.timeoutMs}
                  min={1}
                  max={2147483647}
                  hint={translate("default45000")}
                  onChange={(timeoutMs) =>
                    setDraft({ ...draft, vision: { ...vision, timeoutMs } })
                  }
                />
              </details>
            </>
          )}
        </fieldset>
        <fieldset>
          <legend>{translate("imageAndVideoGeneration")}</legend>
          <label className="check">
            <input
              type="checkbox"
              checked={!!images}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  images: e.target.checked
                    ? create(HostedImageSettingsSchema)
                    : undefined,
                })
              }
            />
            {translate("configureImageAndVideoGeneration")}
          </label>
          {images && (
            <>
              <p>{translate("requiresAnXaiApiKeyConnectionOr")}</p>
              <TargetFields
                label={translate("media")}
                {...targetProps}
                target={images.target}
                onChange={(target) =>
                  setDraft({ ...draft, images: { ...images, target } })
                }
              />
              <label className="check">
                <input
                  type="checkbox"
                  checked={images.bridgeEnabled}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      images: { ...images, bridgeEnabled: e.target.checked },
                    })
                  }
                />
                {translate("enableImageGeneration")}
              </label>
              <label className="check">
                <input
                  type="checkbox"
                  checked={images.videoBridgeEnabled}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      images: {
                        ...images,
                        videoBridgeEnabled: e.target.checked,
                      },
                    })
                  }
                />
                {translate("enableVideoGeneration")}
              </label>
              <details>
                <summary>{translate("advancedMediaSettings")}</summary>
                <Field
                  label={translate("imageModelOverride")}
                  hint={translate("defaultGrokImagineImageQuality")}
                >
                  <input
                    maxLength={512}
                    value={images.bridgeModel ?? ""}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        images: {
                          ...images,
                          bridgeModel: e.target.value || undefined,
                        },
                      })
                    }
                  />
                </Field>
                <OptionalNumber
                  label={translate("imageTimeoutMs")}
                  value={images.timeoutMs}
                  min={1}
                  max={300000}
                  onChange={(timeoutMs) =>
                    setDraft({ ...draft, images: { ...images, timeoutMs } })
                  }
                />
                <Field
                  label={translate("videoModelOverride")}
                  hint={translate("defaultGrokImagineVideo")}
                >
                  <input
                    maxLength={512}
                    value={images.videoBridgeModel ?? ""}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        images: {
                          ...images,
                          videoBridgeModel: e.target.value || undefined,
                        },
                      })
                    }
                  />
                </Field>
                <OptionalNumber
                  label={translate("videoTimeoutMs")}
                  value={images.videoTimeoutMs}
                  min={1}
                  max={300000}
                  hint={translate("default300000IncludingPolling")}
                  onChange={(videoTimeoutMs) =>
                    setDraft({
                      ...draft,
                      images: { ...images, videoTimeoutMs },
                    })
                  }
                />
              </details>
            </>
          )}
        </fieldset>
        <Notice>
          {translate("saveReplacesAllHostedHelperSettingsUnchecking")}
        </Notice>
        <ErrorNotice error={action.error} />
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={action.pending || resourcesPending || !!resourcesError}
          >
            {action.pending
              ? translate("message_23e39291d6")
              : translate("saveHelperSettings")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
