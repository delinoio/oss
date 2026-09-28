import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Integrations } from "./integrations";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";

function fixture() {
  let profile = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, revision: 3n, schemaVersion: 1, documentJson: encode({ name: "Work", provider: "github.com", token_kind: "fine-grained", resource_owner: "test-owner" }) });
  const save = vi.fn(async (_request: unknown) => ({ profile }));
  const replace = vi.fn(async (_request: unknown) => ({ profile, problemJson: new Uint8Array() }));
  const validate = vi.fn(async (_request: unknown) => ({ profile }));
  const remove = vi.fn(async (_request: unknown) => ({ deleted: true }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { listResources: () => ({ resources: [profile] }), getResource: () => ({ resource: profile }) });
    router.service(IntegrationService, { saveIntegrationProfile: save, replaceIntegrationToken: replace, validateIntegrationProfile: validate, deleteIntegrationProfile: remove });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Integrations active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { save, replace, validate, remove, client, view, profile, update: (value: Resource) => { profile = value; } };
}
it("creates a fine-grained profile without token or fabricated identity fields", async () => {
  const f = fixture(); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
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
  f.update(create(ResourceSchema, { ...f.profile, revision: 9007199254741000n, documentJson: encode({ ...document(f.profile), pending: { operation: "delete-profile", request_id: requestId, expected_revision: "9007199254740999" } }) }));
  render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Manage Work" }));
  expect(f.remove).not.toHaveBeenCalled();
  expect((screen.getByRole("button", { name: "Validate profile" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Retry original profile deletion" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  expect(f.remove.mock.calls[0][0]).toMatchObject({ mutation: { requestId, expectedRevision: 9007199254740999n } });
});
it("keeps identity distinct from repository access and confirms before deleting", async () => {
  const f = fixture();
  f.update(create(ResourceSchema, { ...f.profile, documentJson: encode({ ...document(f.profile), connection: { generation_id: newRequestId(), validation: { state: "identity-verified", identity: { id: "17", node_id: "U_17", login: "fixture-user" } } } }) }));
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
