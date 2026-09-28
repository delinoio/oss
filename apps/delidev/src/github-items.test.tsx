import { createHash } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { RepositoryGitHubItems, githubResult } from "./github-items";
import { encode } from "./documents";
import { ItemKind, QueryOperation, type GitHubQuery } from "./github-query-model";

function fixture() {
  const repository = create(ResourceSchema, { id: newRequestId(), revision: 9007199254740993n, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode({ integration_id: newRequestId(), github_owner: "fixture-owner", github_name: "repo" }) });
  const profile = JSON.parse(new TextDecoder().decode(repository.documentJson)).integration_id as string;
  const generation = newRequestId();
  const query = vi.fn(async (request: { repositoryId: string; queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson)), detail = ["detail", "diff", "checks", "statuses"].includes(q.operation), search = q.operation === "search", pr = q.kind === "pull-request";
    const item = { provider: "github.com", kind: q.kind, identity_source: pr && !search ? "pull-request-api" : "issue-api", id: "9007199254740993", node_id: "ITEM_17", number: "17", title: "Original fixture title", state: "open", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-28T00:00:00Z", author: { id: "19", node_id: "U_19", login: "fixture-author", kind: "user", provider_type: "User" }, url: `https://github.com/fixture-owner/repo/${pr ? "pull" : "issues"}/17`, ...(pr ? { draft: false } : {}), ...(detail ? { body: "<script>never executed</script>\nOriginal body", ...(pr ? { merged: false, base_ref: "main", base_sha: "a".repeat(40), head_ref: "feature", head_sha: "b".repeat(40) } : {}) } : {}) };
    const patch = "diff --git a/file b/file\n+Original patch\n";
    const observation = q.operation === "diff" ? { diff: { patch, digest: createHash("sha256").update(patch).digest("hex"), base_sha: "a".repeat(40), head_sha: "b".repeat(40) } } : q.operation === "checks" ? { checks: { head_sha: "b".repeat(40), filter: "latest", total_count: "1", runs: [{ id: "53", node_id: "CHECK_53", name: "Fixture Check", head_sha: "b".repeat(40), status: "completed", native_status: "completed", conclusion: "success", native_conclusion: "success", application: { id: "15368", node_id: "APP_15368", slug: "github-actions" } }] } } : q.operation === "statuses" ? { statuses: { head_sha: "b".repeat(40), state: "pending", native_state: "pending", total_count: "0", contexts: [] } } : {};
    return { schemaVersion: 1, documentJson: encode({ ...observation, repository_id: repository.id, repository_revision: repository.revision.toString(), profile_id: profile, generation_id: generation, observed_at: "2026-09-28T00:00:00Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: "fixture-owner", name: "repo", private: true }, query: q, items: [item], ...(search ? { total_count: "1001", incomplete: true } : {}), ...(!detail && q.page === 1 ? { next_page: 2 } : {}) }) };
  });
  const transport = createRouterTransport((router) => router.service(IntegrationService, { queryRepositoryIntegration: query }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><RepositoryGitHubItems selected={repository} active={active} /></QueryClientProvider></TransportProvider>;
  return { repository, query, client, view };
}
it("opens explicit repository results and reads detail without inventing mergeability", async () => {
  const f = fixture(); const view = render(f.view());
  expect(f.query).not.toHaveBeenCalled(); fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  await screen.findByRole("button", { name: "Read #17" });
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
  fireEvent.click(screen.getByRole("button", { name: "Next GitHub page" }));
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
