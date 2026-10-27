// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "../i18n";
import { useEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import type { PriceOverride } from "@aiprouter/client";
import { useApi } from "../api";
import { ErrorNotice, type useAction } from "./common";
import { PriceRates, priceRecordName } from "./price-display";

// Controls the context-specific helper shown in the current-price review.
export enum PriceReviewContext {
  Save = "save",
  Delete = "delete",
}
// The primary submit action of the surrounding dialog. It receives focus after
// the operator explicitly adopts the reviewed revision.
const submitActionSelector =
  "[data-price-submit], form .admin-actions button:not([data-cancel])";

export type PriceIdentity = Pick<
  PriceOverride,
  "providerId" | "modelId" | "serviceTier"
>;
export type PriceSnapshot = { price?: PriceOverride; revision: string };
export function usePriceRevision(
  initial: string,
  identity: PriceIdentity,
  action: ReturnType<typeof useAction>,
  context: PriceReviewContext = PriceReviewContext.Save,
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
  const controlsRef = useRef<HTMLDivElement>(null);
  // Counts explicit adoptions so the announcement and focus move run once each.
  const [adoptions, setAdoptions] = useState(0);
  // Identity key whose reviewed revision was adopted, if any.
  const [adoptedKey, setAdoptedKey] = useState<string>();
  useEffect(() => {
    if (!adoptions) return;
    controlsRef.current
      ?.closest("dialog")
      ?.querySelector<HTMLElement>(submitActionSelector)
      ?.focus();
  }, [adoptions]);
  const conflicted =
    !!action.error && ConnectError.from(action.error).code === Code.Aborted;
  async function refresh() {
    if (active.current) return;
    active.current = true;
    setRefreshing(true);
    setError(undefined);
    setReview(undefined);
    setAdoptedKey(undefined);
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
    <div ref={controlsRef} className="admin-price-review">
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
          className="admin-banner admin-price-current"
        >
          <h3>{translate("currentSavedPrice")}</h3>
          {review.price ? (
            <>
              <p>{priceRecordName(review.price)}</p>
              <p>
                {translate("currency_633895", {
                  value0: review.price.rates?.currency || translate("unknown"),
                })}
              </p>
              <PriceRates rates={review.price.rates} />
            </>
          ) : (
            <p>{translate("noOverrideExistsForThisProviderModel")}</p>
          )}
          <p>
            {context === PriceReviewContext.Delete
              ? translate("reviewDoesNotDeleteThisOverrideDeletion")
              : translate("yourEnteredRatesHaveNotChangedSaving")}
          </p>
          <button
            type="button"
            onClick={() => {
              setRevision(review.revision);
              setReview(undefined);
              action.clearError();
              setAdoptedKey(identityKey);
              setAdoptions((count) => count + 1);
            }}
          >
            {translate("useReviewedRevision")}
          </button>
        </section>
      )}
      <p role="status" className="admin-price-adopted">
        {adoptedKey === identityKey && !review && !conflicted
          ? translate("reviewedRevisionSelectedYourEntriesAre")
          : ""}
      </p>
    </div>
  );
  return {
    revision,
    // The conflict alert in the controls already announces this mutation error.
    conflicted,
    blocked: !revision || conflicted || refreshing || !!review,
    controls,
  };
}
