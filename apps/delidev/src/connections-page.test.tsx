// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { TransportProvider } from "@connectrpc/connect-query";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SystemService, newRequestId } from "@delinoio/delidev-api-client";
import {
  ConnectionsPage,
  PersistentConnectionView,
  type ConnectionPageSlots,
} from "./connections-page";
import { LocalConnectionPresentationProvider } from "./local-connection-presentation";
import { LocalServerControls, LocalServerState } from "./local-server";
import {
  SavedConnections,
  SavedConnectionState,
  type SavedConnectionActions,
} from "./saved-connections";
import { MutationIntents } from "./mutation";

function fixture() {
  const profile = {
    version: 1,
    revision: 2,
    id: newRequestId(),
    name: "Studio server",
    endpoint: "https://studio.example",
    server_id: newRequestId(),
    pairing_id: newRequestId(),
    device_id: newRequestId(),
    state: SavedConnectionState.Paired,
    created_at: "2026-10-08T00:00:00Z",
  };
  const actions: SavedConnectionActions = {
    list: vi.fn(async () => [
      profile,
      {
        ...profile,
        id: newRequestId(),
        name: "Build server",
        endpoint: "https://build.example",
      },
    ]),
    removed: vi.fn(async () => ({ connections: [] })),
    remove: vi.fn(async () => profile),
    retainedWorker: vi.fn(),
    pair: vi.fn(async () => profile),
    retry: vi.fn(async () => profile),
    rename: vi.fn(async () => profile),
    open: vi.fn(async () => {}),
  };
  const stop = vi.fn(async (_request: unknown) => ({})),
    start = vi.fn();
  const transport = createRouterTransport((router) =>
    router.service(SystemService, { stopServer: stop }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Harness() {
    const [shown, show] = useState(true),
      [slots, setSlots] = useState<ConnectionPageSlots>();
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <button onClick={() => show((value) => !value)}>
              Toggle Settings
            </button>
            {shown ? <ConnectionsPage local onSlots={setSlots} /> : null}
            <LocalConnectionPresentationProvider
              inline={false}
              target={slots?.current}
            >
              <LocalServerControls
                connected
                endpoint="http://127.0.0.1:46300"
                status={{
                  state: LocalServerState.Ready,
                  attempts: 0,
                  retry_ms: 0,
                }}
                restart={start}
                busy={false}
              />
            </LocalConnectionPresentationProvider>
            <SavedConnections
              target={slots?.saved}
              advancedTarget={slots?.advanced}
              visible={Boolean(slots)}
              close={() => {}}
              actions={actions}
            />
            <PersistentConnectionView target={slots?.advanced} hidden>
              <p role="alert">Retained update needs inspection</p>
            </PersistentConnectionView>
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return { actions, stop, start, Harness, profile };
}
it("shows the current Local connection and stored profiles without probes or mutations", async () => {
  const value = fixture();
  render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  expect(screen.getByText("Connected")).toBeTruthy();
  expect(screen.getByText("http://127.0.0.1:46300")).toBeTruthy();
  expect(screen.getAllByText("Saved")).toHaveLength(2);
  expect(screen.queryByRole("dialog", { name: "Saved servers" })).toBeNull();
  expect(value.stop).not.toHaveBeenCalled();
  expect(value.start).not.toHaveBeenCalled();
  expect(value.actions.open).not.toHaveBeenCalled();
  expect(value.actions.removed).not.toHaveBeenCalled();
  expect(screen.getByText("Advanced controls need attention.")).toBeTruthy();
  const advanced = screen.getByText("Advanced").closest("details")!;
  expect(advanced.open).toBe(false);
  fireEvent.click(screen.getByText("Advanced"));
  expect(value.actions.removed).not.toHaveBeenCalled();
});
it("retains the original stop confirmation and exact uncertain request across page departure", async () => {
  const value = fixture();
  value.stop.mockRejectedValueOnce(
    new ConnectError("Response lost", Code.Unavailable),
  );
  render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  fireEvent.click(screen.getByRole("button", { name: "Stop local server" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm server stop" }));
  await screen.findByRole("button", { name: "Retry the same server stop" });
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same server stop" }),
  );
  await waitFor(() => expect(value.stop).toHaveBeenCalledTimes(2));
  expect(value.stop.mock.calls[0]?.[0]).toEqual(value.stop.mock.calls[1]?.[0]);
  expect(value.start).not.toHaveBeenCalled();
});
it("retains saved rows after a failed inventory read and keeps pending private pairing bytes", async () => {
  const value = fixture();
  render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  vi.mocked(value.actions.list).mockRejectedValueOnce("timed-out");
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh saved servers" }),
  );
  await screen.findByText(
    "Saved server inventory is unavailable. Showing the last confirmed profiles.",
  );
  expect(
    screen.getByRole("button", { name: "Open Studio server" }),
  ).toBeTruthy();
  expect(screen.queryByText("No saved servers")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  const input = screen.getByLabelText(
    "Private client pairing document",
  ) as HTMLInputElement;
  const grant = JSON.stringify({
    version: 1,
    endpoint: value.profile.endpoint,
    server_id: value.profile.server_id,
    pairing_id: value.profile.pairing_id,
    code: "private-fixture",
  });
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "Original name" },
  });
  fireEvent.change(input, { target: { value: grant } });
  vi.mocked(value.actions.pair).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  await screen.findByRole("button", { name: "Retry original server pairing" });
  fireEvent.click(
    screen.getByRole("button", { name: "Close Add a server connection" }),
  );
  for (let visit = 0; visit < 2; visit++) {
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    expect(
      screen.queryByRole("dialog", { name: "Add a server connection" }),
    ).toBeNull();
    expect(value.actions.pair).toHaveBeenCalledTimes(1);
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Continue Add a server connection" }),
  );
  expect(
    (
      screen.getByLabelText(
        "Private client pairing document",
      ) as HTMLInputElement
    ).value,
  ).toBe(grant);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original server pairing" }),
  );
  await waitFor(() => expect(value.actions.pair).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.pair).mock.calls[0]).toEqual(
    vi.mocked(value.actions.pair).mock.calls[1],
  );
});
it("retains original saved-profile rename and removal receipts across page movement", async () => {
  const value = fixture();
  render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  const menu = screen.getByLabelText("Actions for Studio server");
  fireEvent.click(menu);
  fireEvent.click(screen.getByRole("button", { name: "Rename Studio server" }));
  fireEvent.change(screen.getByLabelText("New connection name"), {
    target: { value: "Original rename" },
  });
  vi.mocked(value.actions.rename).mockRejectedValueOnce("timed-out");
  fireEvent.click(screen.getByRole("button", { name: "Save connection name" }));
  await screen.findByRole("button", { name: "Retry original name edit" });
  fireEvent.click(
    screen.getByRole("button", { name: "Close Rename Studio server" }),
  );
  for (let visit = 0; visit < 2; visit++) {
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    expect(
      screen.queryByRole("dialog", { name: "Rename Studio server" }),
    ).toBeNull();
    expect(value.actions.rename).toHaveBeenCalledTimes(1);
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Continue Rename Studio server" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original name edit" }),
  );
  await waitFor(() => expect(value.actions.rename).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.rename).mock.calls[0]).toEqual(
    vi.mocked(value.actions.rename).mock.calls[1],
  );
  fireEvent.click(screen.getByRole("button", { name: "Discard name edit" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove Studio server" }));
  vi.mocked(value.actions.remove).mockRejectedValueOnce("timed-out");
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm connection removal" }),
  );
  await screen.findByRole("button", {
    name: "Retry original connection removal",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Close Confirm connection removal" }),
  );
  for (let visit = 0; visit < 2; visit++) {
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
    expect(
      screen.queryByRole("dialog", { name: "Confirm connection removal" }),
    ).toBeNull();
    expect(value.actions.remove).toHaveBeenCalledTimes(1);
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Continue Confirm connection removal" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original connection removal" }),
  );
  await waitFor(() => expect(value.actions.remove).toHaveBeenCalledTimes(2));
  expect(vi.mocked(value.actions.remove).mock.calls[0]).toEqual(
    vi.mocked(value.actions.remove).mock.calls[1],
  );
  expect(vi.mocked(value.actions.remove).mock.calls[0]?.[0]).toBe(
    value.profile.id,
  );
  expect(vi.mocked(value.actions.remove).mock.calls[0]?.[2]).toBe(
    value.profile.revision,
  );
});

it.each([
  {
    action: "Add server",
    title: "Add a server connection",
    field: "Connection name",
  },
  {
    action: "Rename Studio server",
    title: "Rename Studio server",
    field: "New connection name",
  },
  {
    action: "Remove Studio server",
    title: "Confirm connection removal",
    field: undefined,
  },
])(
  "retains dismissed $title across page moves and explicit Continue",
  async ({ action, title, field }) => {
    const value = fixture();
    render(<value.Harness />);
    await screen.findByRole("button", { name: "Open Studio server" });
    if (action !== "Add server")
      fireEvent.click(screen.getByLabelText("Actions for Studio server"));
    fireEvent.click(screen.getByRole("button", { name: action }));
    const dialog = screen.getByRole("dialog", {
      name: title,
    }) as HTMLDialogElement;
    const input = field
      ? (screen.getByLabelText(field) as HTMLInputElement)
      : undefined;
    if (input) fireEvent.change(input, { target: { value: "Retained draft" } });
    for (const dismissal of ["Close", "Escape"]) {
      if (dismissal === "Close")
        fireEvent.click(screen.getByRole("button", { name: `Close ${title}` }));
      else fireEvent(dialog, new Event("cancel", { cancelable: true }));
      expect(dialog.open).toBe(false);
      const navigation = screen.getByRole("button", {
        name: "Toggle Settings",
      });
      navigation.focus();
      for (let visit = 0; visit < 2; visit++) {
        fireEvent.click(navigation);
        fireEvent.click(navigation);
        expect(dialog.open).toBe(false);
        expect(
          screen.getByRole("button", { name: `Continue ${title}` }),
        ).toBeTruthy();
        expect(document.activeElement).toBe(navigation);
      }
      fireEvent.click(
        screen.getByRole("button", { name: `Continue ${title}` }),
      );
      expect(screen.getByRole("dialog", { name: title })).toBe(dialog);
      expect(dialog.open).toBe(true);
      if (field) {
        expect(screen.getByLabelText(field)).toBe(input);
        expect(input?.value).toBe("Retained draft");
      }
      expect(value.actions.pair).not.toHaveBeenCalled();
      expect(value.actions.rename).not.toHaveBeenCalled();
      expect(value.actions.remove).not.toHaveBeenCalled();
    }
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    const navigation = screen.getByRole("button", { name: "Toggle Settings" });
    (document.activeElement as HTMLElement | null)?.blur();
    fireEvent.click(navigation);
    fireEvent.click(navigation);
    expect(dialog.open).toBe(false);
    const resume = screen.getByRole("button", { name: `Continue ${title}` });
    expect(document.activeElement).toBe(resume);
    fireEvent.click(resume);
    if (action === "Rename Studio server") {
      fireEvent.click(
        screen.getByRole("button", { name: "Discard name edit" }),
      );
      const menu = screen.getByLabelText("Actions for Studio server");
      if (!(menu.closest("details") as HTMLDetailsElement).open)
        fireEvent.click(menu);
      fireEvent.click(screen.getByRole("button", { name: action }));
      expect(screen.getByRole("dialog", { name: title })).toBeTruthy();
    } else if (action === "Remove Studio server") {
      fireEvent.click(screen.getByRole("button", { name: "Keep connection" }));
      const menu = screen.getByLabelText("Actions for Studio server");
      if (!(menu.closest("details") as HTMLDetailsElement).open)
        fireEvent.click(menu);
      fireEvent.click(screen.getByRole("button", { name: action }));
      expect(screen.getByRole("dialog", { name: title })).toBeTruthy();
    }
  },
);
it("presents a new Add task without remounting the retained form", async () => {
  const value = fixture();
  let complete!: () => void;
  vi.mocked(value.actions.pair).mockImplementation(
    (id) =>
      new Promise((resolve) => {
        complete = () => resolve({ ...value.profile, id });
      }),
  );
  render(<value.Harness />);
  await screen.findByRole("button", { name: "Open Studio server" });
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  const dialog = screen.getByRole("dialog", {
    name: "Add a server connection",
  });
  const input = screen.getByLabelText("Connection name") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "First server" } });
  fireEvent.change(screen.getByLabelText("Private client pairing document"), {
    target: {
      value: JSON.stringify({
        version: 1,
        endpoint: value.profile.endpoint,
        server_id: value.profile.server_id,
        pairing_id: value.profile.pairing_id,
        code: "private-fixture",
      }),
    },
  });
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  const original = vi.mocked(value.actions.pair).mock.calls[0];
  fireEvent.click(
    screen.getByRole("button", { name: "Close Add a server connection" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle Settings" }));
  expect(value.actions.pair).toHaveBeenCalledTimes(1);
  expect(vi.mocked(value.actions.pair).mock.calls[0]).toEqual(original);
  expect(
    screen.queryByRole("dialog", { name: "Add a server connection" }),
  ).toBeNull();
  await act(async () => complete());
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "Add server" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  expect(screen.getByRole("dialog", { name: "Add a server connection" })).toBe(
    dialog,
  );
  expect(screen.getByLabelText("Connection name")).toBe(input);
  expect(input.value).toBe("");
  expect(
    screen.queryByRole("button", {
      name: "Continue Add a server connection",
    }),
  ).toBeNull();
  expect(value.actions.pair).toHaveBeenCalledTimes(1);
});

it("publishes no Tailscale slot from the Connections page", () => {
  const onSlots = vi.fn();
  render(<ConnectionsPage local onSlots={onSlots} />);
  const slots = onSlots.mock.calls.at(-1)?.[0] as ConnectionPageSlots;
  expect(slots.current).toBeInstanceOf(HTMLElement);
  expect(slots.saved).toBeInstanceOf(HTMLElement);
  expect(slots.advanced).toBeInstanceOf(HTMLElement);
  expect(Object.keys(slots).sort()).toEqual(["advanced", "current", "saved"]);
});
