// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState } from "react";
import { SubscriptionServiceIdentity, UsageAccountingProfile, UsageTimeGranularity } from "@delinoio/delidev-api-client";
import type { UsageEntry } from "./usage-entry";
import { detectDeviceTimeZone, localDateTimeToUnixMs, unixMsToLocalDateTime } from "./usage-time";

interface Filters { from: string; until: string; sessionId: string; projectId: string; accountId: string; providerId: string; subscriptionService: SubscriptionServiceIdentity; modelId: string; generalChat: boolean }
const emptyFilters: Filters = { from: "", until: "", sessionId: "", projectId: "", accountId: "", providerId: "", subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED, modelId: "", generalChat: false };
function defaults(timeZone: string) {
  const { from: _from, until: _until, ...filters } = emptyFilters;
  return { ...filters, fromUnixMs: 0n, untilUnixMs: 0n, granularity: UsageTimeGranularity.DAY, timeZone, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 };
}
export type UsageSelection = ReturnType<typeof defaults>;
interface State { draft: Filters; applied: Filters; selection: UsageSelection; invalid: boolean; pending: boolean }

/** Own draft validation and timer disposal without changing focus or navigation. */
export function useUsageFilters(active: boolean, entry?: UsageEntry) {
  const [state, setState] = useState<State>(() => ({ draft: emptyFilters, applied: emptyFilters, selection: defaults(detectDeviceTimeZone()), invalid: false, pending: false }));
  const current = useRef(state);
  const consumedEntry = useRef<string>(undefined);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const cancel = () => { clearTimeout(timer.current); timer.current = undefined; };
  const publish = (next: State) => { current.current = next; setState(next); };
  const validate = (snapshot: State) => {
    const { draft, applied, selection } = snapshot;
    try {
      // An unchanged wall-clock endpoint still owns its original instant. This
      // preserves entry milliseconds and later DST-fold occurrences until edit.
      const fromUnixMs = draft.from === applied.from ? selection.fromUnixMs : localDateTimeToUnixMs(draft.from, selection.timeZone);
      const untilUnixMs = draft.until === applied.until ? selection.untilUnixMs : localDateTimeToUnixMs(draft.until, selection.timeZone);
      const until = untilUnixMs || BigInt(Date.now());
      const from = fromUnixMs || until - 30n * 86_400_000n;
      if (from <= 0n || until <= from || until - from > 366n * 86_400_000n) throw new Error("range");
      const { from: _from, until: _until, ...filters } = draft;
      publish({ ...snapshot, applied: draft, selection: { ...selection, ...filters, fromUnixMs, untilUnixMs }, invalid: false, pending: false });
    } catch {
      publish({ ...snapshot, invalid: true, pending: false });
    }
  };
  useLayoutEffect(() => {
    if (!active || !entry || consumedEntry.current === entry.key) return;
    cancel();
    consumedEntry.current = entry.key;
    const zone = detectDeviceTimeZone();
    const draft = { ...emptyFilters, accountId: entry.accountId, from: unixMsToLocalDateTime(entry.fromUnixMs, zone), until: unixMsToLocalDateTime(entry.untilUnixMs, zone) };
    publish({ draft, applied: draft, selection: { ...defaults(zone), accountId: entry.accountId, fromUnixMs: entry.fromUnixMs, untilUnixMs: entry.untilUnixMs }, invalid: false, pending: false });
  }, [active, entry]);
  useLayoutEffect(() => {
    cancel();
    if (active && current.current.pending) timer.current = setTimeout(() => validate(current.current), 300);
    return cancel;
  }, [active, state.draft, state.pending]);
  const edit = (patch: Partial<Filters>, date = false) => {
    cancel();
    const snapshot = { ...current.current, draft: { ...current.current.draft, ...patch }, pending: date, invalid: date ? false : current.current.invalid };
    if (date || !active) publish(snapshot);
    else validate(snapshot);
  };
  const change = <K extends keyof Filters>(key: K, value: Filters[K]) => edit({ [key]: value }, key === "from" || key === "until");
  const reset = () => {
    cancel();
    publish({ draft: emptyFilters, applied: emptyFilters, selection: defaults(detectDeviceTimeZone()), invalid: false, pending: false });
  };
  return { ...state, change, edit, reset, ready: !entry || consumedEntry.current === entry.key };
}
