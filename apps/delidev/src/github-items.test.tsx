import { reviewerObservation } from "./github-reviewers-fixture";
import { feedbackObservation } from "./github-feedback-fixture";
import { ciObservation } from "./github-ci-fixture";
import { createHash } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { RepositoryGitHubItems, githubResult } from "./github-items";
import { encode } from "./documents";
import { ItemKind, QueryOperation, type GitHubQuery } from "./github-query-model";

function fixture(headRepository?: unknown) {
  const repository = create(ResourceSchema, { id: newRequestId(), revision: 9007199254740993n, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode({ integration_id: newRequestId(), github_owner: "fixture-owner", github_name: "repo" }) });
  const profile = JSON.parse(new TextDecoder().decode(repository.documentJson)).integration_id as string;
  const generation = newRequestId();
  const query = vi.fn(async (request: { repositoryId: string; queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson)), detail = ["detail", "diff", "checks", "statuses", "rules", "ci", "feedback", "reviewers"].includes(q.operation), search = q.operation === "search", pr = q.kind === "pull-request";
    const item = { provider: "github.com", kind: q.kind, identity_source: pr && !search ? "pull-request-api" : "issue-api", id: "9007199254740993", node_id: "ITEM_17", number: "17", title: "Original fixture title", state: "open", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-28T00:00:00Z", author: { id: "19", node_id: "U_19", login: "fixture-author", kind: "user", provider_type: "User" }, url: `https://github.com/fixture-owner/repo/${pr ? "pull" : "issues"}/17`, ...(pr ? { draft: false } : {}), ...(detail ? { body: "<script>never executed</script>\nOriginal body", ...(pr ? { merged: false, ...(headRepository === undefined ? {} : { head_repository: headRepository }), base_ref: "main", base_sha: "a".repeat(40), head_ref: "feature", head_sha: "b".repeat(40) } : {}) } : {}) };
    const patch = "diff --git a/file b/file\n+Original patch\n";
    const observation = q.operation === "reviewers" ? { reviewers: reviewerObservation() } : q.operation === "feedback" ? { feedback: feedbackObservation() } : q.operation === "ci" ? { ci: ciObservation() } : q.operation === "rules" ? { rules: { base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40), digest: "c".repeat(64), rules: [{ type: "required_status_checks", ruleset_id: "9007199254740993", source_kind: "repository", native_source_kind: "Repository", source: "fixture-owner/repo", digest: "d".repeat(64), required_checks: { strict: false, checks: [{ context: "CI Result", integration_id: "15368" }] } }] } } : q.operation === "diff" ? { diff: { patch, digest: createHash("sha256").update(patch).digest("hex"), base_sha: "a".repeat(40), head_sha: "b".repeat(40) } } : q.operation === "checks" ? { checks: { head_sha: "b".repeat(40), filter: "latest", total_count: "1", runs: [{ id: "53", node_id: "CHECK_53", name: "Fixture Check", head_sha: "b".repeat(40), status: "completed", native_status: "completed", conclusion: "success", native_conclusion: "success", application: { id: "15368", node_id: "APP_15368", slug: "github-actions" } }] } } : q.operation === "statuses" ? { statuses: { head_sha: "b".repeat(40), state: "pending", native_state: "pending", total_count: "0", contexts: [] } } : {};
    return { schemaVersion: 1, documentJson: encode({ ...observation, repository_id: repository.id, repository_revision: repository.revision.toString(), profile_id: profile, generation_id: generation, observed_at: "2026-09-28T00:00:00Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: "fixture-owner", name: "repo", private: true }, query: q, items: [item], ...(search ? { total_count: "1001", incomplete: true } : {}), ...(!detail && q.page === 1 ? { next_page: 2 } : {}) }) };
  });
  const inspect = vi.fn(() => { throw new Error("Retired access inspection must not run"); });
  const transport = createRouterTransport((router) => router.service(IntegrationService, { queryRepositoryIntegration: query, inspectRepositoryIntegration: inspect }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><RepositoryGitHubItems selected={repository} active={active} /></QueryClientProvider></TransportProvider>;
  return { repository, query, inspect, client, view };
}
it("opens explicit repository results and reads detail without inventing mergeability", async () => {
  const f = fixture(); const view = render(f.view());
  expect(f.query).not.toHaveBeenCalled(); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  await screen.findByRole("button", { name: "Read #17" });
  expect(f.inspect).not.toHaveBeenCalled();
  expect(f.query.mock.calls[0][0].repositoryId).toBe(f.repository.id);
  fireEvent.click(screen.getByRole("button", { name: "Read #17" }));
  await screen.findByText(/Mergeability: Unknown/);
  expect(screen.getByText(/<script>never executed/)).toBeTruthy();
  expect(view.container.querySelector("script")).toBeNull();
  expect(screen.queryAllByRole("link")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Back to results" }));
  await screen.findByRole("button", { name: "Read #17" });
});
it("sends plain issue search separately and discloses incomplete search", async () => {
  const f = fixture(); render(f.view());fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));await screen.findByRole("button", { name: "Read #17" });
  fireEvent.change(screen.getByLabelText("GitHub item type"), { target: { value: "issue" } });
  fireEvent.change(screen.getByLabelText("Search title and body"), { target: { value: "fix OR 오류" } });
  fireEvent.click(screen.getByRole("button", { name: "Read GitHub items" }));
  await screen.findByText(/GitHub returned incomplete search results/);
  const args = JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson));
  expect(args).toMatchObject({ kind: "issue", operation: "search", search: "fix OR 오류", page: 1 });
  fireEvent.click(screen.getByRole("button", { name: "Load more GitHub query results" }));
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson)).page).toBe(2));
});
it("rejects a foreign revision without rendering partial items", async () => {
  const f = fixture(); const valid = await f.query({ repositoryId: f.repository.id, queryJson: encode({ kind: "pull-request", operation: "list", state: "open", page: 1, page_size: 20 }) });
  const result = JSON.parse(new TextDecoder().decode(valid.documentJson)); result.repository_revision = "2";
  f.query.mockResolvedValueOnce({ schemaVersion: 1, documentJson: encode(result) });render(f.view());fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  await screen.findByRole("alert"); expect(screen.queryByRole("button", { name: "Read #17" })).toBeNull();
});
it("discards repository content on inactivation", async () => {
  const f = fixture();const view = render(f.view());fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));await screen.findByRole("button", { name: "Read #17" });
  view.rerender(f.view(false));expect(screen.queryByText("Original fixture title")).toBeNull();
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(0));expect(f.query).toHaveBeenCalledTimes(1);
});

it("reads checks and empty pending commit statuses independently through the PR detail", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" })); fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read PR checks" }));
  await screen.findByRole("table", { name: "Check runs for the observed head" }); expect(screen.getByText("success")).toBeTruthy();
  expect(screen.queryByText(/GitHub combined commit status/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Back to results" })); fireEvent.click(await screen.findByRole("button", { name: "Read PR commit statuses" }));
  await screen.findByText(/GitHub combined commit status: pending/); expect(screen.getByText("No commit status contexts on this returned page.")).toBeTruthy(); expect(screen.queryByRole("table")).toBeNull();
  expect(screen.getByText(/Missing, pending or unknown results do not establish passing CI/)).toBeTruthy();
  fireEvent.change(screen.getByLabelText("PR result page size"), { target: { value: "1" } });
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson))).toMatchObject({ operation: "statuses", number: "17", page: 1, page_size: 1 }));
});
it("shows the original immutable diff text without rendering active content", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" })); fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));fireEvent.click(await screen.findByRole("button", { name: "Read PR diff" }));
  await screen.findByText(/Original patch/); expect(screen.queryByRole("navigation", { name: "GitHub result pages" })).toBeNull();
  expect(screen.getByText(/Immutable base/)).toBeTruthy();
});
it("reads complete active base rules without inferring successful CI", async () => {
  const f = fixture(); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read active PR rules" }));
  await screen.findByRole("table", { name: "Required status checks · ruleset 9007199254740993" });
  expect(screen.getByText("CI Result")).toBeTruthy();
  expect(screen.getByText("App 15368")).toBeTruthy();
  expect(screen.getByText(/They do not establish which commit GitHub evaluates/)).toBeTruthy();
  expect(screen.queryByRole("navigation", { name: "GitHub result pages" })).toBeNull();
  expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson))).toEqual({ kind: "pull-request", operation: "rules", number: "17" });
});
it("rejects foreign heads, mixed observation families and an empty successful status aggregate", async () => {
  const f = fixture(); const query: GitHubQuery = { kind: ItemKind.PullRequest, operation: QueryOperation.Checks, number: "17", page: 1, page_size: 20 };
  const response = await f.query({ repositoryId: f.repository.id, queryJson: encode(query) }); const value = JSON.parse(new TextDecoder().decode(response.documentJson));expect(githubResult(response.documentJson, f.repository, query)).toBeTruthy();
  expect(githubResult(encode({ ...value, checks: { ...value.checks, head_sha: "c".repeat(40) } }), f.repository, query)).toBeUndefined();
  expect(githubResult(encode({ ...value, diff: {} }), f.repository, query)).toBeUndefined();
  const statuses: GitHubQuery = { ...query, operation: QueryOperation.Statuses };const result = await f.query({ repositoryId: f.repository.id, queryJson: encode(statuses) });const statusValue = JSON.parse(new TextDecoder().decode(result.documentJson));
  expect(githubResult(encode({ ...statusValue, statuses: { ...statusValue.statuses, state: "success", native_state: "success" } }), f.repository, statuses)).toBeUndefined();
});
it("preserves unknown check source values without accepting a known passing classification", async () => {
  const f = fixture(); const query: GitHubQuery = { kind: ItemKind.PullRequest, operation: QueryOperation.Checks, number: "17", page: 1, page_size: 20 };
  const response = await f.query({ repositoryId: f.repository.id, queryJson: encode(query) });const value = JSON.parse(new TextDecoder().decode(response.documentJson));
  value.checks.runs[0].native_status = "constructor";value.checks.runs[0].status = "unknown";value.checks.runs[0].native_conclusion = "future-conclusion";value.checks.runs[0].conclusion = "unknown";
  expect(githubResult(encode(value), f.repository, query)).toBeTruthy();value.checks.runs[0].conclusion = "success";expect(githubResult(encode(value), f.repository, query)).toBeUndefined();
});

it("evaluates required CI separately from the head-only observations", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: "Evaluate required CI" }));
  await screen.findByRole("table", { name: "Active ruleset CI requirements" });
  expect(screen.getByRole("status").textContent).toBe("Terminal required CI failure");
  expect(screen.queryByRole("navigation", { name: "GitHub result pages" })).toBeNull();
  expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson))).toEqual({ kind: "pull-request", operation: "ci", number: "17" });
});

it("reads published PR feedback explicitly without a partial page control", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read published feedback" }));
  await screen.findByRole("region", { name: "Published PR feedback" });
  expect(screen.getByText("<script>Approved review feedback</script>")).toBeTruthy();
  expect(screen.queryByRole("navigation", { name: "GitHub result pages" })).toBeNull();
  expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson))).toEqual({ kind: "pull-request", operation: "feedback", number: "17" });
});

it("verifies feedback authors with a separate complete observation", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: "Verify feedback authors" }));
  await screen.findByRole("table", { name: "Current feedback author identities and permissions" });
  expect(screen.getByText("No collaborator grant")).toBeTruthy();
  expect(screen.queryByRole("navigation", { name: "GitHub result pages" })).toBeNull();
  expect(JSON.parse(new TextDecoder().decode(f.query.mock.calls.at(-1)![0].queryJson))).toEqual({ kind: "pull-request", operation: "reviewers", number: "17" });
});

const forkSource = () => ({ state: "available", repository: { provider: "github.com", id: "9007199254740993", node_id: "FORK_1", owner: "fixture-author", name: "fork-repo", private: true, default_branch: "main" } });
it.each([
  [undefined, "not recorded in this observation"],
  [{ state: "unavailable" }, "unavailable on GitHub"],
  [forkSource(), "fixture-author/fork-repo"],
])("keeps original fork, deleted source and historical absence distinct (%j)", async (source, expected) => {
  const f = fixture(source); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  await screen.findByText((_, element) => element?.tagName === "P" && Boolean(element.textContent?.startsWith("Source repository:") && element.textContent.includes(expected)));
  if (source && source.state === "available") {
    fireEvent.click(screen.getByText("Source repository identity"));
    expect(screen.getByText("9007199254740993")).toBeTruthy();
    expect(screen.getByText("FORK_1")).toBeTruthy();
  }
});
it("rejects inconsistent source identities and source data outside PR detail", async () => {
  const f = fixture(forkSource()), query: GitHubQuery = { kind: ItemKind.PullRequest, operation: QueryOperation.Detail, number: "17" };
  const response = await f.query({ repositoryId: f.repository.id, queryJson: encode(query) });
  const original = JSON.parse(new TextDecoder().decode(response.documentJson));
  expect(githubResult(response.documentJson, f.repository, query)).toBeTruthy();
  for (const source of [
    { state: "future" }, { state: "available" }, { ...forkSource(), state: "unavailable" },
    ...[{ id: "37" }, { node_id: "R_37" }, { owner: "fixture-owner", name: "repo" }, { id: 9007199254740992 }, { provider: "other" }, { owner: "<script>" }, { name: ".." }, { private: null }, { default_branch: "main\nother" }].map((change) => ({ state: "available", repository: { ...forkSource().repository, ...change } })),
  ]) {
    const value = structuredClone(original); value.items[0].head_repository = source;
    expect(githubResult(encode(value), f.repository, query), JSON.stringify(source)).toBeUndefined();
  }
  for (const other of [{ kind: ItemKind.Issue, operation: QueryOperation.Detail, number: "17" }, { kind: ItemKind.PullRequest, operation: QueryOperation.List, page: 1, page_size: 20 }] satisfies GitHubQuery[]) {
    const read = await f.query({ repositoryId: f.repository.id, queryJson: encode(other) });
    const value = JSON.parse(new TextDecoder().decode(read.documentJson)); value.items[0].head_repository = forkSource();
    expect(githubResult(encode(value), f.repository, other)).toBeUndefined();
  }
});


it("retains accepted rows after a failed append and explicitly retries the same numeric page", async () => {
  const f = fixture(), original = f.query.getMockImplementation()!;
  let failed = false;
  f.query.mockImplementation(async request => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (query.page === 2 && !failed) { failed = true; throw new ConnectError("Read unavailable", Code.Unavailable); }
    const reply = await original(request), data = JSON.parse(new TextDecoder().decode(reply.documentJson));
    if (query.page === 2) data.items = [{ ...data.items[0], id: "9007199254740994", node_id: "ITEM_18", number: "18", title: "Later fixture title", url: "https://github.com/fixture-owner/repo/pull/18" }];
    return { ...reply, documentJson: encode(data) };
  });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" })); await screen.findByRole("button", { name: "Read #17" });
  fireEvent.click(screen.getByRole("button", { name: "Load more GitHub query results" })); await screen.findByRole("button", { name: "Retry" });
  expect(screen.getByRole("button", { name: "Read #17" })).toBeTruthy(); expect(f.query).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: "Retry" })); await screen.findByRole("button", { name: "Read #18" });
  expect(f.query.mock.calls[2][0]).toEqual(f.query.mock.calls[1][0]);
  expect(screen.getByRole("button", { name: "Read #17" })).toBeTruthy();
});

it.each(["checks", "statuses"])("appends %s observations and deduplicates their IDs instead of the repeated PR envelope", async operation => {
  const f = fixture(), original = f.query.getMockImplementation()!;
  f.query.mockImplementation(async request => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson)), reply = await original(request);
    if (query.operation !== operation) return reply;
    const data = JSON.parse(new TextDecoder().decode(reply.documentJson));
    if (operation === "checks") {
      const first = data.checks.runs[0]; data.checks.total_count = "2";
      data.checks.runs = query.page === 1 ? [first] : [first, { ...first, id: "54", node_id: "CHECK_54", name: "Later observed check" }];
    } else {
      const first = { id: "61", node_id: "STATUS_61", context: "First observed status", native_state: "pending", state: "pending", created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-28T00:00:00Z" };
      data.statuses.total_count = "2";
      data.statuses.contexts = query.page === 1 ? [first] : [first, { ...first, id: "62", node_id: "STATUS_62", context: "Later observed status" }];
    }
    if (query.page === 1) data.next_page = 2;
    return { ...reply, documentJson: encode(data) };
  });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" })); fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
  fireEvent.click(await screen.findByRole("button", { name: operation === "checks" ? "Read PR checks" : "Read PR commit statuses" }));
  await screen.findByRole("table"); fireEvent.click(screen.getByRole("button", { name: "Load more GitHub query results" }));
  await screen.findByText(operation === "checks" ? "Later observed check" : "Later observed status");
  expect(screen.getAllByText(operation === "checks" ? "Fixture Check" : "First observed status")).toHaveLength(1);
});
