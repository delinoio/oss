// SPDX-License-Identifier: Apache-2.0
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
            "Collection changed during review",
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
          throw new ConnectError("Repeated page token", Code.Internal);
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
        <p role="alert">
          Prices changed since you opened this form. Your entries are preserved.
          Refresh the current price and review it before saving again.
        </p>
      )}
      <button
        type="button"
        disabled={refreshing || action.pending}
        onClick={() => void refresh()}
      >
        {refreshing ? "Refreshing prices…" : "Refresh prices for review"}
      </button>
      <ErrorNotice error={error} />
      {review && (
        <section aria-label="Current price review" className="admin-banner">
          <h3>Current saved price</h3>
          {review.price ? (
            <>
              <p>
                {review.price.providerId} / {review.price.modelId} ·{" "}
                {review.price.serviceTier || "Default tier"}
              </p>
              <p>Currency: {review.price.rates?.currency || "Unknown"}</p>
              {(
                [
                  ["input", "Input"],
                  ["output", "Output"],
                  ["cacheRead", "Cache read"],
                  ["cacheWrite", "Cache write"],
                  ["cacheWriteShort", "Short cache write"],
                  ["cacheWriteLong", "Long cache write"],
                ] as const
              ).map(([key, label]) => (
                <p key={key}>
                  {label}: {review.price?.rates?.[key] ?? "Unknown"} per million
                  tokens
                </p>
              ))}
            </>
          ) : (
            <p>No override exists for this provider, model and tier.</p>
          )}
          <p>
            Your entered rates have not changed. Saving replaces this current
            price for future requests only.
          </p>
          <button
            type="button"
            onClick={() => {
              setRevision(review.revision);
              setReview(undefined);
              action.clearError();
            }}
          >
            Use reviewed revision
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
