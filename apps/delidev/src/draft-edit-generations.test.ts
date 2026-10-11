// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { DraftEditGenerations } from "./draft-edit-generations";

it("retains synchronous ABA and empty-draft history independently per owner", () => {
  const edits = new DraftEditGenerations();
  const confirmed = edits.read("a");
  // All three delivered events can precede one React render, including an
  // accepted same-value event or deletion of the empty text payload.
  expect(edits.advance("a")).toBe(true);
  expect(edits.advance("a")).toBe(true);
  expect(edits.advance("a")).toBe(true);
  expect(edits.read("a")).toBe(confirmed + 3n);
  expect(edits.read("b")).toBe(0n);
  expect(new DraftEditGenerations().read("a")).toBe(0n);
});

it("bounds history without evicting an original generation", () => {
  const edits = new DraftEditGenerations();
  for (let n = 0; n < 1000; n++) expect(edits.advance(String(n))).toBe(true);
  expect(edits.advance("new")).toBe(false);
  expect(edits.advance("0")).toBe(true);
  expect(edits.read("0")).toBe(2n);
});
