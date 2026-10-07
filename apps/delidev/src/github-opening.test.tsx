import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, GitHubTokenAccess, GitHubTokenKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { GitHubDraftTokenForm, GitHubTokenForm, OpenGitHub, githubTokenFormURL } from "./github-opening";

const native = vi.hoisted(() => ({ invoke: vi.fn(async (_command: string, _args: unknown) => undefined), isTauri: () => true }));
vi.mock("@tauri-apps/api/core", () => native);
function fixture(classic = false, bad = false) {
  native.invoke.mockClear();
  const profile = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name: "Work", provider: "github.com", token_kind: classic ? "classic" : "fine-grained", ...(classic ? {} : { resource_owner: "fixture-owner" }) }) });
  const form = vi.fn(async (request: { access: GitHubTokenAccess; profileId: string; expectedRevision: bigint }) => {
    const q = classic ? new URLSearchParams({ description: request.access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? "DeliDev private repository lookup" : "DeliDev public read-only", ...(request.access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? { scopes: "repo" } : {}) }) : new URLSearchParams({ name: "DeliDev read-only", description: "Read-only repository inspection", target_name: "fixture-owner", expires_in: "30", metadata: "read", contents: "read", pull_requests: "read", issues: "read", statuses: "read" });
    q.sort();
    return { schemaVersion: 1, documentJson: encode({ profile_id: profile.id, profile_revision: profile.revision.toString(), token_kind: classic ? "classic" : "fine-grained", ...(classic ? {} : { resource_owner: "fixture-owner" }), access: classic ? request.access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? "private-repositories" : "public-repositories" : "selected-repositories", url: `https://github.com/settings/${classic ? "tokens" : "personal-access-tokens"}/new?${q}${bad ? "&checks=write" : ""}` }) };
  });
  const transport = createRouterTransport((router) => router.service(IntegrationService, { getGitHubTokenForm: form }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = <TransportProvider transport={transport}><QueryClientProvider client={client}><GitHubTokenForm profile={profile} active disabled={false} /></QueryClientProvider></TransportProvider>;
  return { profile, form, view, client };
}
it("explains selected-repository defaults and opens only a freshly bound official form", async () => {
  const f = fixture(); render(f.view);
  expect(screen.getByText(/defaults to All repositories/)).toBeTruthy();
  expect(screen.getByText(/offers no Checks permission/)).toBeTruthy();
  expect(f.form).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  expect(f.form.mock.calls[0][0]).toMatchObject({ profileId: f.profile.id, expectedRevision: 9007199254740993n, access: GitHubTokenAccess.SELECTED_REPOSITORIES });
  expect(native.invoke.mock.calls[0][0]).toBe("open_github");
  expect((native.invoke.mock.calls[0][1] as { url: string }).url).toContain("contents=read");
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" }));
  await waitFor(() => expect(f.form).toHaveBeenCalledTimes(2));
});
it("requires an explicit classic private choice and explains broad repo rights", async () => {
  const f = fixture(true); render(f.view);
  expect(screen.getByText(/No classic scopes/)).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Classic token access"), { target: { value: GitHubTokenAccess.PRIVATE_REPOSITORIES } });
  expect(screen.getByText(/broad read\/write access/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  expect((native.invoke.mock.calls[0][1] as { url: string }).url).toContain("scopes=repo");
});
it("rejects extra permissions in a form response before native presentation", async () => {
  const f = fixture(false, true); render(f.view);
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" }));
  await screen.findByText(/official form could not be confirmed/);
  expect(native.invoke).not.toHaveBeenCalled();
});
it("does not open a delayed form after its settings panel closes", async () => {
  const f = fixture(); const original = f.form.getMockImplementation()!;
  let release!: () => void;
  f.form.mockImplementationOnce(async (r) => { await new Promise<void>((resolve) => { release = resolve; }); return original(r); });
  const view = render(f.view);
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" }));
  await waitFor(() => expect(f.form).toHaveBeenCalledTimes(1));
  view.unmount(); release();
  await waitFor(() => expect(f.client.isFetching()).toBe(0));
  expect(native.invoke).not.toHaveBeenCalled();
});
it("does not automatically repeat an uncertain native open", async () => {
  native.invoke.mockClear(); native.invoke.mockRejectedValueOnce(new Error("private diagnostic"));
  render(<OpenGitHub url="https://github.com/owner/repo/pull/1" />);
  fireEvent.click(screen.getByRole("button", { name: "Open on GitHub" }));
  await screen.findByText(/Browser opening was not confirmed/);
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("private diagnostic")).toBeNull();
});

function draftFixture() {
  native.invoke.mockClear();
  const form = vi.fn(async (r: { requestId: string; tokenKind: GitHubTokenKind; resourceOwner: string; access: GitHubTokenAccess }) => ({ ...r, url: githubTokenFormURL(r.tokenKind === GitHubTokenKind.FINE_GRAINED ? "fine-grained" : "classic", r.resourceOwner, r.access as 1 | 2 | 3)! }));
  const transport = createRouterTransport(router => router.service(IntegrationService, { prepareGitHubTokenForm: form }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Draft({ active }: { active: boolean }) {
    const [kind, setKind] = useState(GitHubTokenKind.FINE_GRAINED), [owner, setOwner] = useState("");
    return <GitHubDraftTokenForm kind={kind} owner={owner} changeKind={setKind} changeOwner={setOwner} active={active} disabled={false} />;
  }
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Draft active={active} /></QueryClientProvider></TransportProvider>;
  return { form, client, view };
}
it("prepares a draft official form only after explicit owner input and click", async () => {
  const f = draftFixture(); render(f.view());
  expect((screen.getByLabelText("Resource owner") as HTMLInputElement).value).toBe("");
  expect((screen.getByRole("button", { name: "Open official GitHub token form" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Resource owner"), { target: { value: "example-org" } }); expect(f.form).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  const url = new URL((native.invoke.mock.calls[0][1] as { url: string }).url);
  expect(url.searchParams.get("target_name")).toBe("example-org"); expect(url.searchParams.get("expires_in")).toBe("30");
  for (const key of ["metadata", "contents", "pull_requests", "issues", "statuses"]) expect(url.searchParams.get(key)).toBe("read");
  expect(url.searchParams.has("checks")).toBe(false); expect(url.searchParams.has("repositories")).toBe(false);
});
it("keeps draft classic forms public by default and requires a deliberate broad repo selection", async () => {
  const f = draftFixture(); render(f.view()); fireEvent.change(screen.getByLabelText("Token type"), { target: { value: GitHubTokenKind.CLASSIC } });
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  expect((native.invoke.mock.calls[0][1] as { url: string }).url).not.toContain("scopes=");
  fireEvent.change(screen.getByLabelText("Classic token access"), { target: { value: GitHubTokenAccess.PRIVATE_REPOSITORIES } });
  expect(screen.getByText(/broad read\/write access/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(2));
  expect((native.invoke.mock.calls[1][1] as { url: string }).url).toContain("scopes=repo");
});
it.each(["url", "requestId", "resourceOwner", "tokenKind", "access"])("rejects a mismatched draft form %s before browser dispatch", async key => {
  const f = draftFixture(), original = f.form.getMockImplementation()!;
  f.form.mockImplementationOnce(async r => { const result = await original(r); return { ...result, [key]: key === "url" ? result.url + "&checks=write" : key === "access" || key === "tokenKind" ? 99 : "wrong" }; });
  render(f.view()); fireEvent.change(screen.getByLabelText("Resource owner"), { target: { value: "example-org" } });
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" })); await screen.findByText(/official form could not be confirmed/); expect(native.invoke).not.toHaveBeenCalled();
});
it.each(["owner", "departure"])("discards a delayed draft form after %s", async change => {
  const f = draftFixture(), original = f.form.getMockImplementation()!; let release!: () => void;
  f.form.mockImplementationOnce(async r => { await new Promise<void>(resolve => { release = resolve; }); return original(r); });
  const view = render(f.view()); fireEvent.change(screen.getByLabelText("Resource owner"), { target: { value: "example-org" } });
  fireEvent.click(screen.getByRole("button", { name: "Open official GitHub token form" })); await waitFor(() => expect(f.form).toHaveBeenCalledTimes(1));
  if (change === "owner") fireEvent.change(screen.getByLabelText("Resource owner"), { target: { value: "different-org" } }); else view.rerender(f.view(false));
  release(); await waitFor(() => expect(f.client.isFetching()).toBe(0)); expect(native.invoke).not.toHaveBeenCalled();
});
