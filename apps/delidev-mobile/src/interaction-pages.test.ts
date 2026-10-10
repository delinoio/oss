// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import type { Resource } from "@delinoio/delidev-api-client";
import { acceptInteractionPage, emptyInteractionPages, interactionByteLimit, interactionPageLimit } from "./interaction-pages";
const row = (id: string) => ({ id, revision: 1n, documentJson: new Uint8Array([1]) }) as Resource;
it("retains 51 original interactions in order and refreshes without duplicate ownership", () => {
  const first = Array.from({ length: 50 }, (_, n) => row(String(n)));
  let state = acceptInteractionPage(emptyInteractionPages(), "", first, "exact opaque token");
  const last = row("open-on-page-two");
  state = acceptInteractionPage(state, "exact opaque token", [last], "");
  expect(state.resources).toEqual([...first, last]); expect(state.resources[50]).toBe(last);
  expect(acceptInteractionPage(state, "", [row("refreshed")], "").resources.map(r => r.id)).toEqual(["refreshed"]);
});
it("rejects duplicate IDs, token cycles, out-of-order pages and excessive bytes without changing prior state", () => {
  const state = acceptInteractionPage(emptyInteractionPages(), "", [row("original")], "next");
  expect(() => acceptInteractionPage(state, "next", [row("original")], "")).toThrow();
  expect(() => acceptInteractionPage(state, "next", [row("second")], "next")).toThrow();
  expect(() => acceptInteractionPage(state, "foreign", [], "")).toThrow();
  expect(() => acceptInteractionPage(state, "next", [{ ...row("second"), documentJson: new Uint8Array(interactionByteLimit) }], "")).toThrow();
  expect(state.resources.map(r => r.id)).toEqual(["original"]);
});
it("bounds retained pages and replaces only the current refetched page", () => {
  let state = emptyInteractionPages();
  for (let n = 0; n < interactionPageLimit; n++) state = acceptInteractionPage(state, n ? String(n) : "", [row(String(n))], String(n + 1));
  expect(() => acceptInteractionPage(state, String(interactionPageLimit), [], "")).toThrow();
  const updated = { ...row(String(interactionPageLimit - 1)), revision: 2n };
  const refreshed = acceptInteractionPage(state, String(interactionPageLimit - 1), [updated], "");
  expect(refreshed.resources.at(-1)).toBe(updated); expect(refreshed.resources).toHaveLength(interactionPageLimit);
});
