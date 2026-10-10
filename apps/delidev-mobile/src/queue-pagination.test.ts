// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import type { Resource } from "@delinoio/delidev-api-client";
import { acceptQueuePage, emptyQueue, reachedQueue, queueContinuation } from "./queue-pagination";
const row = (n: number) => ({ id: `original-${n}`, revision: BigInt(n), sessionId: "original-session" }) as Resource;
const valid = (r: Resource) => r.sessionId === "original-session" && r.revision > 0n;
it("preserves 51 original rows and exact opaque cursor and revisions", () => {
  const first = Array.from({ length: 50 }, (_, n) => row(n + 1)), last = row(51);
  let state = acceptQueuePage(emptyQueue(), "", first, "original-token==", valid);
  state = acceptQueuePage(state, queueContinuation(state), [last], "", valid);
  expect(reachedQueue(state)).toEqual([...first, last]);
  expect(reachedQueue(state)[50]).toBe(last);
  expect(queueContinuation(state)).toBe(""); expect(state.invalid).toBe(false);
});
it("preserves reached originals on duplicate, nonadvancing, foreign and malformed pages", () => {
  const base = acceptQueuePage(emptyQueue(), "", [row(1)], "next", valid);
  for (const [rows, next] of [[[row(1)], ""], [[row(2)], "next"], [[], "later"], [[{ ...row(2), sessionId: "foreign" }], ""], [Array.from({ length: 51 }, (_, n) => row(n + 2)), ""]] as [Resource[], string][]) {
    const failed = acceptQueuePage(base, "next", rows, next, valid);
    expect(failed.invalid).toBe(true); expect(reachedQueue(failed)).toEqual([row(1)]);
  }
  expect(acceptQueuePage(base, "expired-or-foreign", [row(2)], "", valid).invalid).toBe(true);
});
it("handles empty completion, the 1000 bound and a fresh first-page reload", () => {
  expect(acceptQueuePage(emptyQueue(), "", [], "", valid).invalid).toBe(false);
  let state = emptyQueue();
  for (let p = 0; p < 20; p++) state = acceptQueuePage(state, p ? `page-${p}` : "", Array.from({ length: 50 }, (_, n) => row(p * 50 + n + 1)), p === 19 ? "" : `page-${p + 1}`, valid);
  expect(reachedQueue(state)).toHaveLength(1000); expect(state.invalid).toBe(false);
  const overflow = acceptQueuePage(state, "", Array.from({ length: 50 }, (_, n) => row(n + 1)), "next", valid);
  expect(reachedQueue(overflow)).toHaveLength(50); // Refreshed first page discards every stale suffix.
  let bounded = emptyQueue();
  for (let p = 0; p < 20; p++) bounded = acceptQueuePage(bounded, p ? `page-${p}` : "", Array.from({ length: 50 }, (_, n) => row(p * 50 + n + 1)), `page-${p + 1}`, valid);
  expect(bounded.invalid).toBe(true); expect(reachedQueue(bounded)).toHaveLength(950);
});
