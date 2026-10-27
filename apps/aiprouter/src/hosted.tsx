// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, enumLabel } from "./i18n";
import { useRef, useState, type ReactNode, type SyntheticEvent } from "react";
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
import type { UseActionResult } from "./actions";
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
/** Collapsible advanced areas whose expansion drives the compact notice. */
enum HostedAdvanced {
  Search = "search",
  Vision = "vision",
  Media = "media",
}
type HostedAction = UseActionResult;
export function HostedHelpers({
  organizationId,
  organizationName,
}: {
  organizationId: string;
  organizationName?: string;
}) {
  useLocale();
  const api = useApi();
  // The action lives here, not in the dialog, so a save submitted before the
  // dialog closes still reports its final outcome in this entry region.
  const action = useAction();
  const [open, setOpen] = useState(false);
  const session = useRef(0);
  function openDialog() {
    action.reset();
    session.current += 1;
    setOpen(true);
  }
  function save(settings: HostedSettings) {
    const submitted = session.current;
    void action.run(
      () =>
        api.hosted.updateHostedSettings({
          organizationId,
          settings,
        }),
      () => {
        // Close only the dialog that submitted this change.
        if (session.current === submitted) setOpen(false);
      },
      translate("hostedHelperSettingsSaved"),
    );
  }
  return (
    <Card>
      <h2>{translate("hostedHelpers")}</h2>
      <p>{translate("chooseYourOwnAccountsForWebSearch")}</p>
      <Button onClick={openDialog}>
        {translate("configureHostedHelpers")}
      </Button>
      {!open && <ErrorNotice error={action.error} />}
      <p className="mutation-status" role="status">
        {open
          ? ""
          : action.pending
            ? translate("message_c5ca36dfc7")
            : action.notice}
      </p>
      {open && (
        <HostedLoader
          organizationId={organizationId}
          organizationName={organizationName}
          action={action}
          onSave={save}
          onClose={() => setOpen(false)}
        />
      )}
    </Card>
  );
}
function HostedIntro({ organizationName }: { organizationName?: string }) {
  useLocale();
  return (
    <div className="hosted-intro">
      <p>
        {organizationName
          ? translate("onlyYoursIn", { value0: organizationName })
          : translate("onlyYoursInThisOrganization")}
      </p>
      <p>{translate("helpersUseTheirOwnSelectedAccountsNot")}</p>
    </div>
  );
}
function HostedLoader({
  organizationId,
  organizationName,
  action,
  onSave,
  onClose,
}: {
  organizationId: string;
  organizationName?: string;
  action: HostedAction;
  onSave: (settings: HostedSettings) => void;
  onClose: () => void;
}) {
  useLocale();
  const query = useQuery(HostedSettingsService.method.getHostedSettings, {
    organizationId,
  });
  const resources = usePersonalResources(organizationId);
  // One dialog for every state keeps the heading focus and size stable while
  // the content moves from loading to the editor.
  let content: ReactNode;
  if (query.isPending) {
    content = <Loading label={translate("loadingHostedHelperSettings")} />;
  } else if (query.isError && !query.data) {
    // A failed background refetch must not discard an open draft, so only the
    // initial load failure replaces the editor.
    content = (
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
    );
  } else {
    const connectionsError = resources.connections.error;
    const poolsError = resources.pools.error;
    content = (
      <HostedEditor
        initial={query.data?.settings}
        connections={resources.connections.data ?? []}
        pools={resources.pools.data ?? []}
        resourcesError={connectionsError ?? poolsError}
        resourcesPending={
          resources.connections.isPending || resources.pools.isPending
        }
        retryResources={() => {
          if (connectionsError) void resources.connections.refetch();
          if (poolsError) void resources.pools.refetch();
        }}
        action={action}
        onSave={onSave}
        onClose={onClose}
      />
    );
  }
  return (
    <Dialog
      title={translate("hostedHelpers")}
      onClose={onClose}
      focusHeading
      className="hosted-dialog"
    >
      <HostedIntro organizationName={organizationName} />
      {content}
    </Dialog>
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
  initial,
  connections,
  pools,
  resourcesError,
  resourcesPending,
  retryResources,
  action,
  onSave,
  onClose,
}: {
  initial?: HostedSettings;
  connections: ProviderConnection[];
  pools: Pool[];
  resourcesError: unknown;
  resourcesPending: boolean;
  retryResources: () => void;
  action: HostedAction;
  onSave: (settings: HostedSettings) => void;
  onClose: () => void;
}) {
  useLocale();
  const [draft, setDraft] = useState(() =>
    create(HostedSettingsSchema, initial),
  );
  const [advancedOpen, setAdvancedOpen] = useState<
    Record<HostedAdvanced, boolean>
  >({
    [HostedAdvanced.Search]: false,
    [HostedAdvanced.Vision]: false,
    [HostedAdvanced.Media]: false,
  });
  const search = draft.search,
    vision = draft.vision,
    images = draft.images;
  const targetProps = { connections, pools };
  // Removed sections unmount their disclosure without a toggle event, so only
  // count areas whose section still exists in the draft.
  const advancedExpanded =
    (!!search && advancedOpen[HostedAdvanced.Search]) ||
    (!!vision && advancedOpen[HostedAdvanced.Vision]) ||
    (!!images && advancedOpen[HostedAdvanced.Media]);
  const trackAdvanced = (area: HostedAdvanced) => ({
    onToggle: (e: SyntheticEvent<HTMLDetailsElement>) => {
      const expanded = e.currentTarget.open;
      setAdvancedOpen((current) =>
        current[area] === expanded ? current : { ...current, [area]: expanded },
      );
    },
  });
  const noResources =
    !resourcesPending &&
    !resourcesError &&
    connections.length === 0 &&
    pools.length === 0;
  return (
    <>
      {advancedExpanded && (
        <div className="notice hosted-compact-notice">
          {translate("saveReplacesAllHelperSettingsRemovingA")}
        </div>
      )}
      {resourcesPending && (
        <p className="hosted-resource-status" role="status">
          {translate("loadingPersonalConnectionsAndPoolsSaveIs")}
        </p>
      )}
      {!!resourcesError && (
        <div className="hosted-resource-error">
          <p>{translate("personalConnectionsAndPoolsCouldNotBe")}</p>
          <ErrorNotice error={resourcesError} retry={retryResources} />
        </div>
      )}
      {noResources && (
        <p className="hosted-resource-status">
          {translate("noPersonalConnectionsOrPoolsAvailable")}
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (action.pending || resourcesPending || resourcesError) return;
          onSave(draft);
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
              <details {...trackAdvanced(HostedAdvanced.Search)}>
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
              <details {...trackAdvanced(HostedAdvanced.Vision)}>
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
              <details {...trackAdvanced(HostedAdvanced.Media)}>
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
        <div className="notice hosted-notice">
          <h3>{translate("saveReplacesAllHelperSettings")}</h3>
          <p>{translate("uncheckingAConfigurationRemovesItsSavedSettings")}</p>
        </div>
        <ErrorNotice error={action.error} />
        {action.pending && (
          <p className="hosted-pending" role="status">
            {translate("message_c5ca36dfc7")}
          </p>
        )}
        <div className="actions hosted-footer">
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
    </>
  );
}
