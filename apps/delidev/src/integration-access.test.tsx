import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { RepositoryGitHubAccess, repositoryAccess } from "./integration-access";
import { encode } from "./documents";

function fixture() {
  const profile = newRequestId(), repository = create(ResourceSchema, { id: newRequestId(), revision: 9007199254740993n, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode({ name: "Workspace", integration_id: profile, github_owner: "fixture-owner", github_name: "repo" }) });
  const features = ["repository-metadata", "repository-contents", "pull-requests", "issues", "checks", "commit-statuses", "rulesets", "reviewer-permissions"];
  const observation = { repository_id: repository.id, repository_revision: repository.revision.toString(), profile_id: profile, generation_id: newRequestId(), observed_at: "2026-09-28T00:00:00Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: "fixture-owner", name: "repo", private: true, default_branch: "main", head_commit: "a".repeat(40) }, features: features.map((feature) => ({ feature, state: feature === "checks" ? "restricted" : "available", ...(feature === "checks" ? { problem: { message: "Checks access is restricted.", guidance: "Check the selected token's access." } } : {}) })) };
  const inspect = vi.fn(async (_request: unknown) => ({ schemaVersion: 1, documentJson: encode(observation) }));
  const transport = createRouterTransport((router) => router.service(IntegrationService, { inspectRepositoryIntegration: inspect }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><RepositoryGitHubAccess selected={repository} active={active} /></QueryClientProvider></TransportProvider>;
  return { repository, observation, inspect, client, view };
}
it("inspects only on explicit opening and separates feature access from CI health", async () => {
  const f = fixture(); const view = render(f.view());
  expect(f.inspect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Inspect GitHub access" }));
  await screen.findByRole("table");
  expect(f.inspect).toHaveBeenCalledTimes(1);
  expect(f.inspect.mock.calls[0][0]).toMatchObject({ repositoryId: f.repository.id });
  expect(screen.getByText(/API availability does not mean CI passed/)).toBeTruthy();
  expect(within(screen.getByRole("row", { name: /Checks API/ })).getByText("restricted")).toBeTruthy();
  expect(within(screen.getByRole("row", { name: /Pull requests/ })).getByText("available")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close GitHub access" }));
  expect(screen.queryByRole("table")).toBeNull();
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(0));
  view.unmount();
});
it("rejects changed scope, rounded revisions, incomplete features and fake availability", () => {
  const f = fixture(); expect(repositoryAccess(encode(f.observation), f.repository)).toBeTruthy();
  const cases = [
    { ...f.observation, repository_id: newRequestId() },
    { ...f.observation, profile_id: newRequestId() },
    { ...f.observation, repository_revision: Number(f.observation.repository_revision) },
    { ...f.observation, repository: { ...f.observation.repository, owner: "other-owner" } },
    { ...f.observation, features: f.observation.features.slice(1) },
    { ...f.observation, features: f.observation.features.map((feature) => feature.feature === "checks" ? { ...feature, state: "available" } : feature) },
    { ...f.observation, repository: { ...f.observation.repository, head_commit: undefined } },
  ];
  for (const value of cases) expect(repositoryAccess(encode(value), f.repository)).toBeUndefined();
});
it("shows a mismatched observation as unavailable instead of rendering partial results", async () => {
  const f = fixture(); f.inspect.mockResolvedValueOnce({ schemaVersion: 1, documentJson: encode({ ...f.observation, profile_id: newRequestId() }) }); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Inspect GitHub access" }));
  await screen.findByRole("alert");
  expect(screen.queryByRole("table")).toBeNull();
});
it("removes inactive observations without making another request", async () => {
  const f = fixture(); const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Inspect GitHub access" })); await screen.findByRole("table");
  view.rerender(f.view(false)); expect(screen.queryByRole("table")).toBeNull();
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(0));
  expect(f.inspect).toHaveBeenCalledTimes(1);
});
