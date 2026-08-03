import { describe, expect, it } from "vitest";

import {
  parseDeckQuery,
  serializeDeckQuery,
  updateDeckQueryClause,
} from "./query";

describe("Deck raw and visual GitHub query editing", () => {
  it("round trips recognized and unknown clauses without dropping unknown syntax", () => {
    const raw = 'repo:delinoio/oss label:"needs review" no:assignee custom:future-term free-text';
    const parsed = parseDeckQuery(raw);
    expect(parsed.clauses.map((clause) => [clause.field, clause.value])).toEqual([
      ["repo", "delinoio/oss"],
      ["label", "needs review"],
    ]);
    expect(parsed.unknownClauses).toEqual(["no:assignee", "custom:future-term", "free-text"]);
    expect(serializeDeckQuery(parsed)).toBe(raw);
  });

  it("rewrites only a recognized builder clause and preserves negation and unknown clauses", () => {
    const parsed = parseDeckQuery("-author:@me archived:false");
    const clause = parsed.clauses[0]!;
    expect(
      serializeDeckQuery(updateDeckQueryClause(parsed, clause.id, {
        field: "review-requested",
        value: "@me",
        negated: false,
      })),
    ).toBe("review-requested:@me archived:false");
  });

  it("preserves unknown clauses in place when recognized clauses are interleaved", () => {
    const parsed = parseDeckQuery("future:first repo:delinoio/oss free-text label:ready tail:last");
    const repository = parsed.clauses[0]!;
    expect(
      serializeDeckQuery(updateDeckQueryClause(parsed, repository.id, {
        ...repository,
        value: "delinoio/devhud",
      })),
    ).toBe("future:first repo:delinoio/devhud free-text label:ready tail:last");
  });

  it("keeps quoted values parseable after a builder edit", () => {
    const parsed = parseDeckQuery('label:"release blocker"');
    expect(serializeDeckQuery(parsed)).toBe('label:"release blocker"');
    expect(parseDeckQuery(serializeDeckQuery(parsed))).toEqual(parsed);
  });

  it("projects canonical owner and state qualifiers into builder fields", () => {
    const raw = "user:octocat org:delinoio is:open is:draft";
    const parsed = parseDeckQuery(raw);

    expect(parsed.clauses.map((clause) => [
      clause.field,
      clause.value,
      clause.ownerQualifier,
    ])).toEqual([
      ["owner", "octocat", "user"],
      ["owner", "delinoio", "org"],
      ["state", "open", undefined],
      ["draft", "draft", undefined],
    ]);
    expect(serializeDeckQuery(parsed)).toBe(raw);
  });

  it("serializes builder field changes with canonical GitHub qualifiers", () => {
    const parsed = parseDeckQuery("repo:delinoio/oss");
    const repository = parsed.clauses[0]!;
    const state = updateDeckQueryClause(parsed, repository.id, {
      ...repository,
      field: "state",
    });
    expect(serializeDeckQuery(state)).toBe("is:open");

    const draft = updateDeckQueryClause(state, repository.id, {
      ...state.clauses[0]!,
      field: "draft",
    });
    expect(serializeDeckQuery(draft)).toBe("is:draft");

    const owner = updateDeckQueryClause(draft, repository.id, {
      ...draft.clauses[0]!,
      field: "owner",
    });
    expect(serializeDeckQuery(owner)).toBe("org:organization");
  });
});
