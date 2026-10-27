// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  AllowedDestinationSchema,
  HealthState,
  InstanceSettingsSchema,
  OperatorService,
  type InstanceSettings,
  type PriceOverride,
} from "@aiprouter/client";
import { Card, PageHeader } from "../ui";
import { useDirtyForm } from "../api";
import {
  Confirm,
  Dialog,
  Empty,
  ErrorNotice,
  Loading,
  Pager,
  date,
  label,
  useAction,
} from "./common";
import { Summary, timeRange } from "./reporting";
import { usePriceRevision, type PriceSnapshot } from "./price-revision";

export function Health() {
  const health = useQuery(
    OperatorService.method.getInstanceHealth,
    {},
    { refetchInterval: 30000 },
  );
  const [dates, setDates] = useState(() => ({
    start: new Date(Date.now() - 7 * 86400000).toISOString().slice(0, 10),
    end: new Date().toISOString().slice(0, 10),
  }));
  const valid = !!dates.start && !!dates.end && dates.start <= dates.end;
  const usage = useQuery(
    OperatorService.method.getInstanceUsage,
    { timeRange: valid ? timeRange(dates.start, dates.end) : undefined },
    { enabled: valid },
  );
  return (
    <>
      <PageHeader
        title="Instance health"
        description="Whole-instance availability and request aggregates."
      />
      {health.isPending ? (
        <Loading />
      ) : health.error ? (
        <ErrorNotice error={health.error} retry={() => void health.refetch()} />
      ) : (
        <Card>
          <h2>Availability</h2>
          <div className="admin-metrics">
            <div>
              <span>Service</span>
              <strong>
                {health.data?.health?.service
                  ? label(HealthState[health.data.health.service])
                  : "Unknown"}
              </strong>
            </div>
            <div>
              <span>Database</span>
              <strong>
                {health.data?.health?.database
                  ? label(HealthState[health.data.health.database])
                  : "Unknown"}
              </strong>
            </div>
          </div>
          <p>
            Last successful cleanup:{" "}
            {date(health.data?.health?.lastCleanupSuccessAt)}
          </p>
          <p>
            Consecutive cleanup failures:{" "}
            {health.data?.health?.lastCleanupSuccessAt
              ? health.data.health.consecutiveCleanupFailures
              : "Unknown"}
          </p>
          <p>
            Usage recording health:{" "}
            {health.data?.health?.usageRecording
              ? label(HealthState[health.data.health.usageRecording])
              : "Unknown"}
            . Recording failures can leave request totals incomplete.
          </p>
          <button onClick={() => void health.refetch()}>Refresh health</button>
        </Card>
      )}
      <Card>
        <h2>Aggregate period</h2>
        <div className="admin-form-grid">
          <label>
            From (UTC)
            <input
              type="date"
              value={dates.start}
              onChange={(e) => setDates({ ...dates, start: e.target.value })}
            />
          </label>
          <label>
            Through (UTC)
            <input
              type="date"
              value={dates.end}
              onChange={(e) => setDates({ ...dates, end: e.target.value })}
            />
          </label>
        </div>
        {!valid && <p role="alert">Choose a valid start and end date.</p>}
      </Card>
      {valid &&
        (usage.isPending ? (
          <Loading />
        ) : usage.error ? (
          <ErrorNotice error={usage.error} retry={() => void usage.refetch()} />
        ) : (
          <Summary summary={usage.data?.summary} />
        ))}
    </>
  );
}
export function InstanceConfiguration({
  section,
}: {
  section: "limits" | "retention" | "destinations";
}) {
  const query = useQuery(OperatorService.method.getInstanceSettings, {});
  return (
    <>
      <PageHeader
        title={
          {
            limits: "Request limits",
            retention: "Retention",
            destinations: "Approved destinations",
          }[section]
        }
        description={
          section === "limits"
            ? "Applied per user within each organization."
            : "Manage instance policy."
        }
      />
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : query.data?.settings ? (
        <ConfigurationForm
          key={section}
          settings={query.data.settings}
          section={section}
        />
      ) : (
        <Empty>Instance settings are unavailable.</Empty>
      )}
    </>
  );
}
function ConfigurationForm({
  settings: initialSettings,
  section,
}: {
  settings: InstanceSettings;
  section: "limits" | "retention" | "destinations";
}) {
  // Keep the reviewed snapshot across background refetches. A new revision
  // must not silently replace safe edits or authorize overwriting newer policy.
  const [settings, setSettings] = useState(initialSettings);
  const [draft, setDraft] = useState(() =>
    create(InstanceSettingsSchema, initialSettings),
  );
  const [review, setReview] = useState(false);
  const [origin, setOrigin] = useState("");
  const [networks, setNetworks] = useState("");
  const [editingDestination, setEditingDestination] = useState<number>();
  const destinationInputDirty =
    !!origin || !!networks || editingDestination !== undefined;
  const dirty = useDirtyForm();
  const [changed, setChanged] = useState(false);
  useEffect(() => {
    dirty(changed || destinationInputDirty);
    return () => dirty(false);
  }, [dirty, changed, destinationInputDirty]);
  const action = useAction(true);
  const update = useMutation(OperatorService.method.updateInstanceSettings);
  function save() {
    void action.run(
      async () => {
        const response = await update.mutateAsync({
          settings: draft,
          expectedRevision: settings.revision,
        });
        if (response.settings) {
          setSettings(response.settings);
          setDraft(create(InstanceSettingsSchema, response.settings));
        }
      },
      {
        done: () => {
          setReview(false);
          setChanged(false);
        },
      },
    );
  }
  const reduced =
    !!draft.retention &&
    !!settings.retention &&
    (draft.retention.requestDays < settings.retention.requestDays ||
      draft.retention.aggregateDays < settings.retention.aggregateDays ||
      draft.retention.auditDays < settings.retention.auditDays);
  function submit() {
    if (reduced || section === "destinations") setReview(true);
    else save();
  }
  return (
    <Card>
      {action.feedback}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {section === "limits" && draft.requestLimits && (
          <>
            <label>
              Requests per minute
              <input
                type="number"
                required
                min={1}
                max={6000}
                step={1}
                value={draft.requestLimits.requestsPerMinute}
                onChange={(e) => {
                  setDraft({
                    ...draft,
                    requestLimits: {
                      ...draft.requestLimits!,
                      requestsPerMinute: Number(e.target.value),
                    },
                  });
                  setChanged(true);
                }}
              />
            </label>
            <p>1–6,000 requests per minute. Default: 120.</p>
            <label>
              Concurrent requests
              <input
                type="number"
                required
                min={1}
                max={100}
                step={1}
                value={draft.requestLimits.concurrentRequests}
                onChange={(e) => {
                  setDraft({
                    ...draft,
                    requestLimits: {
                      ...draft.requestLimits!,
                      concurrentRequests: Number(e.target.value),
                    },
                  });
                  setChanged(true);
                }}
              />
            </label>
            <p>
              1–100 concurrent requests. Default: 10. These limits apply per
              user within each organization.
            </p>
          </>
        )}
        {section === "retention" && draft.retention && (
          <>
            {(
              [
                {
                  field: "requestDays",
                  name: "Request metadata",
                  min: 1,
                  max: 90,
                  default: 30,
                },
                {
                  field: "aggregateDays",
                  name: "Daily aggregates",
                  min: 30,
                  max: 730,
                  default: 365,
                },
                {
                  field: "auditDays",
                  name: "Audit records",
                  min: 30,
                  max: 365,
                  default: 90,
                },
              ] as const
            ).map((item) => (
              <label key={item.field}>
                {item.name} (days)
                <input
                  type="number"
                  required
                  min={item.min}
                  max={item.max}
                  step={1}
                  value={draft.retention![item.field]}
                  onChange={(e) => {
                    setDraft({
                      ...draft,
                      retention: {
                        ...draft.retention!,
                        [item.field]: Number(e.target.value),
                      },
                    });
                    setChanged(true);
                  }}
                />
                <small>
                  {item.min}–{item.max} days. Default: {item.default}.
                </small>
              </label>
            ))}
          </>
        )}
        {section === "destinations" && (
          <>
            <p>
              Public HTTPS destinations are allowed by default. Approve precise
              origins to permit private-network or local HTTP destinations.
              Leave networks empty for exact-origin approval. A nonempty network
              list restricts every resolved address for that origin to those
              networks, even for public HTTPS. Redirect and DNS checks still
              apply.
            </p>
            <div className="admin-records">
              {draft.allowedDestinations.map((destination, index) => (
                <article
                  className="admin-record"
                  key={`${destination.origin}:${index}`}
                >
                  <div>
                    <h3>{destination.origin}</h3>
                    <p>
                      {destination.allowedCidrs.length > 0
                        ? `Restricted networks: ${destination.allowedCidrs.join(", ")}`
                        : "Exact-origin approval; no CIDR narrowing."}
                    </p>
                  </div>
                  <div className="admin-actions">
                    <button
                      type="button"
                      disabled={destinationInputDirty}
                      onClick={() => {
                        setEditingDestination(index);
                        setOrigin(destination.origin);
                        setNetworks(destination.allowedCidrs.join("\n"));
                      }}
                    >
                      Edit approval
                    </button>
                    <button
                      type="button"
                      disabled={destinationInputDirty}
                      onClick={() => {
                        setDraft({
                          ...draft,
                          allowedDestinations: draft.allowedDestinations.filter(
                            (_, i) => i !== index,
                          ),
                        });
                        setChanged(true);
                      }}
                    >
                      Remove approval
                    </button>
                  </div>
                </article>
              ))}
            </div>
            {!draft.allowedDestinations.length && (
              <Empty>No additional destinations approved.</Empty>
            )}
            <label>
              Exact origin
              <input
                type="url"
                value={origin}
                placeholder="http://localhost:8080"
                onChange={(e) => setOrigin(e.target.value)}
              />
            </label>
            <label>
              Allowed networks (optional)
              <textarea
                rows={3}
                value={networks}
                placeholder="10.0.0.0/8&#10;2001:db8::/32"
                onChange={(e) => setNetworks(e.target.value)}
                aria-describedby="destination-networks-help"
              />
            </label>
            <p id="destination-networks-help">
              Enter CIDR ranges, one per line or separated by commas. Clearing
              this field removes network narrowing for this origin, including
              public HTTPS.
            </p>
            <div className="admin-actions">
              <button
                type="button"
                disabled={
                  !validOrigin(origin) ||
                  draft.allowedDestinations.some(
                    (d, index) =>
                      index !== editingDestination && d.origin === origin,
                  )
                }
                onClick={() => {
                  if (!validOrigin(origin)) return;
                  // Preserve the stable rule ID and any future fields when editing.
                  // Prefix canonicalization, duplicates and mapped addresses are validated by the server.
                  const destination = {
                    ...create(
                      AllowedDestinationSchema,
                      editingDestination === undefined
                        ? undefined
                        : draft.allowedDestinations[editingDestination],
                    ),
                    origin,
                    allowedCidrs: networks.split(/[\s,]+/u).filter(Boolean),
                  };
                  setDraft({
                    ...draft,
                    allowedDestinations:
                      editingDestination === undefined
                        ? [...draft.allowedDestinations, destination]
                        : draft.allowedDestinations.map((d, index) =>
                            index === editingDestination ? destination : d,
                          ),
                  });
                  setOrigin("");
                  setNetworks("");
                  setEditingDestination(undefined);
                  setChanged(true);
                }}
              >
                {editingDestination === undefined
                  ? "Add to review"
                  : "Update review"}
              </button>
              {destinationInputDirty && (
                <button
                  type="button"
                  onClick={() => {
                    setOrigin("");
                    setNetworks("");
                    setEditingDestination(undefined);
                  }}
                >
                  Cancel destination edit
                </button>
              )}
            </div>
          </>
        )}
        <div className="admin-actions">
          <button
            className="admin-primary"
            disabled={
              action.pending ||
              !changed ||
              destinationInputDirty ||
              !draft.requestLimits ||
              !draft.retention
            }
          >
            Review and save changes
          </button>
        </div>
      </form>
      {review && (
        <Confirm
          title={
            section === "destinations"
              ? "Save destination policy"
              : "Reduce retention"
          }
          pending={action.pending}
          onClose={() => setReview(false)}
          action={save}
        >
          {section === "destinations" ? (
            <>
              <p>Only these additional destinations will remain approved:</p>
              <ul>
                {draft.allowedDestinations.map((d, i) => (
                  <li key={d.id || i}>
                    <strong>{d.origin}</strong> —{" "}
                    {d.allowedCidrs.length
                      ? `Restricted to ${d.allowedCidrs.join(", ")}. Addresses outside these networks are blocked, including public HTTPS.`
                      : "Exact-origin approval with no CIDR narrowing. Clearing previous networks broadens access for this origin."}
                  </li>
                ))}
              </ul>
              <p>
                Removing approval blocks future connections that require it.
                Adding approval permits connections to the specified origin.
              </p>
            </>
          ) : (
            <p>
              Records older than the new limits become unavailable and are
              eligible for deletion. Request metadata:{" "}
              {draft.retention?.requestDays} days. Daily aggregates:{" "}
              {draft.retention?.aggregateDays} days. Audit records:{" "}
              {draft.retention?.auditDays} days. Existing backups follow
              operator policy.
            </p>
          )}
          <ErrorNotice error={action.error} />
          {!!action.error &&
            section === "destinations" &&
            ConnectError.from(action.error).code === Code.InvalidArgument && (
              <p>
                Check the exact origins and CIDR prefixes. Duplicate canonical
                origins and invalid network prefixes are not accepted.
              </p>
            )}
          {!!action.error &&
            ConnectError.from(action.error).code === Code.Aborted && (
              <p>
                Your draft and network entries are preserved. The saved policy
                changed. Reload the current settings and review your changes
                before submitting again.
              </p>
            )}
        </Confirm>
      )}
    </Card>
  );
}
export function validOrigin(value: string) {
  try {
    const u = new URL(value);
    return (
      ["http:", "https:"].includes(u.protocol) &&
      !u.username &&
      !u.password &&
      u.origin === value
    );
  } catch {
    return false;
  }
}
const rateFields = [
  ["input", "Input"],
  ["output", "Output"],
  ["cacheRead", "Cache read"],
  ["cacheWrite", "Cache write"],
  ["cacheWriteShort", "Short cache write"],
  ["cacheWriteLong", "Long cache write"],
] as const;
export function Prices() {
  const [token, setToken] = useState("");
  const [editing, setEditing] = useState<PriceSnapshot>();
  const [deleting, setDeleting] = useState<PriceSnapshot>();
  const action = useAction(true);
  const query = useQuery(OperatorService.method.listPriceOverrides, {
    page: { pageSize: 100, pageToken: token },
  });

  return (
    <>
      <PageHeader
        title="Price overrides"
        description="Common prices in currency units per million tokens. Applies to new requests only."
        action={
          <button
            className="admin-primary"
            disabled={
              !query.data?.revision ||
              !!query.error ||
              query.isFetching ||
              action.pending
            }
            onClick={() => {
              action.clearError();
              setEditing({ revision: query.data!.revision });
            }}
          >
            Add price override
          </button>
        }
      />
      {action.feedback}
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <ErrorNotice
          error={query.error}
          retry={() => {
            if (token) setToken("");
            else void query.refetch();
          }}
        />
      ) : (
        <Card>
          {!query.data?.overrides.length && (
            <Empty>
              No price overrides. Bundled prices apply where available.
            </Empty>
          )}
          <div className="admin-records">
            {query.data?.overrides.map((price) => (
              <article key={price.id} className="admin-record">
                <div>
                  <h2>
                    {price.providerId} / {price.modelId}
                  </h2>
                  <p>
                    Tier: {price.serviceTier || "Default"} · Provenance:
                    Instance operator override
                  </p>
                  <p>Updated {date(price.updatedAt)}</p>
                  {rateFields.map(([field, name]) => (
                    <p key={field}>
                      {name}:{" "}
                      {price.rates?.[field] === undefined
                        ? "Unknown"
                        : `${price.rates.currency} ${price.rates[field]} per million tokens`}
                    </p>
                  ))}
                </div>
                <div className="admin-actions">
                  <button
                    disabled={!query.data?.revision || action.pending}
                    onClick={() => {
                      action.clearError();
                      setEditing({ price, revision: query.data!.revision });
                    }}
                  >
                    Edit override
                  </button>
                  <button
                    disabled={!query.data?.revision || action.pending}
                    onClick={() => {
                      action.clearError();
                      setDeleting({ price, revision: query.data!.revision });
                    }}
                  >
                    Delete override
                  </button>
                </div>
              </article>
            ))}
          </div>
          <Pager
            token={token}
            next={query.data?.page?.nextPageToken}
            onChange={setToken}
          />
        </Card>
      )}
      {editing && (
        <PriceEditor
          price={editing.price}
          revision={editing.revision}
          action={action}
          onClose={() => setEditing(undefined)}
        />
      )}
      {deleting?.price && (
        <PriceDeletion
          price={deleting.price}
          revision={deleting.revision}
          action={action}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </>
  );
}
function PriceEditor({
  price,
  revision,
  action,
  onClose,
}: {
  price?: PriceOverride;
  revision: string;
  action: ReturnType<typeof useAction>;
  onClose: () => void;
}) {
  const [provider, setProvider] = useState(price?.providerId ?? "");
  const [model, setModel] = useState(price?.modelId ?? "");
  const [tier, setTier] = useState(price?.serviceTier ?? "");
  const [currency, setCurrency] = useState(price?.rates?.currency ?? "USD");
  const [rates, setRates] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      rateFields.map(([field]) => [field, price?.rates?.[field] ?? ""]),
    ),
  );
  const snapshot = usePriceRevision(
    revision,
    { providerId: provider, modelId: model, serviceTier: tier },
    action,
  );
  const save = useMutation(OperatorService.method.setPriceOverride);
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(true);
    return () => dirty(false);
  }, [dirty]);
  return (
    <Dialog
      title={price ? "Edit price override" : "Add price override"}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (snapshot.blocked) return;
          void action.run(
            () =>
              save.mutateAsync({
                expectedRevision: snapshot.revision,
                providerId: provider,
                modelId: model,
                serviceTier: tier,
                rates: {
                  currency,
                  ...Object.fromEntries(
                    Object.entries(rates).map(([key, value]) => [
                      key,
                      value === "" ? undefined : value,
                    ]),
                  ),
                },
              }),
            { done: onClose },
          );
        }}
      >
        <label>
          Provider ID
          <input
            required
            readOnly={!!price}
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
          />
        </label>
        <label>
          Model ID
          <input
            required
            readOnly={!!price}
            value={model}
            onChange={(e) => setModel(e.target.value)}
          />
        </label>
        <label>
          Service tier
          <input
            readOnly={!!price}
            value={tier}
            onChange={(e) => setTier(e.target.value)}
          />
        </label>
        <label>
          Currency
          <input
            required
            pattern="[A-Z]{3}"
            maxLength={3}
            value={currency}
            onChange={(e) => setCurrency(e.target.value.toUpperCase())}
          />
        </label>
        <p>
          Rates per million tokens. Blank means unknown. Enter 0 only for a
          confirmed zero price. Historical estimates remain unchanged.
        </p>
        <div className="admin-form-grid">
          {rateFields.map(([field, name]) => (
            <label key={field}>
              {name}
              <input
                inputMode="decimal"
                pattern="[0-9]+([.][0-9]+)?"
                value={rates[field]}
                onChange={(e) =>
                  setRates({ ...rates, [field]: e.target.value })
                }
              />
            </label>
          ))}
        </div>
        <ErrorNotice error={action.error} />
        {snapshot.controls}
        <div className="admin-actions">
          <button type="button" data-cancel onClick={onClose}>
            Cancel
          </button>
          <button
            className="admin-primary"
            disabled={action.pending || snapshot.blocked}
          >
            Save override
          </button>
        </div>
      </form>
    </Dialog>
  );
}

function PriceDeletion({
  price,
  revision,
  action,
  onClose,
}: {
  price: PriceOverride;
  revision: string;
  action: ReturnType<typeof useAction>;
  onClose: () => void;
}) {
  const snapshot = usePriceRevision(revision, price, action);
  const remove = useMutation(OperatorService.method.deletePriceOverride);
  return (
    <Confirm
      title="Delete price override"
      name={price.modelId}
      pending={action.pending || snapshot.blocked}
      onClose={onClose}
      action={() => {
        if (snapshot.blocked) return;
        void action.run(
          () =>
            remove.mutateAsync({
              overrideId: price.id,
              expectedRevision: snapshot.revision,
            }),
          { done: onClose, success: "Price override deleted." },
        );
      }}
    >
      <p>
        Future requests use the bundled price where available; otherwise cost is
        unknown. Historical estimates remain unchanged.
      </p>
      <ErrorNotice error={action.error} />
      {snapshot.controls}
    </Confirm>
  );
}
