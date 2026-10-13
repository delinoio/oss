// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";

// setTimeout stores its delay in a signed 32-bit integer.
const maxTimerDelay = 2 ** 31 - 1;

/**
 * A listed row only carries a point-in-time `authenticated` flag. The safe
 * credential expiry lets the desktop revoke that claim at the deadline without
 * another read.
 */
export function credentialCurrent(
  row: { authenticated: boolean; credentialExpiresAt: string },
  now: number,
): boolean {
  if (!row.authenticated) return false;
  if (!row.credentialExpiresAt) return true;
  const deadline = Date.parse(row.credentialExpiresAt);
  return Number.isFinite(deadline) && now < deadline;
}

/** Re-renders its caller at the next credential deadline among the rows. */
export function useCredentialClock(
  rows: readonly { credentialExpiresAt: string }[],
): number {
  const [now, setNow] = useState(() => Date.now());
  const expiries = rows.map((row) => row.credentialExpiresAt).join("|");
  useEffect(() => {
    const current = Date.now();
    const next = expiries
      .split("|")
      .map((value) => (value ? Date.parse(value) : Number.NaN))
      .filter((value) => Number.isFinite(value) && value > current)
      .sort((a, b) => a - b)[0];
    if (next === undefined) {
      if (current > now) setNow(current);
      return;
    }
    const timer = setTimeout(
      () => setNow(Date.now()),
      Math.min(next - current + 1, maxTimerDelay),
    );
    return () => clearTimeout(timer);
  }, [expiries, now]);
  return now;
}
