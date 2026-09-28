import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PRReviewers, validPRReviewers } from "./github-reviewers";
import { reviewerObservation } from "./github-reviewers-fixture";

const item = { base_sha: "a".repeat(40), head_sha: "b".repeat(40), url: "https://github.com/fixture-owner/repo/pull/17" };
it("separates verified Bot identity from absent collaborator and App proof", () => {
  const value = reviewerObservation(); expect(validPRReviewers(value, item)).toBe(true); render(<PRReviewers value={value} />);
  expect(screen.getByRole("table", { name: "Current feedback author identities and permissions" })).toBeTruthy();
  expect(screen.getByText(/Verified identity/)).toBeTruthy();
  expect(screen.getByText("No collaborator grant")).toBeTruthy();
  expect(screen.getByText(/Public repository visibility is not a collaborator permission/)).toBeTruthy();
  expect(screen.getAllByText(/App not verified/)).toHaveLength(2);
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});
it("displays a custom role's proven floor and uncertain higher role", () => {
  const value = reviewerObservation(); value.actors[0].permission = { access: "available", legacy_permission: "write", minimum: "WRITE", maximum: "MAINTAIN" };
  const withRole = { ...value, actors: [{ ...value.actors[0], permission: { ...value.actors[0].permission, role_name: "Custom maintainer" } }] };
  expect(validPRReviewers(withRole, item)).toBe(true); render(<PRReviewers value={withRole} />);
  expect(screen.getByText(/WRITE verified; MAINTAIN uncertain/)).toBeTruthy();
});
it("rejects foreign identities, inferred Apps, stale versions and inflated permission", () => {
  for (const mode of ["identity", "permission", "stale-app", "missing-actor", "duplicate-app", "app-on-review", "unknown-identity"]) {
    const value = reviewerObservation();
    switch (mode) {
      case "identity": value.actors[0].identity.node_id = "FOREIGN"; break;
      case "permission": value.actors[0].permission.minimum = "ADMIN"; break;
      case "stale-app": value.applications[0].content_version = "x".repeat(64); break;
      case "missing-actor": value.actors = []; break;
      case "duplicate-app": value.applications[1] = value.applications[0]; break;
      case "app-on-review": value.applications[0].access = "available"; value.applications[0].state = "none"; break;
      case "unknown-identity": value.actors[0].identity_access = "unavailable"; break;
    }
    expect(validPRReviewers(value, item), mode).toBe(false);
  }
});
