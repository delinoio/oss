// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import type { PriceOverride } from "@aiprouter/client";
import { useApi } from "../api";
import { ErrorNotice, type useAction } from "./common";

export type PriceIdentity = Pick<
  PriceOverride,
  "providerId" | "modelId" | "serviceTier"
>;
export type PriceSnapshot = { price?: PriceOverride; revision: string };
export function usePriceRevision(
  initial: string,
  identity: PriceIdentity,
  action: ReturnType<typeof useAction>,
) {
  useLocale();
  const api = useApi();
  const [revision, setRevision] = useState(initial);
  const [candidate, setReview] = useState<
    PriceSnapshot & { identityKey: string }
  >();
  const identityKey = JSON.stringify([
    identity.providerId,
    identity.modelId,
    identity.serviceTier,
  ]);
  const review = candidate?.identityKey === identityKey ? candidate : undefined;
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<unknown>();
  const active = useRef(false);
  const conflicted =
    !!action.error && ConnectError.from(action.error).code === Code.Aborted;
  async function refresh() {
    if (active.current) return;
    active.current = true;
    setRefreshing(true);
    setError(undefined);
    setReview(undefined);
    try {
      let token = "",
        snapshotRevision = "";
      const seen = new Set<string>();
      do {
        const page = await api.operator.listPriceOverrides({
          page: { pageSize: 100, pageToken: token },
        });
        if (
          !page.revision ||
          (snapshotRevision && snapshotRevision !== page.revision)
        )
          throw new ConnectError(
            translate("collectionChangedDuringReview"),
            Code.Aborted,
          );
        snapshotRevision = page.revision;
        const price = page.overrides.find(
          (p) =>
            p.providerId === identity.providerId &&
            p.modelId === identity.modelId &&
            p.serviceTier === identity.serviceTier,
        );
        if (price) {
          setReview({ price, revision: snapshotRevision, identityKey });
          return;
        }
        token = page.page?.nextPageToken ?? "";
        if (token && seen.has(token))
          throw new ConnectError(translate("repeatedPageToken"), Code.Internal);
        seen.add(token);
      } while (token);
      setReview({ revision: snapshotRevision, identityKey });
    } catch (cause) {
      setError(cause);
    } finally {
      active.current = false;
      setRefreshing(false);
    }
  }
  const controls = (
    <>
      {conflicted && (
        <p role="alert">{translate("pricesChangedSinceYouOpenedThisForm")}</p>
      )}
      <button
        type="button"
        disabled={refreshing || action.pending}
        onClick={() => void refresh()}
      >
        {refreshing
          ? translate("refreshingPrices")
          : translate("refreshPricesForReview")}
      </button>
      <ErrorNotice error={error} />
      {review && (
        <section
          aria-label={translate("currentPriceReview")}
          className="admin-banner"
        >
          <h3>{translate("currentSavedPrice")}</h3>
          {review.price ? (
            <>
              <p>
                {review.price.providerId} / {review.price.modelId} ·{" "}
                {review.price.serviceTier || translate("defaultTier")}
              </p>
              <p>
                {translate("currency_633895", {
                  value0: review.price.rates?.currency || translate("unknown"),
                })}
              </p>
              {(
                [
                  ["input", translate("input")],
                  ["output", translate("output")],
                  ["cacheRead", translate("cacheRead")],
                  ["cacheWrite", translate("cacheWrite")],
                  ["cacheWriteShort", translate("shortCacheWrite")],
                  ["cacheWriteLong", translate("longCacheWrite")],
                ] as const
              ).map(([key, label]) => (
                <p key={key}>
                  {translate("perMillionTokens_21d1dd", {
                    value0: label,
                    value1: review.price?.rates?.[key] ?? translate("unknown"),
                  })}
                </p>
              ))}
            </>
          ) : (
            <p>{translate("noOverrideExistsForThisProviderModel")}</p>
          )}
          <p>{translate("yourEnteredRatesHaveNotChangedSaving")}</p>
          <button
            type="button"
            onClick={() => {
              setRevision(review.revision);
              setReview(undefined);
              action.clearError();
            }}
          >
            {translate("useReviewedRevision")}
          </button>
        </section>
      )}
    </>
  );
  return {
    revision,
    blocked: !revision || conflicted || refreshing || !!review,
    controls,
  };
}
