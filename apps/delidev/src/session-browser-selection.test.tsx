// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  BrowserCapability,
  BrowserProfileState,
  BrowserService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  SyncKind,
  newRequestId,
  synchronizeResources,
  type RegisterBrowserProfileRequest,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";
import { SessionTabsProvider } from "./session-tabs";

const native = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: native }));
vi.mock("@delinoio/delidev-api-client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@delinoio/delidev-api-client")>()),
  synchronizeResources: vi.fn(),
}));
// Keep unrelated session readers outside this browser boundary fixture.
vi.mock("./session-tools", () => ({ SessionTools: () => null }));
vi.mock("./session-fork", () => ({
  SessionForkAction: () => null,
  PendingSidechatPane: () => null,
  SessionCreationAction: { Fork: "fork", Sidechat: "sidechat" },
}));
vi.mock("./session-pull-requests", () => ({ SessionPullRequests: () => null }));
vi.mock("./session-budget", () => ({ SessionBudget: () => null }));
vi.mock("./native-usage", () => ({ NativeUsage: () => null }));

it.each([false, true])(
  "switches the open browser before Resume and retains the composer (current execution: %s)",
  async (current) => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      x: 100,
      y: 100,
      left: 100,
      top: 100,
      width: 300,
      height: 200,
      right: 400,
      bottom: 300,
      toJSON: () => ({}),
    });
    const accountA = newRequestId(),
      accountB = newRequestId(),
      sessionId = newRequestId();
    const execution = {
      initial_execution: { id: newRequestId(), initial_account_id: accountA },
      ...(current
        ? { current_execution: { id: newRequestId(), account_id: accountA } }
        : {}),
      archive: "active",
      dispatch: "paused",
      workspace: "general-chat",
    };
    const resource = (revision: bigint, accounts: string[]) =>
      create(ResourceSchema, {
        id: sessionId,
        kind: EntityKind.SESSION,
        schemaVersion: 1,
        revision,
        documentJson: encode({
          ...execution,
          account_changes: accounts.map((account_id) => ({ account_id })),
        }),
      });
    let publish!: (value: Resource | undefined) => void;
    vi.mocked(synchronizeResources).mockImplementation(
      async function* (_client, _filter, options) {
        yield { kind: SyncKind.Snapshot, resources: [resource(1n, [])] };
        while (!options.signal?.aborted) {
          const next = await new Promise<Resource | undefined>((resolve) => {
            const abort = () => resolve(undefined);
            options.signal?.addEventListener("abort", abort, { once: true });
            publish = (value) => {
              options.signal?.removeEventListener("abort", abort);
              resolve(value);
            };
          });
          if (!next) return;
          yield {
            kind: SyncKind.Upsert,
            resource: next,
            eventId: newRequestId(),
          };
        }
      },
    );
    const profileA = newRequestId(),
      profileB = newRequestId(),
      pageA = newRequestId();
    const profile = (id: string, accountId: string) => ({
      id,
      accountId,
      serverId: newRequestId(),
      deviceId: newRequestId(),
      revision: 1n,
      state: BrowserProfileState.ACTIVE,
    });
    // Account A already retains one native page; account B has no profile yet.
    let registeredB = false;
    const pages = new Map<string, { id: string; url: string }[]>([
      [profileA, [{ id: pageA, url: "https://fixture.test/" }]],
      [profileB, []],
    ]);
    const register = vi.fn((request: RegisterBrowserProfileRequest) => {
      registeredB = registeredB || request.accountId === accountB;
      return {
        profile: profile(
          request.accountId === accountA ? profileA : profileB,
          request.accountId,
        ),
      };
    });
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, {
        listResources: () => ({ resources: [] }),
      });
      router.service(SessionService, { listQueue: () => ({ inputs: [] }) });
      router.service(BrowserService, {
        registerBrowserProfile: register,
        listBrowserProfiles: () => ({
          profiles: [
            profile(profileA, accountA),
            ...(registeredB ? [profile(profileB, accountB)] : []),
          ],
        }),
        getBrowserCapabilities: () => ({
          capabilities: [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1],
        }),
      });
    });
    const state = (profileId: string) => {
      const list = pages.get(profileId)!;
      return {
        tabs: { tabs: list, selected: list[0]?.id ?? "" },
        removal_pending: false,
      };
    };
    native.mockReset().mockImplementation(async (operation, args) => {
      if (operation === "browser_pages") {
        const request = args.request;
        if (request.action === "create") {
          const list = pages.get(args.profileId)!;
          const page = { id: newRequestId(), url: request.url };
          list.push(page);
          return {
            state: state(args.profileId),
            receipt: {
              operation_id: request.operation_id,
              page_id: page.id,
              phase: "completed",
              metadata_committed: true,
              native_cleanup_complete: true,
              failure: null,
            },
          };
        }
        return { state: state(args.profileId), receipt: null };
      }
      return state(args.profileId);
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const view = render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <SessionTabsProvider>
              <SessionView
                id={sessionId}
                draft="unsent composer text"
                setDraft={() => {}}
              />
            </SessionTabsProvider>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>,
    );
    const composer = await screen.findByRole("textbox", { name: "Message" });
    try {
      await waitFor(() => expect(publish).toBeTypeOf("function"));
      // Restoration lists the retained page and registers nothing.
      fireEvent.click(
        await screen.findByRole("tab", { name: "https://fixture.test/" }),
      );
      await waitFor(() =>
        expect(
          native.mock.calls.filter(([op]) => op === "open_browser"),
        ).toHaveLength(1),
      );
      expect(register).not.toHaveBeenCalled();
      const first = native.mock.calls.find(([op]) => op === "open_browser")![1];
      expect(first).toMatchObject({ profileId: profileA, pageId: pageA });
      // A pending account switch closes A's presentation and its descriptors
      // without rewriting the unsent composer; B has no retained profile.
      await act(async () => publish(resource(2n, [accountB])));
      await waitFor(() =>
        expect(native).toHaveBeenCalledWith(
          "control_browser",
          expect.objectContaining({ action: "hide", viewId: first.viewId }),
        ),
      );
      await waitFor(() =>
        expect(
          screen.queryByRole("tab", { name: "https://fixture.test/" }),
        ).toBeNull(),
      );
      expect((composer as HTMLTextAreaElement).value).toBe(
        "unsent composer text",
      );
      expect(register).not.toHaveBeenCalled();
      // Explicitly opening a page registers the selected account once.
      fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
      fireEvent.click(screen.getByRole("menuitem", { name: "Browser" }));
      const dialog = await screen.findByRole("dialog", {
        name: "New browser page",
      });
      fireEvent.change(
        within(dialog).getByRole("textbox", { name: "Address" }),
        {
          target: { value: "https://fixture.test/b" },
        },
      );
      await waitFor(() =>
        expect(
          (
            within(dialog).getByRole("button", {
              name: "Open page",
            }) as HTMLButtonElement
          ).disabled,
        ).toBe(false),
      );
      fireEvent.click(
        within(dialog).getByRole("button", { name: "Open page" }),
      );
      await screen.findByRole("tab", {
        name: "https://fixture.test/b",
        selected: true,
      });
      expect(register).toHaveBeenCalledTimes(1);
      expect(register.mock.calls[0][0]).toMatchObject({
        accountId: accountB,
        session: { expectedRevision: 2n },
      });
      await waitFor(() =>
        expect(
          native.mock.calls.filter(([op]) => op === "open_browser"),
        ).toHaveLength(2),
      );
      const second = native.mock.calls.filter(
        ([op]) => op === "open_browser",
      )[1][1];
      expect(second.profileId).toBe(profileB);
      // Switching back restores A's retained page without a second registration.
      await act(async () => publish(resource(3n, [accountB, accountA])));
      await waitFor(() =>
        expect(native).toHaveBeenCalledWith(
          "control_browser",
          expect.objectContaining({ action: "hide", viewId: second.viewId }),
        ),
      );
      await screen.findByRole("tab", { name: "https://fixture.test/" });
      expect(
        screen.queryByRole("tab", { name: "https://fixture.test/b" }),
      ).toBeNull();
      expect(register).toHaveBeenCalledTimes(1);
      expect((composer as HTMLTextAreaElement).value).toBe(
        "unsent composer text",
      );
    } finally {
      view.unmount();
      client.clear();
    }
  },
);
