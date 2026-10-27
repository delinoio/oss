// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { OperatorService, type PriceOverride } from "@aiprouter/client";
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
  useAction,
} from "./common";
import { DestinationPolicyForm, DestinationPolicyNotice } from "./destinations";
import { RequestLimitsForm } from "./limits";
import { RetentionForm } from "./retention";
import { usePriceRevision, type PriceSnapshot } from "./price-revision";

export { Health } from "./instance-health";

export function InstanceConfiguration({
  section,
}: {
  section: "limits" | "retention" | "destinations";
}) {
  useLocale();
  const query = useQuery(OperatorService.method.getInstanceSettings, {});
  return (
    <>
      <PageHeader
        title={
          {
            limits: translate("requestLimits"),
            retention: translate("retention"),
            destinations: translate("approvedDestinations"),
          }[section]
        }
        description={
          section === "limits"
            ? translate("appliedPerUserWithinEachOrganization")
            : section === "retention"
              ? translate("setHowLongInstanceRecordsRemainAvailable")
              : translate("controlAdditionalOriginsForTheWholeInstance")
        }
      />
      {section !== "limits" && (
        <p className="admin-scope">{translate("instancePolicy")}</p>
      )}
      {section === "destinations" && <DestinationPolicyNotice />}
      {query.isPending ? (
        <Loading
          label={
            section === "destinations"
              ? translate("loadingDestinationPolicy")
              : undefined
          }
        />
      ) : query.error ? (
        <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      ) : query.data?.settings ? (
        section === "limits" ? (
          <RequestLimitsForm key={section} settings={query.data.settings} />
        ) : section === "retention" ? (
          query.data.settings.retention ? (
            <RetentionForm key={section} settings={query.data.settings} />
          ) : (
            <Unavailable retry={() => void query.refetch()} />
          )
        ) : (
          <DestinationPolicyForm key={section} settings={query.data.settings} />
        )
      ) : section === "retention" ? (
        <Unavailable retry={() => void query.refetch()} />
      ) : (
        <Empty>{translate("instanceSettingsAreUnavailable")}</Empty>
      )}
    </>
  );
}
// Retention never falls back to defaults or editable zero fields when the
// saved values are missing.
function Unavailable({ retry }: { retry: () => void }) {
  useLocale();
  return (
    <>
      <Empty>{translate("instanceSettingsAreUnavailable")}</Empty>
      <div className="admin-actions">
        <button onClick={retry}>{translate("tryAgain")}</button>
      </div>
    </>
  );
}
const rateFields = () =>
  [
    ["input", translate("input")],
    ["output", translate("output")],
    ["cacheRead", translate("cacheRead")],
    ["cacheWrite", translate("cacheWrite")],
    ["cacheWriteShort", translate("shortCacheWrite")],
    ["cacheWriteLong", translate("longCacheWrite")],
  ] as const;
export function Prices() {
  useLocale();
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
        title={translate("priceOverrides")}
        description={translate("commonPricesInCurrencyUnitsPerMillion")}
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
            {translate("addPriceOverride")}
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
              {translate("noPriceOverridesBundledPricesApplyWhere")}
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
                    {translate("tierProvenanceInstanceOperatorOverride", {
                      value0: price.serviceTier || translate("default"),
                    })}
                  </p>
                  <p>
                    {translate("updated", { value0: date(price.updatedAt) })}
                  </p>
                  {rateFields().map(([field, name]) => (
                    <p key={field}>
                      {name}:{" "}
                      {price.rates?.[field] === undefined
                        ? translate("unknown")
                        : translate("perMillionTokens", {
                            value0: price.rates.currency,
                            value1: price.rates[field],
                          })}
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
                    {translate("editOverride")}
                  </button>
                  <button
                    disabled={!query.data?.revision || action.pending}
                    onClick={() => {
                      action.clearError();
                      setDeleting({ price, revision: query.data!.revision });
                    }}
                  >
                    {translate("deleteOverride")}
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
  useLocale();
  const [provider, setProvider] = useState(price?.providerId ?? "");
  const [model, setModel] = useState(price?.modelId ?? "");
  const [tier, setTier] = useState(price?.serviceTier ?? "");
  const [currency, setCurrency] = useState(price?.rates?.currency ?? "USD");
  const [rates, setRates] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      rateFields().map(([field]) => [field, price?.rates?.[field] ?? ""]),
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
      title={
        price ? translate("editPriceOverride") : translate("addPriceOverride")
      }
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
          {translate("providerId")}
          <input
            required
            readOnly={!!price}
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
          />
        </label>
        <label>
          {translate("modelId_089ef2")}
          <input
            required
            readOnly={!!price}
            value={model}
            onChange={(e) => setModel(e.target.value)}
          />
        </label>
        <label>
          {translate("serviceTier_e9cf60")}
          <input
            readOnly={!!price}
            value={tier}
            onChange={(e) => setTier(e.target.value)}
          />
        </label>
        <label>
          {translate("currency")}
          <input
            required
            pattern="[A-Z]{3}"
            maxLength={3}
            value={currency}
            onChange={(e) => setCurrency(e.target.value.toUpperCase())}
          />
        </label>
        <p>{translate("ratesPerMillionTokensBlankMeansUnknown")}</p>
        <div className="admin-form-grid">
          {rateFields().map(([field, name]) => (
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
            {translate("cancel")}
          </button>
          <button
            className="admin-primary"
            disabled={action.pending || snapshot.blocked}
          >
            {translate("saveOverride")}
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
  useLocale();
  const snapshot = usePriceRevision(revision, price, action);
  const remove = useMutation(OperatorService.method.deletePriceOverride);
  return (
    <Confirm
      title={translate("deletePriceOverride")}
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
          { done: onClose, success: translate("priceOverrideDeleted") },
        );
      }}
    >
      <p>{translate("futureRequestsUseTheBundledPriceWhere")}</p>
      <ErrorNotice error={action.error} />
      {snapshot.controls}
    </Confirm>
  );
}
