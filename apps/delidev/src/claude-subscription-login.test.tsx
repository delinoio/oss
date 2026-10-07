// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SubscriptionLoginMethod,
  SubscriptionLoginState,
  SubscriptionService,
  SubscriptionServiceId,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import { claudeLoginURL } from "./claude-subscription-login";
import { useSubscriptionLogin } from "./subscription-login";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { document, encode } from "./documents";
const authorization = new URL("https://claude.com/cai/oauth/authorize");
for (const [key, value] of Object.entries({
  client_id: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  response_type: "code",
  redirect_uri: "https://platform.claude.com/oauth/code/callback",
  scope: "user:profile user:inference user:sessions:claude_code",
  code_challenge: "a".repeat(43),
  code_challenge_method: "S256",
  state: "b".repeat(32),
  code: "true",
}))
  authorization.searchParams.set(key, value);
const originalURL = authorization.href;
function fixture(dialog = false) {
  const machine = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Remote Linux",
      disabled: false,
      worker_capabilities: ["native-claude-subscriptions-v1"],
      installations: [
        {
          harness: "claude-code",
          version: "2.1.236",
          state: "detected",
          protocol_verified: true,
          protocol: { state: "verified" },
        },
      ],
    }),
  });
  let current = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 1n,
    documentJson: encode({
      alias: "Claude",
      type: "subscription",
      subscription_service: "claude",
    }),
  });
  let state = SubscriptionLoginState.WAITING,
    operation = "";
  const generation = newRequestId(),
    profile = newRequestId(),
    browser = newRequestId();
  const save = vi.fn((request) => {
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: request.documentJson,
    });
    return { requestId: request.mutation.requestId, resource: current };
  });
  const login = vi.fn((request) => {
    operation = request.mutation.requestId;
    current = create(ResourceSchema, {
      ...current,
      revision: current.revision + 1n,
      documentJson: encode({
        ...document(current),
        subscription: {
          owner_machine_id: machine.id,
          native_profile_id: profile,
          native_operation: { id: operation },
          pending: { id: operation },
        },
      }),
    });
    return { account: current, operationId: operation };
  });
  const progress = vi.fn(() => ({
    state,
    url: state === SubscriptionLoginState.WAITING ? originalURL : "",
    loginMethod: SubscriptionLoginMethod.BROWSER_CODE,
    generation,
  }));
  const submit = vi.fn(async (request) => {
    request.code.fill(0);
    return { accepted: true };
  });
  const cancel = vi.fn(() => ({ account: current }));
  const native = vi.fn(
    async (
      _opening: string,
      _action: OAuthNativeAction,
      _generation: string,
      _operation: string,
      _url: string,
    ) => ({ generation: browser }),
  );
  const list = vi.fn((_request: { filter?: { pageToken: string } }) => ({ resources: [machine], nextPageToken: "" }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, {
      listResources: list,
      getResource: (request) => ({
        resource: request.id === machine.id ? machine : current,
      }),
    });
    router.service(SubscriptionService, {
      requestSubscription: login,
      getSubscriptionProgress: progress,
      submitSubscriptionLoginCode: submit,
      cancelSubscription: cancel,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Body() {
    const flow = useSubscriptionLogin(true, () => undefined);
    return (
      <>
        {flow.body ? (
          dialog ? (
            <SettingsTaskDialog
              title="Connect Claude account"
              size={SettingsDialogSize.Wide}
              retained={flow.retained}
              close={flow.hide}
            >
              {flow.body}
            </SettingsTaskDialog>
          ) : (
            flow.body
          )
        ) : flow.hidden ? (
          <button onClick={flow.show}>View original operation</button>
        ) : (
          <button onClick={() => flow.begin(SubscriptionServiceId.Claude)}>
            Add Claude
          </button>
        )}
      </>
    );
  }
  const view = () => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider control={native}>
            <SettingsLifetime>{() => <Body />}</SettingsLifetime>
          </OAuthNativeProvider>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    view,
    client,
    list,
    save,
    login,
    progress,
    submit,
    cancel,
    native,
    machine,
    current: () => current,
    success: () => {
      state = SubscriptionLoginState.SUCCEEDED;
      current = create(ResourceSchema, {
        ...current,
        revision: current.revision + 1n,
        documentJson: encode({
          ...document(current),
          connection: { id: newRequestId() },
          subscription: {
            owner_machine_id: machine.id,
            native_profile_id: profile,
            generation,
            native_operation: { id: operation },
          },
        }),
      });
    },
  };
}
async function start(f: ReturnType<typeof fixture>) {
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Add Claude" }));
  const runner = await screen.findByRole("combobox", { name: "Runner Device" });
  fireEvent.click(runner);
  await screen.findByRole("option", { name: "Remote Linux" });
  expect(f.save).not.toHaveBeenCalled();
  expect(f.login).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("option", { name: "Remote Linux" }));
  const button = screen.getByRole("button", { name: "Start sign-in" });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(button);
  fireEvent.click(button);
  await screen.findByLabelText("Approval code", {}, { timeout: 4000 });
}
it("creates and logs in once after explicit Runner selection and keeps sensitive data outside caches", async () => {
  const f = fixture();
  await start(f);
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.login.mock.calls[0][0].machineId).toBe(f.machine.id);
  await waitFor(() =>
    expect(
      f.native.mock.calls.filter(
        (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionOpen,
      ),
    ).toHaveLength(1),
  );
  expect(f.native.mock.calls.some((c) => c[1] === OAuthNativeAction.Take)).toBe(
    false,
  );
  expect(
    JSON.stringify(
      f.client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
      (_key, value) => (typeof value === "bigint" ? value.toString() : value),
    ),
  ).not.toContain(originalURL);
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() =>
    expect(
      f.native.mock.calls.filter(
        (c) => c[1] === OAuthNativeAction.ClaudeSubscriptionReopen,
      ),
    ).toHaveLength(1),
  );
});
it("does not retransmit approval input after an uncertain result", async () => {
  const f = fixture();
  f.submit.mockImplementation(async (request) => {
    request.code.fill(0);
    throw new ConnectError("Fixture lost reply", Code.Unavailable);
  });
  await start(f);
  const input = screen.getByLabelText("Approval code") as HTMLInputElement;
  fireEvent.change(input, {
    target: { value: "fixture-approval#original-state" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit code" }));
  await waitFor(() => expect(f.submit).toHaveBeenCalledTimes(1));
  expect(input.value).toBe("");
  expect(input.disabled).toBe(true);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1100));
  });
  expect(f.submit).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Retry original request" }),
  ).toBeNull();
  expect(
    JSON.stringify(
      f.client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
      (_key, value) => (typeof value === "bigint" ? value.toString() : value),
    ),
  ).not.toContain("fixture-approval");
});
it("verifies original success before editing the name and Later keeps the connection", async () => {
  const f = fixture();
  await start(f);
  f.success();
  const name = await screen.findByLabelText(
    "Account name",
    {},
    { timeout: 4000 },
  );
  expect((name as HTMLInputElement).value).toBe("Claude");
  await waitFor(() => expect(window.document.activeElement).toBe(name));
  fireEvent.click(screen.getByRole("button", { name: "Later" }));
  expect(document(f.current()).connection).toBeTruthy();
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.cancel).not.toHaveBeenCalled();
});
it("hides the original task without canceling and clears the approval input on hide", async () => {
  const f = fixture(true);
  await start(f);
  const code = screen.getByLabelText("Approval code") as HTMLInputElement;
  fireEvent.change(code, { target: { value: "unsubmitted-fixture-code" } });
  fireEvent.click(
    screen.getByRole("button", { name: "Close Connect Claude account" }),
  );
  await screen.findByRole("button", { name: "View original operation" });
  expect(code.value).toBe("");
  expect(f.cancel).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "View original operation" }),
  );
  await screen.findByLabelText("Approval code");
  expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.save).toHaveBeenCalledTimes(1);
});
it("rejects a substituted browser profile", () => {
  expect(claudeLoginURL(originalURL)).toBe(true);
  expect(claudeLoginURL(originalURL + "&state=replacement-state-value")).toBe(
    false,
  );
  expect(
    claudeLoginURL(originalURL.replace("claude.com", "other.invalid")),
  ).toBe(false);
});

it("appends Runner display choices without clearing the selected exact machine or starting sign-in", async () => {
  const f = fixture();
  const second = create(ResourceSchema, { ...f.machine, id: newRequestId(), documentJson: encode({ ...document(f.machine), name: "Second Runner" }) });
  f.list.mockImplementation(request => ({ resources: request.filter?.pageToken ? [second] : [f.machine], nextPageToken: request.filter?.pageToken ? "" : "original-next" }));
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add Claude" }));
  const picker = await screen.findByRole("combobox", { name: "Runner Device" });
  fireEvent.click(picker); fireEvent.click(await screen.findByRole("option", { name: "Remote Linux" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(picker); fireEvent.click(screen.getByRole("button", { name: "Load more Runner Device" }));
  await screen.findByRole("option", { name: "Second Runner" });
  expect(screen.getByRole("option", { name: "Remote Linux" })).toBeTruthy();
  expect(picker.getAttribute("data-value")).toBe(f.machine.id);
  expect(f.list.mock.calls.map(([request]) => request.filter?.pageToken)).toEqual(["", "original-next"]);
  expect(f.save).not.toHaveBeenCalled(); expect(f.login).not.toHaveBeenCalled();
});

it("distinguishes failed inventory from a final empty read and retries only the original read", async () => {
  const f = fixture(); f.list.mockImplementationOnce(() => { throw new ConnectError("opaque native private path", Code.PermissionDenied); });
  render(f.view()); fireEvent.click(screen.getByText("Add Claude"));
  await screen.findByText("Runner Devices could not be read. Check your access and connection, then read the list again.");
  expect(screen.getByText("This connection is not authorized for the action. Check its access.")).toBeTruthy();
  expect(screen.queryByText(/opaque native private path/)).toBeNull();
  expect(screen.queryByText("No Runner Devices were returned. Register a Runner Device with this server, then check this list again.")).toBeNull();
  f.list.mockReturnValue({ resources: [], nextPageToken: "" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh Runner Devices" }));
  await screen.findByText("No Runner Devices were returned. Register a Runner Device with this server, then check this list again.");
  expect(f.save).not.toHaveBeenCalled(); expect(f.login).not.toHaveBeenCalled();
});
it("distinguishes a current page with no eligible Runner from final absence and keeps excluded choices disabled", async () => {
  const f = fixture();
  const excluded = create(ResourceSchema, { ...f.machine, id: newRequestId(), documentJson: encode({ ...document(f.machine), name: "Disabled Runner", disabled: true }) });
  f.list.mockImplementation(request => ({ resources: request.filter?.pageToken ? [f.machine] : [excluded], nextPageToken: request.filter?.pageToken ? "" : "next-page" }));
  render(f.view()); fireEvent.click(screen.getByText("Add Claude"));
  await screen.findByText("No eligible Runner is in the loaded pages. Load more Runner Devices before deciding none are available.");
  const picker = screen.getByRole("combobox", { name: "Runner Device" }); fireEvent.click(picker);
  const disabled = await screen.findByRole("option", { name: "Disabled Runner" }); expect((disabled as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(disabled); expect(picker.getAttribute("data-value")).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Load more Runner Device" }));
  fireEvent.click(await screen.findByRole("option", { name: "Remote Linux" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(false));
  expect(f.save).not.toHaveBeenCalled(); expect(f.login).not.toHaveBeenCalled();
});
it("retains selected Runner after failed refresh and grants no new sign-in while its retry is pending", async () => {
  const f = fixture(); render(f.view()); fireEvent.click(screen.getByText("Add Claude"));
  const picker = await screen.findByRole("combobox", { name: "Runner Device" }); fireEvent.click(picker); fireEvent.click(await screen.findByRole("option", { name: "Remote Linux" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(false));
  f.list.mockImplementationOnce(() => { throw new ConnectError("refresh lost", Code.Unavailable); });
  fireEvent.click(screen.getByRole("button", { name: "Refresh Runner Devices" }));
  await screen.findByText("Runner observations could not be refreshed. Previous observations are unconfirmed; check the list again before starting sign-in.");
  expect(picker.getAttribute("data-value")).toBe(f.machine.id);
  expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(true);
  let resolve: (result: { resources: typeof f.machine[]; nextPageToken: string }) => void = () => {};
  f.list.mockImplementationOnce(() => new Promise(done => { resolve = done; }) as unknown as { resources: typeof f.machine[]; nextPageToken: string });
  fireEvent.click(screen.getByRole("button", { name: "Refresh Runner Devices" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Start sign-in" }));
  expect(f.save).not.toHaveBeenCalled(); expect(f.login).not.toHaveBeenCalled();
  await act(async () => resolve({ resources: [f.machine], nextPageToken: "" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Start sign-in" }) as HTMLButtonElement).disabled).toBe(false));
});
