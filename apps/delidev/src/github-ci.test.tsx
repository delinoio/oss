import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PRCI, validPRCI } from "./github-ci";

const item = { state: "open", merged: false, base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40) };
import { ciObservation } from "./github-ci-fixture";

it("shows required failure with exact rule, App and evaluated commit provenance", () => {
  const value = ciObservation();
  expect(validPRCI(value, item)).toBe(true);
  render(<PRCI value={value} />);
  expect(screen.getByRole("status").textContent).toBe("Terminal required CI failure");
  expect(screen.getByText(item.head_sha)).toBeTruthy();
  expect(screen.getByRole("table", { name: "Active ruleset CI requirements" })).toBeTruthy();
  expect(screen.getByText(/ruleset 9007199254740993/)).toBeTruthy();
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});
it("rejects partial contexts, foreign sources, optional references and duplicate IDs", () => {
  const partial = ciObservation(); partial.head.total_count = "2";
  expect(validPRCI(partial, item)).toBe(false);
  const foreign = ciObservation(); foreign.result.evaluated_sha = "f".repeat(40);
  expect(validPRCI(foreign, item)).toBe(false);
  const optional = ciObservation(); optional.head.contexts[0].required = false;
  expect(validPRCI(optional, item)).toBe(false);
  const duplicate = ciObservation(); duplicate.result.requirements[0].result_node_ids.push("CHECK_1");
  expect(validPRCI(duplicate, item)).toBe(false);
  const mixed = ciObservation(); mixed.in_merge_queue = true;
  expect(validPRCI(mixed, item)).toBe(false);
});
it("keeps an unknown evaluated commit explicit without a required-failure table", () => {
  const value = ciObservation();
  const unknown = { ...value, native_mergeability: "UNKNOWN", test_merge: undefined, result: { source: "unknown", state: "unknown", reason: "commit-unverified", requirements: [] } };
  expect(validPRCI(unknown, item)).toBe(true);
  render(<PRCI value={unknown} />);
  expect(screen.getByRole("status").textContent).toBe("Unknown");
  expect(screen.queryByRole("table")).toBeNull();
  expect(screen.getByText(/evaluated commit could not be verified/)).toBeTruthy();
});
