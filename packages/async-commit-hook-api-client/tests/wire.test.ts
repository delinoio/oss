import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { ExecutionState, RunSchema, RerunResponseSchema, LocalQuery, ListRepositoriesRequestSchema, ListRepositoriesResponseSchema, ListBranchesRequestSchema, ListBranchesResponseSchema, WorktreeSchema, ListRunsRequestSchema, ListCommitsRequestSchema, GetChangesRequestSchema } from "../src/index.js";
describe("v1 generated wire", () => {
  it("retains exact sequence and explicit nonpassing states", () => {
    const run = create(RunSchema, { id: "01900000-0000-7000-8000-000000000001", sequence: 9007199254740993n, state: ExecutionState.INTERRUPTED });
    expect(fromBinary(RunSchema, toBinary(RunSchema, run))).toEqual(run);
    expect(LocalQuery.acknowledge.name).toBe("Acknowledge");
  });
});

it("preserves a rerun receipt and its optional startup diagnostic", () => {
  const receipt = create(RerunResponseSchema, { runId: "accepted", startupDiagnostic: { code: "startup-failed", message: "Request remains saved", hint: "Inspect ach doctor" } });
  expect(fromBinary(RerunResponseSchema, toBinary(RerunResponseSchema, receipt))).toEqual(receipt);
  const legacy = create(RerunResponseSchema, { runId: "accepted" });
  expect(fromBinary(RerunResponseSchema, toBinary(RerunResponseSchema, legacy)).startupDiagnostic).toBeUndefined();
});

it("retains optional summary counts without requiring detail arrays", () => {
  for (const checkCount of [undefined, 0, 4000]) {
    const run = create(RunSchema, { id: "summary", checkCount });
    const decoded = fromBinary(RunSchema, toBinary(RunSchema, run));
    expect(decoded.checkCount).toBe(checkCount);
    expect(decoded.checks).toEqual([]);
  }
});


it("preserves worktree pagination and legacy last-page responses", () => {
  const request = create(ListRepositoriesRequestSchema, { cursor: "opaque-v1", limit: 50 });
  expect(fromBinary(ListRepositoriesRequestSchema, toBinary(ListRepositoriesRequestSchema, request))).toEqual(request);
  for (const nextCursor of ["", "next-worktree"]) {
    const response = create(ListRepositoriesResponseSchema, { repositories: [{ id: "same-repository", worktrees: [{ id: "tree" }] }], nextCursor });
    expect(fromBinary(ListRepositoriesResponseSchema, toBinary(ListRepositoriesResponseSchema, response))).toEqual(response);
  }
});


it("preserves scoped branch pagination and legacy responses", () => {
  const request = create(ListBranchesRequestSchema, { worktreeId: "tree", cursor: "opaque", limit: 50 });
  expect(fromBinary(ListBranchesRequestSchema, toBinary(ListBranchesRequestSchema, request))).toEqual(request);
  for (const nextCursor of ["", "next-ref"]) {
    const response = create(ListBranchesResponseSchema, { branches: [{ name: "main", commit: "oid" }], nextCursor });
    expect(fromBinary(ListBranchesResponseSchema, toBinary(ListBranchesResponseSchema, response))).toEqual(response);
  }
});

it("preserves opaque branch identities and legacy omissions", () => {
  const branches = create(ListBranchesResponseSchema, { branches: [{ id: "one", name: "bad-�", commit: "a" }, { id: "two", name: "bad-�", commit: "b" }] });
  expect(fromBinary(ListBranchesResponseSchema, toBinary(ListBranchesResponseSchema, branches))).toEqual(branches);
  const tree = create(WorktreeSchema, { branch: "bad-�", branchId: "one" });
  expect(fromBinary(WorktreeSchema, toBinary(WorktreeSchema, tree))).toEqual(tree);
  for (const schema of [ListRunsRequestSchema, ListCommitsRequestSchema, GetChangesRequestSchema]) {
    const request = create(schema, { worktreeId: "tree", branchId: "one" });
    expect(fromBinary(schema, toBinary(schema, request))).toEqual(request);
    expect(create(schema).branchId).toBe("");
  }
});
