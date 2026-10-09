import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, GitHubTokenAccess, GitHubTokenKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { GitHubDraftTokenForm, GitHubTokenForm, OpenGitHub, githubDraftTokenFormURL, githubTokenFormURL } from "./github-opening";
import { i18n, SupportedLanguage } from "./localization";

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
  const changeKind = vi.fn();
  const form = vi.fn(async (r: { requestId: string; tokenKind: GitHubTokenKind; resourceOwner: string; access: GitHubTokenAccess }) => ({ ...r, url: githubDraftTokenFormURL(r.tokenKind === GitHubTokenKind.FINE_GRAINED ? "fine-grained" : "classic", r.resourceOwner, r.access as 1 | 2 | 3)! }));
  const transport = createRouterTransport(router => router.service(IntegrationService, { prepareGitHubTokenForm: form }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true, disabled = false) => <TransportProvider transport={transport}><QueryClientProvider client={client}><GitHubDraftTokenForm changeKind={changeKind} active={active} disabled={disabled} /></QueryClientProvider></TransportProvider>;
  return { form, client, view, changeKind };
}
it("shows two static draft buttons and prepares an owner-free fine-grained form only on click", async () => {
  const f = draftFixture(); render(f.view());
  expect(screen.getAllByRole("button").map(button => button.textContent)).toEqual(["Classic", "Fine grained"]);
  expect(screen.queryByRole("textbox")).toBeNull(); expect(screen.queryByRole("combobox")).toBeNull();
  expect(screen.getByText("Create a token on GitHub").closest("details")).toBeNull();
  expect(screen.getByText(/defaults to All repositories/)).toBeTruthy();
  expect(screen.getByText(/no Checks permission/)).toBeTruthy(); expect(f.form).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Fine grained" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  expect(f.form.mock.calls[0][0]).toMatchObject({ tokenKind: GitHubTokenKind.FINE_GRAINED, resourceOwner: "", access: GitHubTokenAccess.SELECTED_REPOSITORIES });
  expect(f.changeKind).toHaveBeenCalledWith(GitHubTokenKind.FINE_GRAINED);
  const url = new URL((native.invoke.mock.calls[0][1] as { url: string }).url);
  expect(url.pathname).toBe("/settings/personal-access-tokens/new");
  expect(url.searchParams.has("target_name")).toBe(false); expect(url.searchParams.get("expires_in")).toBe("30");
  for (const key of ["metadata", "contents", "pull_requests", "issues", "statuses"]) expect(url.searchParams.get(key)).toBe("read");
  expect(url.searchParams.has("checks")).toBe(false); expect(url.searchParams.has("repositories")).toBe(false);
  expect(githubTokenFormURL("fine-grained", "", GitHubTokenAccess.SELECTED_REPOSITORIES)).toBeUndefined();
});
it("opens Classic without scopes and keeps private repo guidance visible", async () => {
  const f = draftFixture(); render(f.view());
  expect(screen.getByText(/broad read\/write access/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Classic" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  expect(f.form.mock.calls[0][0]).toMatchObject({ tokenKind: GitHubTokenKind.CLASSIC, resourceOwner: "", access: GitHubTokenAccess.PUBLIC_REPOSITORIES });
  expect(f.changeKind).toHaveBeenCalledWith(GitHubTokenKind.CLASSIC);
  expect((native.invoke.mock.calls[0][1] as { url: string }).url).toBe("https://github.com/settings/tokens/new?description=DeliDev+public+read-only");
  await waitFor(() => expect((screen.getByRole("button", { name: "Fine grained" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Fine grained" })); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(2));
  expect(f.form.mock.calls[1][0].tokenKind).toBe(GitHubTokenKind.FINE_GRAINED);
});
it.each(["url", "requestId", "resourceOwner", "tokenKind", "access"])("rejects a mismatched draft form %s before browser dispatch", async key => {
  const f = draftFixture(), original = f.form.getMockImplementation()!;
  f.form.mockImplementationOnce(async r => { const result = await original(r); return { ...result, [key]: key === "url" ? result.url + "&checks=write" : key === "access" || key === "tokenKind" ? 99 : "wrong" }; });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Fine grained" })); await screen.findByText(/official form could not be confirmed/); expect(native.invoke).not.toHaveBeenCalled();
});
it.each(["disabled", "departure", "unmount"])("discards a delayed draft form after %s", async change => {
  const f = draftFixture(), original = f.form.getMockImplementation()!; let release!: () => void;
  f.form.mockImplementationOnce(async r => { await new Promise<void>(resolve => { release = resolve; }); return original(r); });
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Fine grained" })); await waitFor(() => expect(f.form).toHaveBeenCalledTimes(1));
  if (change === "disabled") view.rerender(f.view(true, true)); else if (change === "unmount") view.unmount(); else view.rerender(f.view(false));
  release(); await waitFor(() => expect(f.client.isFetching()).toBe(0)); expect(native.invoke).not.toHaveBeenCalled();
});
it("locks both buttons through preparation and native dispatch without duplicate clicks", async () => {
  const f = draftFixture(), original = f.form.getMockImplementation()!;
  let prepared!: () => void, dispatched!: () => void;
  f.form.mockImplementationOnce(async r => { await new Promise<void>(resolve => { prepared = resolve; }); return original(r); });
  native.invoke.mockImplementationOnce(async () => { await new Promise<void>(resolve => { dispatched = resolve; }); });
  render(f.view()); const fine = screen.getByRole("button", { name: "Fine grained" }), classic = screen.getByRole("button", { name: "Classic" });
  fireEvent.click(fine); fireEvent.click(fine); fireEvent.click(classic);
  await waitFor(() => expect(f.form).toHaveBeenCalledTimes(1));
  expect((fine as HTMLButtonElement).disabled).toBe(true); expect((classic as HTMLButtonElement).disabled).toBe(true);
  prepared(); await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  fireEvent.click(classic); expect(f.form).toHaveBeenCalledTimes(1);
  dispatched(); await screen.findByText(/Sent to your default browser/);
  await waitFor(() => expect((classic as HTMLButtonElement).disabled).toBe(false));
});
it("does not retry a failed draft dispatch or expose native diagnostics", async () => {
  const f = draftFixture(); native.invoke.mockRejectedValueOnce(new Error("private-native-diagnostic")); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Classic" }));
  await screen.findByText(/official form could not be confirmed/);
  expect(f.form).toHaveBeenCalledTimes(1); expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("private-native-diagnostic")).toBeNull();
});
it("translates a pending draft without repeating preparation or changing its chosen kind", async () => {
  const f = draftFixture(), original = f.form.getMockImplementation()!; let release!: () => void;
  f.form.mockImplementationOnce(async r => { await new Promise<void>(resolve => { release = resolve; }); return original(r); });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Fine grained" }));
  await waitFor(() => expect(f.form).toHaveBeenCalledTimes(1));
  try {
    await act(async () => { await i18n.changeLanguage(SupportedLanguage.Korean); });
    expect(screen.getByText("공식 GitHub 토큰 양식을 준비하는 중…")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Classic" })).toBeTruthy();
    expect(screen.getByText(/공식 양식의 기본값은 모든 저장소입니다/)).toBeTruthy();
    expect(f.form).toHaveBeenCalledTimes(1); expect(f.changeKind).toHaveBeenCalledTimes(1);
    release(); await screen.findByText(/기본 브라우저로 전달했습니다/);
    expect(native.invoke).toHaveBeenCalledTimes(1); expect(f.form).toHaveBeenCalledTimes(1);
  } finally { await act(async () => { await i18n.changeLanguage(SupportedLanguage.English); }); }
});
