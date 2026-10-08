import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ErrorDetailSchema, IntegrationService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Integrations } from "./integrations";
import { MutationIntents } from "./mutation";
import { document as profileDocument, encode } from "./documents";

function fixture() {
  let profile = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, revision: 3n, schemaVersion: 1, documentJson: encode({ name: "Work", provider: "github.com", token_kind: "fine-grained", resource_owner: "test-owner" }) });
  const save = vi.fn(async (_request: unknown) => ({ profile }));
  const replace = vi.fn(async (_request: unknown) => ({ profile, problemJson: new Uint8Array() }));
  const validate = vi.fn(async (_request: unknown) => ({ profile }));
  const remove = vi.fn(async (_request: unknown) => ({ deleted: true }));
  const list = vi.fn(async (_request: { filter?: { pageSize: number; pageToken: string } }) => ({ resources: [profile], nextPageToken: "" }));
  const form = vi.fn(async (_request: unknown) => ({ schemaVersion: 1, documentJson: new Uint8Array() }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { listResources: list, getResource: () => ({ resource: profile }) });
    router.service(IntegrationService, { saveIntegrationProfile: save, replaceIntegrationToken: replace, validateIntegrationProfile: validate, deleteIntegrationProfile: remove, getGitHubTokenForm: form });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Integrations active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { save, replace, validate, remove, list, form, client, view, profile, update: (value: Resource) => { profile = value; } };
}
it("creates a fine-grained profile without token or fabricated identity fields", async () => {
  const f = fixture(); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  const initialName = await screen.findByRole("textbox", { name: "Profile name" });
  expect(document.activeElement).toBe(initialName);
  expect((screen.getByRole("textbox", { name: "Resource owner" }) as HTMLInputElement).required).toBe(true);
  expect(screen.queryByLabelText("GitHub personal access token")).toBeNull();
  fireEvent.change(screen.getByRole("textbox", { name: "Profile name" }), { target: { value: "Team" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Resource owner" }), { target: { value: "team-owner" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const request = f.save.mock.calls[0][0] as { documentJson: Uint8Array; mutation: { expectedRevision: bigint } };
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "Team", provider: "github.com", token_kind: "fine-grained", resource_owner: "team-owner" });
  expect(request.mutation.expectedRevision).toBe(0n);
});
it("clears PAT input and mutation cache after uncertain transmission and reuses only the original request identity", async () => {
  const f = fixture();
  const buffers: Uint8Array[] = [];
  f.client.getMutationCache().subscribe((event) => {
    if ("mutation" in event && event.mutation) {
      const variables = event.mutation.state.variables as { token?: Uint8Array } | undefined;
      if (variables?.token && !buffers.includes(variables.token)) buffers.push(variables.token);
    }
  });
  f.replace.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable)); render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  const field = screen.getByLabelText("GitHub personal access token") as HTMLInputElement;
  fireEvent.change(field, { target: { value: "fixture-pat" } });
  fireEvent.click(screen.getByRole("button", { name: "Save and validate token" }));
  await screen.findByRole("button", { name: "Retry original token replacement" });
  expect(field.value).toBe("");
  await waitFor(() => expect(f.client.getMutationCache().getAll()).toHaveLength(0));
  expect(JSON.stringify(f.client.getQueryCache().getAll().map((query) => [query.queryKey, query.state.data]), (_, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain("fixture-pat");
  fireEvent.click(screen.getByRole("button", { name: "Retry original token replacement" }));
  expect(f.replace).toHaveBeenCalledTimes(1);
  fireEvent.change(field, { target: { value: "fixture-pat" } });
  fireEvent.click(screen.getByRole("button", { name: "Retry original token replacement" }));
  await waitFor(() => expect(f.replace).toHaveBeenCalledTimes(2));
  const first = f.replace.mock.calls[0][0] as { mutation: unknown; token: Uint8Array };
  const second = f.replace.mock.calls[1][0] as { mutation: unknown; token: Uint8Array };
  expect(second.mutation).toEqual(first.mutation);
  // Router requests are server-side decoded copies. Inspect the frontend's
  // actual mutation variables instead of claiming to erase the receiver copy.
  expect(buffers).toHaveLength(2);
  await waitFor(() => { for (const buffer of buffers) expect([...buffer]).toEqual(Array(buffer.length).fill(0)); });
  await waitFor(() => expect(f.client.getMutationCache().getAll()).toHaveLength(0));
});
it("restores the exact decimal revision for native cleanup and requires explicit deletion", async () => {
  const f = fixture(); const requestId = newRequestId();
  f.update(create(ResourceSchema, { ...f.profile, revision: 9007199254741000n, documentJson: encode({ ...profileDocument(f.profile), pending: { operation: "delete-profile", request_id: requestId, expected_revision: "9007199254740999" } }) }));
  render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  expect(f.remove).not.toHaveBeenCalled();
  expect((screen.getByRole("button", { name: "Validate profile" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Retry original profile deletion" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  expect(f.remove.mock.calls[0][0]).toMatchObject({ mutation: { requestId, expectedRevision: 9007199254740999n } });
});
it("keeps identity distinct from repository access and confirms before deleting", async () => {
  const f = fixture();
  f.update(create(ResourceSchema, { ...f.profile, documentJson: encode({ ...profileDocument(f.profile), connection: { generation_id: newRequestId(), validation: { state: "identity-verified", identity: { id: "17", node_id: "U_17", login: "fixture-user" } } } }) }));
  render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  expect(screen.getByText(/Authenticated as fixture-user/)).toBeTruthy();
  expect(screen.getByText(/Identity validation does not verify access/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Delete profile" }));
  expect(f.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm profile deletion" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
});
it("discards a typed PAT when the settings surface becomes inactive", async () => {
  const f = fixture(); const view = render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  const field = screen.getByLabelText("GitHub personal access token") as HTMLInputElement;
  fireEvent.change(field, { target: { value: "unsent-fixture-pat" } });
  view.rerender(f.view(false));
  expect(field.value).toBe(""); expect(f.replace).not.toHaveBeenCalled();
});

it("distinguishes an initial loading read from a successful empty inventory and keeps one create action", async () => {
  const f = fixture(); let release!: () => void;
  f.list.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); return { resources: [], nextPageToken: "" }; });
  render(f.view());
  expect(screen.getAllByRole("status").some(node => node.textContent === "Loading GitHub profiles…")).toBe(true);
  expect(screen.queryByText("Add your first GitHub profile")).toBeNull();
  expect(screen.getAllByRole("button", { name: "New GitHub profile" })).toHaveLength(1);
  await waitFor(() => expect(f.list).toHaveBeenCalledTimes(1)); release();
  await screen.findByText("Add your first GitHub profile");
  expect(screen.queryByText("Loading GitHub profiles…")).toBeNull();
  expect(screen.queryByRole("navigation", { name: "GitHub profile pages" })).toBeNull();
  expect(screen.getAllByRole("button", { name: "New GitHub profile" })).toHaveLength(1);
  expect(screen.getByRole("list", { name: "GitHub profile setup explanation" }).querySelectorAll("button")).toHaveLength(0);
  expect(screen.getByText("Select it in Repositories")).toBeTruthy();
  expect(screen.getByText(/Tokens are stored in the selected server's OS credential store/)).toBeTruthy();
  expect(screen.queryByLabelText("GitHub personal access token")).toBeNull();
});
it("shows an initial sanitized failure and explicit retry without an empty-success claim or create gate", async () => {
  const f = fixture(); const reference = newRequestId();
  f.list.mockRejectedValueOnce(new ConnectError("Denied", Code.PermissionDenied, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "permission_denied", correlationId: reference, guidance: "Refresh the selected connection." }) }]));
  render(f.view());
  await screen.findByRole("alert");
  expect(screen.queryByText("Add your first GitHub profile")).toBeNull();
  expect(screen.queryByText("No GitHub profiles on this page.")).toBeNull();
  expect(screen.getAllByRole("button", { name: "New GitHub profile" })).toHaveLength(1);
  expect(screen.getByRole("button", { name: "New GitHub profile" }).hasAttribute("disabled")).toBe(false);
  expect(screen.getByRole("alert").textContent).toContain(reference);
  fireEvent.click(screen.getByRole("button", { name: "Refresh GitHub profiles" }));
  await screen.findByRole("button", { name: "Manage Work" });
});
it("retains previous rows during refresh and labels a refresh failure as stale", async () => {
  const f = fixture(); let release!: () => void;
  render(f.view()); await screen.findByRole("button", { name: "Manage Work" });
  f.list.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); throw new ConnectError("Unavailable", Code.Unavailable); });
  fireEvent.click(screen.getByRole("button", { name: "Refresh GitHub profiles" }));
  await screen.findByText(/Refreshing GitHub profiles… Previous results/);
  expect(screen.getByRole("button", { name: "Manage Work" })).toBeTruthy(); release();
  await screen.findByText(/Previous GitHub profile results are stale/);
  expect(screen.getByRole("button", { name: "Manage Work" })).toBeTruthy();
  expect(screen.queryByText("Add your first GitHub profile")).toBeNull();
});
it("keeps an empty page with continuation distinct and uses the original opaque cursors and page size", async () => {
  const f = fixture();
  f.list.mockImplementation(async (request) => ({ resources: [], nextPageToken: request.filter?.pageToken ? "" : "opaque-next" }));
  render(f.view()); await screen.findByText("No GitHub profiles on this page.");
  expect(screen.queryByText("Add your first GitHub profile")).toBeNull();
  expect(screen.getAllByRole("button", { name: "New GitHub profile" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Load more GitHub profile pages" }));
  await waitFor(() => expect(f.list.mock.calls.at(-1)?.[0].filter).toMatchObject({ pageToken: "opaque-next", pageSize: 50 }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more GitHub profile pages" })).toBeNull());
  expect(screen.getByText("Add your first GitHub profile")).toBeTruthy();
  const beforeRefresh = f.list.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Refresh GitHub profiles" }));
  await waitFor(() => expect(f.list.mock.calls.slice(beforeRefresh).some(([request]) => request.filter?.pageToken === "" && request.filter?.pageSize === 50)).toBe(true));
});
it("renders separate storage and identity facts with readable immutable metadata and named actions", async () => {
  const f = fixture();
  const personal = create(ResourceSchema, { ...f.profile, id: newRequestId(), documentJson: encode({ name: "Personal", provider: "github.com", token_kind: "classic" }) });
  const work = create(ResourceSchema, { ...f.profile, documentJson: encode({ ...profileDocument(f.profile), connection: { validation: { state: "identity-verified", checked_at: "2026-09-30T04:00:00Z", identity: { login: "fixture-user", id: "9007199254740993" } } } }) });
  f.update(work); f.list.mockResolvedValue({ resources: [work, personal], nextPageToken: "" }); render(f.view());
  const manage = await screen.findByRole("button", { name: "Manage Work" });
  const workRow = within(manage.closest("article")!);
  expect(workRow.getByText("Fine-grained PAT · test-owner")).toBeTruthy();
  expect(workRow.getByText("Token stored")).toBeTruthy();
  expect(workRow.getByText("Identity verified")).toBeTruthy();
  expect(screen.getByText("Classic PAT · No owner restriction declared")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Rename Personal" }).textContent).toBe("Rename");
  expect(screen.getByText("Identity verification does not confirm repository access.")).toBeTruthy();
  expect(f.validate).not.toHaveBeenCalled(); fireEvent.click(manage);
  expect(screen.getByText(/Authenticated as fixture-user · GitHub ID 9007199254740993/)).toBeTruthy();
  expect(within(screen.getByRole("dialog")).getByTitle("2026-09-30T04:00:00Z")).toBeTruthy();
  expect((screen.getByText("Create a token on GitHub").closest("details") as HTMLDetailsElement).open).toBe(false);
  const input = screen.getByLabelText("GitHub personal access token") as HTMLInputElement;
  expect(input.value).toBe(""); expect(input.placeholder).toBe("Enter a personal access token");
  expect(input.type).toBe("password"); expect(input.autocomplete).toBe("off");
  expect(screen.getByRole("button", { name: "Save and validate token" }).hasAttribute("disabled")).toBe(true);
});
it.each([
  ["invalid-token", "Invalid token"], ["access-restricted", "Access restricted"], ["sso-required", "SSO required"],
  ["rate-limited", "Rate limited"], ["unavailable", "Unavailable"], ["future-state", "Unknown identity observation"],
])("presents the %s observation without claiming repository authority", async (state, label) => {
  const f = fixture(); f.update(create(ResourceSchema, { ...f.profile, documentJson: encode({ ...profileDocument(f.profile), connection: { validation: { state } } }) }));
  render(f.view()); await screen.findByText(label);
  expect(screen.getByText("Token stored")).toBeTruthy();
  expect(screen.queryByText("Identity verified")).toBeNull();
  expect(screen.getByText("Identity verification does not confirm repository access.")).toBeTruthy();
});
it("keeps unknown schema observations explicit and their original action guards disabled", async () => {
  const f = fixture(); f.update(create(ResourceSchema, { ...f.profile, schemaVersion: 2 })); render(f.view());
  await screen.findAllByText("Unsupported profile version");
  expect(screen.getByRole("button", { name: "Manage Unnamed" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("button", { name: "Rename Unnamed" }).hasAttribute("disabled")).toBe(true);
});
it("focuses newly entered editors once, retains drafts through inactivity, and renames only metadata", async () => {
  const f = fixture(); const view = render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: "Rename Work" }));
  const name = screen.getByRole("textbox", { name: "Profile name" }) as HTMLInputElement;
  expect(name).toBe(document.activeElement);
  expect(screen.getByLabelText("Token type").hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("textbox", { name: "Resource owner" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByText("Token type and owner cannot be changed after creation.")).toBeTruthy();
  fireEvent.change(name, { target: { value: "Renamed" } });
  const cancel = screen.getByRole("button", { name: "Close Rename GitHub profile" }); cancel.focus();
  view.rerender(f.view(false)); view.rerender(f.view(true));
  expect(name.value).toBe("Renamed"); expect(document.activeElement).toBe(cancel);
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const request = f.save.mock.calls[0][0] as { documentJson: Uint8Array; mutation: { expectedRevision: bigint } };
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "Renamed", provider: "github.com", token_kind: "fine-grained", resource_owner: "test-owner" });
  expect(request.mutation.expectedRevision).toBe(3n);
  await screen.findByRole("button", { name: "Manage Work" });
  expect(f.replace).not.toHaveBeenCalled(); expect(f.validate).not.toHaveBeenCalled();
});
it("keeps disclosure toggles informational and preserves the Classic choice while collapsed", async () => {
  const f = fixture(); f.update(create(ResourceSchema, { ...f.profile, documentJson: encode({ name: "Work", provider: "github.com", token_kind: "classic" }) }));
  render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  const details = screen.getByText("Create a token on GitHub").closest("details") as HTMLDetailsElement;
  expect(details.open).toBe(true);
  fireEvent.change(screen.getByLabelText("Classic token access"), { target: { value: 3 } });
  expect(screen.getByText(/broad read\/write access/)).toBeTruthy();
  details.open = false; fireEvent(details, new Event("toggle"));
  details.open = true; fireEvent(details, new Event("toggle"));
  expect((screen.getByLabelText("Classic token access") as HTMLSelectElement).value).toBe("3");
  expect(f.form).not.toHaveBeenCalled(); expect(f.replace).not.toHaveBeenCalled(); expect(f.validate).not.toHaveBeenCalled();
});
it("keeps declining deletion free of side effects", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete profile" }));
  fireEvent.click(screen.getByRole("button", { name: "Keep profile" }));
  expect(screen.queryByRole("button", { name: "Confirm profile deletion" })).toBeNull();
  expect(f.remove).not.toHaveBeenCalled();
});

it("retains an uncertain profile save exactly through category inactivity without automatic replay", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable));
  const view = render(f.view()); fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Profile name" }), { target: { value: "Team" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Resource owner" }), { target: { value: "team-owner" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByRole("button", { name: "Retry the same profile save" });
  const original = f.save.mock.calls[0][0];
  expect(screen.getByRole("button", { name: "Save profile" }).hasAttribute("disabled")).toBe(true);
  view.rerender(f.view(false)); view.rerender(f.view(true));
  expect(f.save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same profile save" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2));
  expect(f.save.mock.calls[1][0]).toEqual(original);
});
it("labels pending token denial independently of historical identity verification", async () => {
  const f = fixture(); f.update(create(ResourceSchema, { ...f.profile, documentJson: encode({ ...profileDocument(f.profile), pending: { operation: "replace-token", request_id: newRequestId(), expected_revision: "3" }, connection: { validation: { state: "identity-verified" } } }) }));
  render(f.view()); await screen.findByText("Change pending; token use disabled");
  expect(screen.getByText("Identity verified")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Manage Work" }));
  expect(screen.getByRole("button", { name: "Validate profile" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("button", { name: "Retry original token replacement" }).hasAttribute("disabled")).toBe(true);
});

it.each(["Rename Work", "Manage Work"])("pauses the original profile inventory beneath %s without replacing its row", async action => {
  const f = fixture(); render(f.view());
  const opener = await screen.findByRole("button", { name: action });
  const row = opener.closest("article")!;
  fireEvent.click(opener); await screen.findByRole("dialog");
  const reads = f.list.mock.calls.length;
  await act(async () => { await f.client.invalidateQueries(); await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(f.list).toHaveBeenCalledTimes(reads); expect(row.isConnected).toBe(true);
  expect(f.save).not.toHaveBeenCalled(); expect(f.replace).not.toHaveBeenCalled();
});
