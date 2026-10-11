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
      tab = newRequestId();
    const register = vi.fn((request: RegisterBrowserProfileRequest) => ({
      profile: {
        id: request.accountId === accountA ? profileA : profileB,
        accountId: request.accountId,
        serverId: newRequestId(),
        deviceId: newRequestId(),
        revision: 1n,
        state: BrowserProfileState.ACTIVE,
      },
    }));
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, {
        listResources: () => ({ resources: [] }),
      });
      router.service(SessionService, { listQueue: () => ({ inputs: [] }) });
      router.service(BrowserService, {
        registerBrowserProfile: register,
        listBrowserProfiles: () => ({ profiles: [] }),
        getBrowserCapabilities: () => ({
          capabilities: [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1],
        }),
      });
    });
    const pages = new Map<
      string,
      {
        tabs: { tabs: { id: string; url: string }[]; selected: string };
        removal_pending: boolean;
      }
    >();
    const receipts = new Map<
      string,
      {
        operation_id: string;
        page_id: string;
        phase: string;
        metadata_committed: boolean;
        failure: null;
      }
    >();
    native.mockReset().mockImplementation(async (command, input) => {
      let local = pages.get(input.profileId) ?? {
        tabs: { tabs: [], selected: "" },
        removal_pending: false,
      };
      if (command === "browser_pages") {
        const request = input.request;
        if (request.action === "create") {
          const page = { id: newRequestId(), url: request.url };
          local = {
            ...local,
            tabs: { tabs: [...local.tabs.tabs, page], selected: page.id },
          };
          pages.set(input.profileId, local);
          receipts.set(request.operation_id, {
            operation_id: request.operation_id,
            page_id: page.id,
            phase: "completed",
            metadata_committed: true,
            failure: null,
          });
        }
        return {
          state: local,
          receipt: receipts.get(request.operation_id) ?? null,
          closed_pages: [],
        };
      }
      return local;
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
    const open = async () => {
      if (!screen.queryByRole("dialog", { name: "New browser page" }))
        fireEvent.click(
          screen.getByRole("button", { name: "New browser page" }),
        );
      fireEvent.change(
        await screen.findByRole("textbox", { name: "Address" }),
        { target: { value: "https://fixture.test/" } },
      );
      await waitFor(() =>
        expect(
          (
            screen.getByRole("button", {
              name: "Open page",
            }) as HTMLButtonElement
          ).disabled,
        ).toBe(false),
      );
      fireEvent.click(screen.getByRole("button", { name: "Open page" }));
      await screen.findByRole("tab", {
        name: "https://fixture.test/",
        selected: true,
      });
    };
    try {
      await waitFor(() => expect(publish).toBeTypeOf("function"));
      fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
      fireEvent.click(screen.getByRole("menuitem", { name: "Browser" }));
      await open();
      expect(register.mock.calls[0][0]).toMatchObject({
        accountId: accountA,
        session: { expectedRevision: 1n },
      });
      const first = native.mock.calls.find(([op]) => op === "open_browser")![1];
      await act(async () => publish(resource(2n, [accountB])));
      await waitFor(() =>
        expect(native).toHaveBeenCalledWith(
          "control_browser",
          expect.objectContaining({ action: "hide", viewId: first.viewId }),
        ),
      );
      expect((composer as HTMLTextAreaElement).value).toBe(
        "unsent composer text",
      );
      expect(register).toHaveBeenCalledTimes(1);
      await open();
      expect(register.mock.calls[1][0]).toMatchObject({
        accountId: accountB,
        session: { expectedRevision: 2n },
      });
      const second = native.mock.calls.filter(
        ([op]) => op === "open_browser",
      )[1][1];
      expect(second.profileId).toBe(profileB);
      await act(async () => publish(resource(3n, [accountB, accountA])));
      await waitFor(() =>
        expect(native).toHaveBeenCalledWith(
          "control_browser",
          expect.objectContaining({ action: "hide", viewId: second.viewId }),
        ),
      );
      await open();
      expect(register.mock.calls[2][0]).toMatchObject({
        accountId: accountA,
        session: { expectedRevision: 3n },
      });
      expect(
        native.mock.calls.filter(([op]) => op === "open_browser")[2][1]
          .profileId,
      ).toBe(profileA);
      expect((composer as HTMLTextAreaElement).value).toBe(
        "unsent composer text",
      );
    } finally {
      view.unmount();
      client.clear();
    }
  },
);
