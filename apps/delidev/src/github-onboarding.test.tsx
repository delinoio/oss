// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, GitHubTokenIdentityState as State, GitHubTokenKind, IntegrationService, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type InspectGitHubTokenRequest, type SaveIntegrationProfileRequest, type ReplaceIntegrationTokenRequest } from "@delinoio/delidev-api-client";
import { Integrations } from "./integrations";
import { App } from "./App";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { githubDraftTokenFormURL } from "./github-opening";

const native = vi.hoisted(() => ({ invoke: vi.fn(async (_command: string, _args: unknown) => undefined), isTauri: () => true }));
vi.mock("@tauri-apps/api/core", () => native);

function fixture() {
  native.invoke.mockClear();
  const id = newRequestId();
  let profile = create(ResourceSchema, { id, kind: EntityKind.INTEGRATION, schemaVersion: 1, revision: 1n });
  let saved = false;
  const inspect = vi.fn(async (request: InspectGitHubTokenRequest): Promise<{ requestId: string; state: State; identity?: { id: string; nodeId: string; login: string }; problemJson: Uint8Array }> => ({ requestId: request.requestId, state: State.VERIFIED, identity: { id: "17", nodeId: "U_17", login: "fixture-user" }, problemJson: new Uint8Array() }));
  const save = vi.fn(async (request: SaveIntegrationProfileRequest) => { saved = true; profile = create(ResourceSchema, { ...profile, documentJson: request.documentJson }); return { requestId: request.mutation!.requestId, profile }; });
  const replace = vi.fn(async (request: ReplaceIntegrationTokenRequest) => ({ requestId: request.mutation!.requestId, profile, problemJson: new Uint8Array() }));
  const status = vi.fn(async () => ({ capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] }));
  const router = createRouterTransport(r => {
    r.service(SystemService, { getStatus: status });
    r.service(ResourceService, { listResources: () => ({ resources: saved ? [profile] : [] }), getResource: () => ({ resource: profile }) });
    r.service(IntegrationService, {
      inspectGitHubToken: inspect, saveIntegrationProfile: save, replaceIntegrationToken: replace,
      prepareGitHubTokenForm: request => ({ requestId: request.requestId, tokenKind: request.tokenKind, resourceOwner: request.resourceOwner, access: request.access, url: githubDraftTokenFormURL(request.tokenKind === GitHubTokenKind.FINE_GRAINED ? "fine-grained" : "classic", request.resourceOwner, request.access as 1 | 2 | 3)! }),
    });
  });
  const buffers: Uint8Array[] = [];
  const transport: Transport = { ...router, unary(method, signal, timeout, header, input, values) {
    const token = (input as { token?: Uint8Array }).token;
    if (token) buffers.push(token);
    return router.unary(method, signal, timeout, header, input, values);
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Integrations active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  const enter = async () => { fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" })); return await screen.findByLabelText("GitHub personal access token") as HTMLInputElement; };
  const verify = async () => { fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Verify token" })); return await screen.findByLabelText("Profile name") as HTMLInputElement; };
  const owner = () => fireEvent.change(screen.getByLabelText("Resource owner"), { target: { value: "example-org" } });
  const erased = () => { for (const bytes of buffers) expect([...bytes]).toEqual(Array(bytes.length).fill(0)); };
  return { inspect, save, replace, status, transport, client, view, enter, verify, owner, erased, buffers };
}

it("retains a verified draft and focus across failed support reads and explicit read recovery", async () => {
  const f = fixture(); render(<StrictMode>{f.view()}</StrictMode>); await f.enter(); const name = await f.verify(); f.owner();
  fireEvent.change(name, { target: { value: "Retained team" } });
  const owner = screen.getByLabelText("Resource owner") as HTMLInputElement;
  owner.focus();
  f.status.mockRejectedValue(new ConnectError("private-support-text", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByRole("button", { name: "Retry current read" });
  expect(screen.getByLabelText("Profile name")).toBe(name); expect(name.value).toBe("Retained team");
  expect(owner.value).toBe("example-org"); expect(document.activeElement).toBe(owner);
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
  expect(screen.queryByText("private-support-text")).toBeNull();
  expect((screen.getByRole("button", { name: "Save and connect" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.save).not.toHaveBeenCalled(); expect(f.inspect).toHaveBeenCalledTimes(1);
  let release!: () => void;
  f.status.mockImplementation(async () => { await new Promise<void>(resolve => { release = resolve; }); return { capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] }; });
  fireEvent.click(screen.getByRole("button", { name: "Retry current read" }));
  await waitFor(() => expect(release).toBeTypeOf("function")); owner.focus();
  await act(async () => { release(); });
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry current read" })).toBeNull());
  expect(screen.getByLabelText("Profile name")).toBe(name); expect(document.activeElement).toBe(owner);
  expect((screen.getByRole("button", { name: "Save and connect" }) as HTMLButtonElement).disabled).toBe(false);
  expect(f.save).not.toHaveBeenCalled(); expect(f.replace).not.toHaveBeenCalled(); expect(f.inspect).toHaveBeenCalledTimes(1);
});

it("keeps one pending save through failed and changed capability observations", async () => {
  const f = fixture(); let release!: () => void; const save = f.save.getMockImplementation()!;
  f.save.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return save(request); });
  render(f.view()); await f.enter(); await f.verify(); f.owner();
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1)); const original = f.save.mock.calls[0][0];
  f.status.mockRejectedValue(new ConnectError("Lost status", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByRole("button", { name: "Retry current read" });
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
  f.status.mockResolvedValue({ capabilities: [] });
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  expect(screen.getByRole("button", { name: "Save and connect" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
  await act(async () => { release(); }); await screen.findByRole("button", { name: "Manage fixture-user" });
  expect(f.save).toHaveBeenCalledTimes(1); expect(f.save.mock.calls[0][0]).toEqual(original);
  expect(f.replace).toHaveBeenCalledTimes(1); f.erased();
});

it("disposes a pending save on dialog close without late token work or abandoned retry", async () => {
  const f = fixture(); let release!: () => void; const save = f.save.getMockImplementation()!;
  f.save.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return save(request); });
  render(f.view()); await f.enter(); await f.verify(); f.owner();
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  f.status.mockRejectedValue(new ConnectError("Lost status", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByRole("button", { name: "Retry current read" });
  fireEvent.click(screen.getByRole("button", { name: "Close New GitHub profile" }));
  expect(screen.queryByRole("button", { name: "Save and connect" })).toBeNull(); f.erased();
  await act(async () => { release(); });
  expect(f.save).toHaveBeenCalledTimes(1); expect(f.replace).not.toHaveBeenCalled();
  f.status.mockResolvedValue({ capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] });
  const token = await f.enter(); expect(token.value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same profile save" })).toBeNull();
  expect(screen.queryByLabelText("Profile name")).toBeNull();
});

it("retries only the original uncertain save despite failed or unsupported capability reads", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Lost save", Code.Unavailable));
  render(f.view()); await f.enter(); const name = await f.verify(); f.owner();
  fireEvent.change(name, { target: { value: "Original uncertain team" } });
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" }));
  await screen.findByRole("button", { name: "Retry the same profile save" }); const original = f.save.mock.calls[0][0];
  const requestId = original.mutation!.requestId, bytes = original.documentJson.slice(); f.erased();
  f.status.mockRejectedValue(new ConnectError("Lost status", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  expect(screen.getByRole("button", { name: "Retry the same profile save" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
  f.status.mockResolvedValue({ capabilities: [] });
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same profile save" }));
  await screen.findByRole("heading", { name: "Connect a token" });
  expect(f.save).toHaveBeenCalledTimes(2); expect(f.save.mock.calls[1][0]).toEqual(original);
  expect(f.save.mock.calls[1][0].mutation!.requestId).toBe(requestId);
  expect(f.save.mock.calls[1][0].mutation!.expectedRevision).toBe(0n);
  expect(f.save.mock.calls[1][0].documentJson).toEqual(bytes); expect(f.replace).not.toHaveBeenCalled();
  expect((screen.getByLabelText("GitHub personal access token") as HTMLInputElement).value).toBe("");
});

it.each([
  [], [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1, SystemCapability.GITHUB_TOKEN_ONBOARDING_V1],
  [999 as SystemCapability], [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1, 999 as SystemCapability],
  [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1, SystemCapability.UNSPECIFIED], undefined,
])("retains the initial metadata fallback across subsequent reads: %j", async capabilities => {
  const f = fixture();
  if (capabilities) f.status.mockResolvedValue({ capabilities }); else f.status.mockRejectedValue(new ConnectError("Initial status failure", Code.Unavailable));
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  const name = await screen.findByLabelText("Profile name") as HTMLInputElement;
  fireEvent.change(name, { target: { value: "Legacy draft" } }); f.owner();
  expect(screen.queryByLabelText("GitHub personal access token")).toBeNull(); expect(f.inspect).not.toHaveBeenCalled();
  f.status.mockRejectedValue(new ConnectError("Refresh failure", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  expect(screen.getByLabelText("Profile name")).toBe(name); expect(name.value).toBe("Legacy draft");
  f.save.mockRejectedValueOnce(new ConnectError("Lost legacy save", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByRole("button", { name: "Retry the same profile save" }); const original = f.save.mock.calls[0][0];
  f.status.mockResolvedValue({ capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] });
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  expect(screen.getByLabelText("Profile name")).toBe(name); expect(screen.queryByLabelText("GitHub personal access token")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Retry the same profile save" }));
  await screen.findByRole("button", { name: "Manage Legacy draft" });
  expect(f.save.mock.calls[1][0]).toEqual(original); expect(f.inspect).not.toHaveBeenCalled(); expect(f.replace).not.toHaveBeenCalled();
});

it("does not inspect a new token after support becomes unavailable", async () => {
  const f = fixture(); render(f.view()); const token = await f.enter();
  fireEvent.change(token, { target: { value: "fixture-pat" } });
  f.status.mockRejectedValue(new ConnectError("Support read failed", Code.Unavailable));
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  expect(screen.getByLabelText("GitHub personal access token")).toBe(token); expect(token.value).toBe("fixture-pat");
  await waitFor(() => expect((screen.getByRole("button", { name: "Verify token" }) as HTMLButtonElement).disabled).toBe(true));
  fireEvent.submit(token.closest("form")!); expect(f.inspect).not.toHaveBeenCalled();
  f.status.mockResolvedValue({ capabilities: [] });
  await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  await waitFor(() => expect((screen.getByRole("button", { name: "Verify token" }) as HTMLButtonElement).disabled).toBe(true));
  fireEvent.submit(token.closest("form")!); expect(f.inspect).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
});

it("retains the opening on same-identity App reconnect and disposes on Settings or identity departure", async () => {
  const f = fixture();
  const view = render(<StrictMode><App transport={f.transport} connectionEpoch={0} /></StrictMode>);
  fireEvent.click(screen.getByRole("button", { name: "Settings" })); fireEvent.click(screen.getByRole("button", { name: "Git Profiles" }));
  await f.enter(); const name = await f.verify(); f.owner();
  fireEvent.change(name, { target: { value: "Reconnect draft" } });
  f.status.mockRejectedValue(new ConnectError("Reconnect read failure", Code.Unavailable));
  const reads = f.status.mock.calls.length;
  view.rerender(<StrictMode><App transport={f.transport} connectionEpoch={1} /></StrictMode>);
  await waitFor(() => expect(f.status.mock.calls.length).toBeGreaterThan(reads));
  await screen.findByRole("button", { name: "Retry current read" });
  expect(screen.getByLabelText("Profile name")).toBe(name); expect(name.value).toBe("Reconnect draft");
  expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close New GitHub profile" }));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" })); f.erased();
  fireEvent.click(screen.getByRole("button", { name: "Settings" })); fireEvent.click(screen.getByRole("button", { name: "Git Profiles" }));
  f.status.mockResolvedValue({ capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] });
  await f.enter(); expect(screen.queryByLabelText("Profile name")).toBeNull();
  const replacement = fixture();
  view.rerender(<StrictMode><App transport={replacement.transport} connectionEpoch={0} /></StrictMode>);
  expect(screen.queryByLabelText("GitHub personal access token")).toBeNull();
  expect(f.save).not.toHaveBeenCalled(); expect(replacement.inspect).not.toHaveBeenCalled();
}, 60_000);

it("focuses token first, verifies without an owner, then saves the explicit organization and editable name", async () => {
  const f = fixture(); render(f.view()); const token = await f.enter();
  expect(document.activeElement).toBe(token); expect(token.type).toBe("password"); expect(token.autocomplete).toBe("off");
  expect(screen.queryByLabelText("Resource owner")).toBeNull(); expect(screen.queryByLabelText("Token type")).toBeNull();
  expect(screen.getByText("Create a token on GitHub").closest("details")).toBeNull();
  expect(screen.getByRole("button", { name: "Classic" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Fine grained" })).toBeTruthy();
  expect(screen.queryByLabelText("Profile name")).toBeNull(); expect(f.inspect).not.toHaveBeenCalled();
  const name = await f.verify(); expect(name.value).toBe("fixture-user"); await waitFor(() => expect(document.activeElement).toBe(name));
  expect((screen.getByLabelText("Resource owner") as HTMLInputElement).value).toBe("");
  expect((screen.getByRole("button", { name: "Save and connect" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.save).not.toHaveBeenCalled(); f.erased();
  fireEvent.change(name, { target: { value: "Team" } }); f.owner();
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" }));
  await screen.findByRole("button", { name: "Manage Team" });
  expect(f.save).toHaveBeenCalledTimes(1); expect(f.replace).toHaveBeenCalledTimes(1);
  expect(JSON.parse(new TextDecoder().decode(f.save.mock.calls[0][0].documentJson))).toEqual({ name: "Team", provider: "github.com", token_kind: "fine-grained", resource_owner: "example-org" });
  expect(f.replace.mock.calls[0][0].mutation).toMatchObject({ expectedRevision: 1n });
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "New GitHub profile" })); f.erased();
  expect(f.client.getMutationCache().getAll()).toHaveLength(0);
  expect(JSON.stringify(f.client.getQueryCache().getAll().map(q => [q.queryKey, q.state.data]), (_, v) => typeof v === "bigint" ? String(v) : v)).not.toContain("fixture-pat");
});
it("carries declared type/owner and preserves a manually edited name through Back and another verification", async () => {
  const f = fixture(); render(f.view()); await f.enter();
  fireEvent.click(screen.getByRole("button", { name: "Classic" }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  const name = await f.verify();
  expect((screen.getByLabelText("Token type") as HTMLSelectElement).value).toBe(String(GitHubTokenKind.CLASSIC));
  expect((screen.getByLabelText("Resource owner") as HTMLInputElement).value).toBe(""); f.owner();
  expect((screen.getByLabelText("Resource owner") as HTMLInputElement).value).toBe("example-org");
  fireEvent.change(name, { target: { value: "My alias" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect((screen.getByLabelText("GitHub personal access token") as HTMLInputElement).value).toBe(""); f.erased();
  f.inspect.mockImplementationOnce(async r => ({ requestId: r.requestId, state: State.VERIFIED, identity: { id: "18", nodeId: "U_18", login: "other-user" }, problemJson: new Uint8Array() }));
  expect((await f.verify()).value).toBe("My alias");
  expect((screen.getByLabelText("Resource owner") as HTMLInputElement).value).toBe("example-org"); expect(f.save).not.toHaveBeenCalled();
});
it.each([State.INVALID_TOKEN, State.ACCESS_RESTRICTED, State.SSO_REQUIRED, State.RATE_LIMITED, State.UNAVAILABLE])("clears rejected tokens and prevents progression for identity state %s", async state => {
  const f = fixture(); f.inspect.mockImplementationOnce(async r => ({ requestId: r.requestId, state, identity: undefined, problemJson: encode({ message: "Safe fixture failure" }) }));
  render(f.view()); const token = await f.enter();
  fireEvent.change(token, { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  await screen.findByRole("alert"); expect(token.value).toBe(""); expect(screen.queryByLabelText("Profile name")).toBeNull();
  expect((screen.getByRole("button", { name: "Verify token" }) as HTMLButtonElement).disabled).toBe(true); expect(f.save).not.toHaveBeenCalled(); f.erased();
});
it.each(["foreign-request", "zero-id", "unknown-state", "mixed-success"])("rejects malformed inspection response %s", async bad => {
  const f = fixture(); const original = f.inspect.getMockImplementation()!;
  f.inspect.mockImplementationOnce(async r => { const result = await original(r); if (bad === "foreign-request") result.requestId = newRequestId(); if (bad === "zero-id") result.identity!.id = "0"; if (bad === "unknown-state") result.state = 99 as State; if (bad === "mixed-success") result.problemJson = encode({ message: "Mixed" }); return result; });
  render(f.view()); await f.enter();
  fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  await screen.findByRole("alert"); expect(screen.queryByLabelText("Profile name")).toBeNull(); f.erased();
});
it("discards canceled and late inspection responses on departure", async () => {
  const f = fixture(); let release!: () => void; const original = f.inspect.getMockImplementation()!;
  f.inspect.mockImplementationOnce(async r => { await new Promise<void>(resolve => { release = resolve; }); return original(r); });
  const view = render(f.view()); await f.enter();
  fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  await waitFor(() => expect(f.inspect).toHaveBeenCalledTimes(1)); view.rerender(f.view(false)); f.erased();
  await act(async () => { release(); }); view.rerender(f.view(true));
  expect(screen.queryByLabelText("Profile name")).toBeNull(); expect(screen.queryByLabelText("GitHub personal access token")).toBeNull(); expect(f.save).not.toHaveBeenCalled();
});
it("clears network failures and requires explicit token reentry", async () => {
  const f = fixture(); f.inspect.mockRejectedValueOnce(new ConnectError("private-network-text", Code.Unavailable)); render(f.view()); await f.enter();
  fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  await screen.findByRole("alert"); expect(screen.queryByText("private-network-text")).toBeNull(); expect(f.inspect).toHaveBeenCalledTimes(1); f.erased();
});
it("reconciles an uncertain metadata save with the exact original ID without retransmitting the cleared token", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable)); render(f.view()); await f.enter(); await f.verify(); f.owner();
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" })); await screen.findByRole("button", { name: "Retry the same profile save" }); f.erased();
  const original = f.save.mock.calls[0][0]; expect(f.replace).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Retry the same profile save" })); await screen.findByRole("heading", { name: "Connect a token" });
  expect(f.save.mock.calls[1][0]).toEqual(original); expect(f.replace).not.toHaveBeenCalled();
  expect((screen.getByLabelText("GitHub personal access token") as HTMLInputElement).value).toBe("");
  expect(screen.getByRole("alert").textContent).toContain("Reenter your token");
});
it("keeps a created profile after uncertain token replacement and retries only after explicit reentry", async () => {
  const f = fixture(); f.replace.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable)); render(f.view()); await f.enter(); await f.verify(); f.owner();
  fireEvent.click(screen.getByRole("button", { name: "Save and connect" })); await screen.findByRole("button", { name: "Retry original token replacement" }); f.erased();
  const original = f.replace.mock.calls[0][0].mutation; expect(f.save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry original token replacement" })); expect(f.replace).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByLabelText("GitHub personal access token"), { target: { value: "fixture-pat" } }); fireEvent.click(screen.getByRole("button", { name: "Retry original token replacement" }));
  await waitFor(() => expect(f.replace).toHaveBeenCalledTimes(2)); expect(f.replace.mock.calls[1][0].mutation).toEqual(original); expect(f.save).toHaveBeenCalledTimes(1); await waitFor(f.erased);
});
