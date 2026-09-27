import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InboxService, NotificationPreferencesSchema } from "@delinoio/delidev-api-client";
import { NotificationSettings } from "./notification-settings";
import { MutationIntents } from "./mutation";

function fixture() {
  let preferences = create(NotificationPreferencesSchema, { revision: 1n, interactions: true, terminals: false });
  const save = vi.fn(async (request: { preferences?: typeof preferences }) => {
    preferences = create(NotificationPreferencesSchema, { interactions: request.preferences?.interactions, terminals: request.preferences?.terminals, revision: preferences.revision + 1n });
    return { preferences };
  });
  const transport = createRouterTransport((router) => router.service(InboxService, { getNotificationPreferences: () => ({ preferences }), setNotificationPreferences: save }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NotificationSettings active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, save, change: async () => { preferences = create(NotificationPreferencesSchema, { revision: 9n, interactions: false, terminals: false }); await client.invalidateQueries(); } };
}

it("retains stale notification drafts across settings visibility and never saves over a changed revision", async () => {
  const value = fixture(), mounted = render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }));
  mounted.rerender(value.view(false)); mounted.rerender(value.view());
  expect((screen.getByRole("checkbox", { name: "Execution completion, failure and interruption" }) as HTMLInputElement).checked).toBe(true);
  await act(value.change);
  expect(await screen.findByText(/These preferences changed elsewhere/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Save notification preferences" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Cancel notification edit" }));
  expect((screen.getByRole("checkbox", { name: "Questions and approval requests" }) as HTMLInputElement).checked).toBe(false);
});

it("retries only the original uncertain preference request after current preferences change", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Acknowledgment lost", Code.Unavailable));
  const mounted = render(value.view());
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Questions and approval requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Save notification preferences" }));
  await screen.findByRole("button", { name: "Retry the same notification preferences" });
  mounted.rerender(value.view(false)); mounted.rerender(value.view());
  await act(value.change);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same notification preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  expect(value.save.mock.calls[0][0].preferences).toMatchObject({ revision: 1n, interactions: false, terminals: false });
});
