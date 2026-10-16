// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  SubscriptionQuery,
  SubscriptionQuotaRefreshTrigger,
  SystemCapability,
  SystemQuery,
  isEntityId,
  newRequestId,
  type RefreshAllSubscriptionQuotasRequest,
  type RefreshAllSubscriptionQuotasResponse,
} from "@delinoio/delidev-api-client";
import {
  isDocumentForeground,
  useDocumentForeground,
} from "./document-foreground";
import { subscriptionQuotaAllKey, useRetainedMutation } from "./mutation";

/** Foreground cadence for both the automatic batch request and the saved
 * resource rereads that deliver completed observations to existing displays. */
export const subscriptionForegroundRefreshInterval = 30000;

/** Stable closed set of reasons a scheduled tick sends nothing. */
export enum SubscriptionQuotaTickOutcome {
  Admitted = "admitted",
  GateClosed = "gate_closed",
  Busy = "busy",
  Uncertain = "uncertain",
}

/** The one acknowledgement predicate shared by manual Refresh all and the
 * automatic owner. It proves request identity and a bounded unique inventory;
 * acknowledgement is admission, never evidence of a fresh observation. */
export function acknowledgeRefreshAllSubscriptionQuotas(
  result: RefreshAllSubscriptionQuotasResponse,
  request: RefreshAllSubscriptionQuotasRequest,
) {
  return (
    result.requestId === request.requestId &&
    result.accounts.length <= 10000 &&
    new Set(result.accounts).size === result.accounts.length &&
    result.accounts.every(isEntityId)
  );
}

/** One connection-owned automatic Refresh all cadence. It mounts under the
 * authenticated connection-level mutation provider, above navigation, and
 * shares the retained `subscription:quota:all` request with Settings. It owns
 * no retry authority: an uncertain original request is only ever retried by the
 * explicit Settings action, and a busy request is never replaced. */
export function SubscriptionQuotaAutoRefresh({ ready }: { ready: boolean }) {
  const { foreground, activation, reconcile } = useDocumentForeground();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: ready });
  const supported =
    status.data?.capabilities.includes(
      SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2,
    ) === true;
  const mutation = useRetainedMutation(
    subscriptionQuotaAllKey,
    SubscriptionQuery.refreshAllSubscriptionQuotas,
    undefined,
    acknowledgeRefreshAllSubscriptionQuotas,
  );
  const eligible =
    ready && foreground && supported && !status.error && !status.data?.stopping;
  // Ticks recheck the newest gates immediately before sending, so a queued
  // callback that outlives a blur, disconnect or stopping status sends nothing.
  const latest = useRef({ mutation, eligible });
  latest.current = { mutation, eligible };
  useEffect(() => {
    if (!eligible) return;
    let timer: number | undefined;
    const arm = () => {
      timer = window.setTimeout(tick, subscriptionForegroundRefreshInterval);
    };
    const tick = () => {
      const { mutation: current, eligible: open } = latest.current;
      const foregroundNow = isDocumentForeground();
      if (!open || !foregroundNow) {
        // A closed gate ends this wait. Regaining eligibility starts a fresh one,
        // and missed ticks are never replayed.
        if (!foregroundNow) reconcile();
        console.info("delidev.subscription.quota_refresh_tick", {
          outcome: SubscriptionQuotaTickOutcome.GateClosed,
        });
        return;
      }
      // Keep the cadence independent of how long one request takes.
      arm();
      const outcome = current.busy
        ? SubscriptionQuotaTickOutcome.Busy
        : current.uncertain
          ? SubscriptionQuotaTickOutcome.Uncertain
          : SubscriptionQuotaTickOutcome.Admitted;
      console.info("delidev.subscription.quota_refresh_tick", { outcome });
      if (outcome === SubscriptionQuotaTickOutcome.Admitted)
        void current.send({
          requestId: newRequestId(),
          trigger: SubscriptionQuotaRefreshTrigger.FOREGROUND,
        });
    };
    arm();
    return () => window.clearTimeout(timer);
  }, [eligible, activation, reconcile]);
  return null;
}
