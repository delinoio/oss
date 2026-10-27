// SPDX-License-Identifier: Apache-2.0
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
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <h2>Hosted helpers</h2>
      <p>
        Choose your own accounts for web search, image understanding, and image
        or video generation.
      </p>
      <Button onClick={() => setOpen(true)}>Configure hosted helpers</Button>
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
  const query = useQuery(HostedSettingsService.method.getHostedSettings, {
    organizationId,
  });
  const resources = usePersonalResources(organizationId);
  if (query.isPending)
    return (
      <Dialog title="Hosted helpers" onClose={onClose}>
        <Loading />
      </Dialog>
    );
  if (query.error)
    return (
      <Dialog title="Hosted helpers" onClose={onClose}>
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
        <option value="">Default</option>
        <option value="true">Enabled</option>
        <option value="false">Disabled</option>
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
  return (
    <Field label={label}>
      <select
        value={value ?? ""}
        onChange={(e) =>
          onChange(e.target.value === "" ? undefined : Number(e.target.value))
        }
      >
        <option value="">Default</option>
        {Object.entries(values)
          .filter(([, v]) => typeof v === "number" && v !== 0)
          .map(([name, v]) => (
            <option key={name} value={v}>
              {name.replaceAll("_", " ")}
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
  const current = target?.selection.case
    ? `${target.selection.case}:${target.selection.value}`
    : "";
  const options = [
    ...connections.map((c) => ({
      value: `connectionId:${c.id}`,
      label: `Connection: ${c.name}`,
    })),
    ...pools.map((p) => ({
      value: `poolId:${p.id}`,
      label: `Pool: ${p.name}`,
    })),
  ];
  return (
    <>
      <Field
        label={`${label} account`}
        hint="Select a personal connection or pool. The server checks readiness and backend compatibility."
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
          <option value="">No account selected</option>
          {current && !options.some((o) => o.value === current) && (
            <option value={current}>Saved account unavailable</option>
          )}
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      {target && (
        <Field label={`${label} model ID`}>
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
    <Dialog title="Hosted helpers" onClose={onClose}>
      <p>
        Private to you in this organization. Helpers use their own selected
        accounts, not the account serving the main request.
      </p>
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
            "Hosted helper settings saved.",
          );
        }}
      >
        <fieldset>
          <legend>Web search</legend>
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
            />{" "}
            Configure web search
          </label>
          {search && (
            <>
              <OptionalBoolean
                label="Web search availability"
                value={search.enabled}
                onChange={(enabled) =>
                  setDraft({ ...draft, search: { ...search, enabled } })
                }
              />
              <EnumField
                label="Search backend"
                value={search.backend}
                values={HostedSearchBackend}
                onChange={(backend) =>
                  setDraft({ ...draft, search: { ...search, backend } })
                }
              />
              <TargetFields
                label="Search"
                {...targetProps}
                target={search.target}
                onChange={(target) =>
                  setDraft({ ...draft, search: { ...search, target } })
                }
              />
              <details>
                <summary>Advanced search settings</summary>
                <EnumField
                  label="Search reasoning effort"
                  value={search.reasoning}
                  values={ReasoningEffort}
                  onChange={(reasoning) =>
                    setDraft({ ...draft, search: { ...search, reasoning } })
                  }
                />
                <OptionalNumber
                  label="Maximum searches per turn"
                  value={search.maxSearchesPerTurn}
                  max={32}
                  hint="Default: 3. Zero disables searches."
                  onChange={(maxSearchesPerTurn) =>
                    setDraft({
                      ...draft,
                      search: { ...search, maxSearchesPerTurn },
                    })
                  }
                />
                <OptionalNumber
                  label="Search timeout (ms)"
                  value={search.timeoutMs}
                  min={1}
                  max={600000}
                  hint="Default: 60000."
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
                      />{" "}
                      Configure X search
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
                          />{" "}
                          Enable X search
                        </label>
                        <Field
                          label="Allowed X handles"
                          hint="One per line, up to 20. Use either allowed or excluded handles."
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
                        <Field label="Excluded X handles">
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
                        <Field label="X search from date">
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
                        <Field label="X search to date">
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
          <legend>Image understanding</legend>
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
            />{" "}
            Configure image understanding
          </label>
          {vision && (
            <>
              <OptionalBoolean
                label="Image understanding availability"
                value={vision.enabled}
                onChange={(enabled) =>
                  setDraft({ ...draft, vision: { ...vision, enabled } })
                }
              />
              <EnumField
                label="Vision backend"
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
                  label="Vision route model"
                  hint="Use an existing personal routing model ID."
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
                  label="Vision"
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
                <summary>Advanced vision settings</summary>
                <OptionalNumber
                  label="Maximum descriptions per turn"
                  value={vision.maxDescriptionsPerTurn}
                  max={16}
                  hint="Default: 8. Zero disables descriptions."
                  onChange={(maxDescriptionsPerTurn) =>
                    setDraft({
                      ...draft,
                      vision: { ...vision, maxDescriptionsPerTurn },
                    })
                  }
                />
                <OptionalNumber
                  label="Vision timeout (ms)"
                  value={vision.timeoutMs}
                  min={1}
                  max={2147483647}
                  hint="Default: 45000."
                  onChange={(timeoutMs) =>
                    setDraft({ ...draft, vision: { ...vision, timeoutMs } })
                  }
                />
              </details>
            </>
          )}
        </fieldset>
        <fieldset>
          <legend>Image and video generation</legend>
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
            />{" "}
            Configure image and video generation
          </label>
          {images && (
            <>
              <p>Requires an XAI API-key connection or compatible pool.</p>
              <TargetFields
                label="Media"
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
                />{" "}
                Enable image generation
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
                />{" "}
                Enable video generation
              </label>
              <details>
                <summary>Advanced media settings</summary>
                <Field
                  label="Image model override"
                  hint="Default: grok-imagine-image-quality."
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
                  label="Image timeout (ms)"
                  value={images.timeoutMs}
                  min={1}
                  max={300000}
                  onChange={(timeoutMs) =>
                    setDraft({ ...draft, images: { ...images, timeoutMs } })
                  }
                />
                <Field
                  label="Video model override"
                  hint="Default: grok-imagine-video."
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
                  label="Video timeout (ms)"
                  value={images.videoTimeoutMs}
                  min={1}
                  max={300000}
                  hint="Default: 300000, including polling."
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
          Save replaces all hosted helper settings. Unchecking a configuration
          removes its saved settings; it does not always disable the provider
          default. Select Disabled to explicitly disable search or image
          understanding.
        </Notice>
        <ErrorNotice error={action.error} />
        <div className="actions">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={action.pending || resourcesPending || !!resourcesError}
          >
            {action.pending ? "Saving…" : "Save helper settings"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
