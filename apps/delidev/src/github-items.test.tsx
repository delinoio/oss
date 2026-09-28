import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { RepositoryGitHubItems } from "./github-items";
import { encode } from "./documents";

function fixture() {
  const repository = create(ResourceSchema, { id: newRequestId(), revision: 9007199254740993n, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode({ integration_id: newRequestId(), github_owner: "fixture-owner", github_name: "repo" }) });
  const profile = JSON.parse(new TextDecoder().decode(repository.documentJson)).integration_id as string;
  const generation = newRequestId();
  const query = vi.fn(async (request: { repositoryId: string; queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson)), detail = q.operation === "detail", search = q.operation === "search", pr = q.kind === "pull-request";
    const item = { provider: "github.com", kind: q.kind, identity_source: pr && !search ? "pull-request-api" : "issue-api", id: "9007199254740993", node_id: "ITEM_17", number: "17", title: "Original fixture title", state: "open", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-28T00:00:00Z", author: { id: "19", node_id: "U_19", login: "fixture-author", kind: "user", provider_type: "User" }, url: `https://github.com/fixture-owner/repo/${pr ? "pull" : "issues"}/17`, ...(pr ? { draft: false } : {}), ...(detail ? { body: "<script>never executed</script>\nOriginal body", ...(pr ? { merged: false, base_ref: "main", base_sha: "a".repeat(40), head_ref: "feature", head_sha: "b".repeat(40) } : {}) } : {}) };
    return { schemaVersion: 1, documentJson: encode({ repository_id: repository.id, repository_revision: repository.revision.toString(), profile_id: profile, generation_id: generation, observed_at: "2026-09-28T00:00:00Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: "fixture-owner", name: "repo", private: true }, query: q, items: [item], ...(search ? { total_count: "1001", incomplete: true } : {}), ...(!detail && q.page === 1 ? { next_page: 2 } : {}) }) };
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
