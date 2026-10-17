import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { pressEnter } from "./test-form-enter";
import { danger, expectTone, neutral } from "./test-action-tone";
import { LocalWorkerAction, LocalWorkerState } from "./local-worker-controls";
import { newRequestId } from "@delinoio/delidev-api-client";
import {
  SavedConnections,
  SavedConnectionState,
  type SavedConnection,
  type SavedConnectionActions,
} from "./saved-connections";

function fixture() {
  const profile: SavedConnection = {
    version: 1,
    revision: 1,
    id: newRequestId(),
    name: "Saved server",
    endpoint: "https://fixture.example.test",
    server_id: newRequestId(),
    pairing_id: newRequestId(),
    device_id: newRequestId(),
    state: SavedConnectionState.Paired,
    created_at: "2026-09-25T00:00:00Z",
  };
  const grant = JSON.stringify({
    version: 1,
    endpoint: profile.endpoint,
    server_id: profile.server_id,
    pairing_id: profile.pairing_id,
    code: "private-single-use-fixture-code",
  });
  const list = vi.fn(async () => [profile]);
  const pair = vi.fn(async (id: string, _name: string, _grant: string) => ({
    ...profile,
    id,
  }));
  const retry = vi.fn(async (_id: string) => profile);
  const open = vi.fn(async (_id: string) => {});
  const rename = vi.fn(
    async (
      _id: string,
      _requestId: string,
      revision: number,
      name: string,
    ) => ({ ...profile, revision: revision + 1, name }),
  );
  const removed = vi.fn(async (_after: string) => ({
    connections: [] as SavedConnection[],
  }));
  const remove = vi.fn(
    async (_id: string, requestId: string, revision: number) => ({
      ...profile,
      revision: revision + 1,
      state: SavedConnectionState.Removed,
      removal: { request_id: requestId, expected_revision: revision },
    }),
  );
  const retainedWorker = vi.fn();
  const actions: SavedConnectionActions = {
    list,
    pair,
    retry,
    rename,
    open,
    removed,
    remove,
    retainedWorker,
  };
  return {
    profile,
    grant,
    actions,
    list,
    pair,
    retry,
    rename,
    open,
    removed,
    remove,
    retainedWorker,
  };
}
it("retains an uncertain pairing and masked original input across dialog visibility", async () => {
  const value = fixture();
  value.pair.mockRejectedValueOnce("timed-out");
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "New server" },
  });
  const input = screen.getByLabelText(
    "Private client pairing document",
  ) as HTMLInputElement;
  expect(input.type).toBe("password");
  fireEvent.change(input, { target: { value: value.grant } });
  expect(
    screen.getByText(`Pairing endpoint: ${value.profile.endpoint}`),
  ).toBeTruthy();
  expect(screen.queryByText("private-single-use-fixture-code")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  await screen.findByText(/operation has not been confirmed/);
  expect(
    (screen.getByLabelText("Connection name") as HTMLInputElement).disabled,
  ).toBe(true);
  view.rerender(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original server pairing" }),
  );
  await screen.findByText(/client pairing is saved/);
  expect(value.pair).toHaveBeenCalledTimes(2);
  expect(value.pair.mock.calls[0]).toEqual(value.pair.mock.calls[1]);
  expect(value.pair.mock.calls[0][2]).toBe(value.grant);
  expect(input.value).toBe("");
  expect(value.open).not.toHaveBeenCalled();
});
it("loads only while visible and separates opening from exact persisted pairing retry", async () => {
  const value = fixture();
  const pending = {
    ...value.profile,
    id: newRequestId(),
    name: "Pending server",
    state: SavedConnectionState.Pending,
    device_id: "",
  };
  value.list.mockResolvedValue([value.profile, pending]);
  value.retry.mockImplementation(async (id) => ({
    ...pending,
    id,
    state: SavedConnectionState.Paired,
    device_id: newRequestId(),
  }));
  const view = render(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  expect(value.list).not.toHaveBeenCalled();
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Open Saved server" }),
  );
  await waitFor(() =>
    expect(value.open).toHaveBeenCalledWith(value.profile.id),
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Retry Pending server",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry Pending server" }));
  await screen.findByText("The original client pairing is saved.");
  expect(value.retry).toHaveBeenCalledWith(pending.id);
  expect(value.pair).not.toHaveBeenCalled();
});
it("does not let an older inventory read erase a new pairing failure", async () => {
  const value = fixture();
  let release!: (profiles: SavedConnection[]) => void;
  value.list.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  value.pair.mockRejectedValueOnce("timed-out");
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "Retained" },
  });
  fireEvent.change(screen.getByLabelText("Private client pairing document"), {
    target: { value: value.grant },
  });
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  await screen.findByText(/operation has not been confirmed/);
  release([value.profile]);
  await waitFor(() =>
    expect(screen.getByText(/operation has not been confirmed/)).toBeTruthy(),
  );
  expect(value.pair).toHaveBeenCalledTimes(1);
});

it("retains an uncertain name edit and its revision across hiding and newer inventory", async () => {
  const value = fixture();
  value.rename.mockRejectedValueOnce("timed-out");
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Rename Saved server" }),
  );
  fireEvent.change(screen.getByLabelText("New connection name"), {
    target: { value: "First edit" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save connection name" }));
  await screen.findByText(/operation has not been confirmed/);
  expect(
    (screen.getByLabelText("New connection name") as HTMLInputElement).disabled,
  ).toBe(true);
  const newer = { ...value.profile, revision: 3, name: "Later accepted name" };
  value.list.mockResolvedValue([newer]);
  value.rename.mockResolvedValue(newer);
  view.rerender(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  await screen.findByText(/This connection changed while you were editing/);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original name edit" }),
  );
  await screen.findByText(/Current connection name: Later accepted name/);
  expect(value.rename.mock.calls[0]).toEqual(value.rename.mock.calls[1]);
  expect(value.rename.mock.calls[0]).toEqual([
    value.profile.id,
    expect.any(String),
    1,
    "First edit",
  ]);
  expect(value.pair).not.toHaveBeenCalled();
  expect(value.retry).not.toHaveBeenCalled();
  expect(value.open).not.toHaveBeenCalled();
});
it("preserves stale unsubmitted names and requires a fresh explicit edit", async () => {
  const value = fixture();
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Rename Saved server" }),
  );
  fireEvent.change(screen.getByLabelText("New connection name"), {
    target: { value: "My draft" },
  });
  value.list.mockResolvedValue([
    { ...value.profile, revision: 2, name: "Peer edit" },
  ]);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh saved servers" }),
  );
  await screen.findByText(/This connection changed while you were editing/);
  expect(
    (screen.getByLabelText("New connection name") as HTMLInputElement).value,
  ).toBe("My draft");
  expect(
    (
      screen.getByRole("button", {
        name: "Save connection name",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.rename).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Discard name edit" }));
  fireEvent.click(screen.getByRole("button", { name: "Rename Peer edit" }));
  expect(
    (screen.getByLabelText("New connection name") as HTMLInputElement).value,
  ).toBe("Peer edit");
});

it("requires removal confirmation, retains the original uncertain request, and clears only the selected client", async () => {
  const value = fixture();
  value.remove.mockRejectedValueOnce("timed-out");
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Remove Saved server" }),
  );
  expect(value.remove).not.toHaveBeenCalled();
  expect(
    screen.getByText(
      /closes all windows for this saved connection and discards their unsent drafts/,
    ),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm connection removal" }),
  );
  await screen.findByText(/operation has not been confirmed/);
  view.rerender(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  value.list.mockResolvedValue([]);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original connection removal" }),
  );
  await screen.findByText(/This local client connection was removed/);
  expect(value.remove.mock.calls[0]).toEqual(value.remove.mock.calls[1]);
  expect(value.remove.mock.calls[0]).toEqual([
    value.profile.id,
    expect.any(String),
    1,
  ]);
  expect(value.open).not.toHaveBeenCalled();
  expect(value.pair).not.toHaveBeenCalled();
  expect(value.retainedWorker).not.toHaveBeenCalled();
});
it("recovers the accepted cleanup request from inventory after reopening the app", async () => {
  const value = fixture();
  const request = newRequestId();
  value.list.mockResolvedValue([
    {
      ...value.profile,
      state: SavedConnectionState.Removing,
      revision: 2,
      removal: { request_id: request, expected_revision: 1 },
    },
  ]);
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry removal of Saved server",
    }),
  );
  expect(
    screen.queryByRole("button", { name: "Open Saved server" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry Saved server" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original connection removal" }),
  );
  await screen.findByText(/This local client connection was removed/);
  expect(value.remove).toHaveBeenCalledWith(value.profile.id, request, 1);
  expect(value.retry).not.toHaveBeenCalled();
});
it("blocks a stale removal confirmation while keeping the selected connection visible", async () => {
  const value = fixture();
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Remove Saved server" }),
  );
  value.list.mockResolvedValue([
    { ...value.profile, revision: 2, name: "New name" },
  ]);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh saved servers" }),
  );
  await screen.findByText(/The connection changed. Keep it/);
  expect(
    (
      screen.getByRole("button", {
        name: "Confirm connection removal",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.remove).not.toHaveBeenCalled();
});
it("keeps independent Worker stop targeting its original generation after client removal", async () => {
  const value = fixture(),
    request = newRequestId(),
    machine = newRequestId(),
    generation = newRequestId();
  value.list.mockResolvedValue([]);
  value.removed.mockResolvedValue({
    connections: [
      {
        ...value.profile,
        state: SavedConnectionState.Removed,
        revision: 2,
        removal: { request_id: request, expected_revision: 1 },
      },
    ],
  });
  value.retainedWorker.mockImplementation(async (_id, action) => {
    if (action === LocalWorkerAction.Stop) throw "timed-out";
    return {
      state: LocalWorkerState.Running,
      machine_id: machine,
      generation,
      controller_active: true,
    };
  });
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Show removed connections" }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Inspect retained Worker for Saved server",
    }),
  );
  const panel = await screen.findByRole("region", {
    name: "Retained Worker for Saved server",
  });
  await within(panel).findByText(/^Execution machine: Runner unavailable/);
  expect(panel.textContent).not.toContain(machine);
  expect(
    within(panel).queryByRole("button", { name: "Register this computer" }),
  ).toBeNull();
  fireEvent.click(
    within(panel).getByRole("button", { name: "Stop local Worker" }),
  );
  fireEvent.click(
    within(panel).getByRole("button", { name: "Confirm Worker stop" }),
  );
  await within(panel).findByText(/original stop is unconfirmed/);
  view.rerender(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  await waitFor(() =>
    expect(
      (
        within(panel).getByRole("button", {
          name: "Retry original Worker stop",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(
    within(panel).getByRole("button", { name: "Retry original Worker stop" }),
  );
  await waitFor(() =>
    expect(
      value.retainedWorker.mock.calls.filter(
        ([, action]) => action === LocalWorkerAction.Stop,
      ),
    ).toHaveLength(2),
  );
  expect(
    value.retainedWorker.mock.calls.filter(
      ([, action]) => action === LocalWorkerAction.Stop,
    ),
  ).toEqual([
    [value.profile.id, LocalWorkerAction.Stop, generation],
    [value.profile.id, LocalWorkerAction.Stop, generation],
  ]);
  expect(value.open).not.toHaveBeenCalled();
});

it("requires explicit first history read, appends with exact cursor and fences hidden late responses", async () => {
  const value = fixture();
  const removed = {
    ...value.profile,
    state: SavedConnectionState.Removed,
    name: "First removed",
  };
  let resolve!: (page: {
    connections: SavedConnection[];
    next_after?: string;
  }) => void;
  value.removed.mockResolvedValueOnce({
    connections: [removed],
    next_after: removed.id,
  } as { connections: SavedConnection[] });
  value.removed.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  expect(value.removed).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Show removed connections" }),
  );
  await screen.findByRole("heading", { name: "First removed" });
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Removed connections" }),
  );
  await waitFor(() =>
    expect(value.removed.mock.calls.map(([after]) => after)).toEqual([
      "",
      removed.id,
    ]),
  );
  view.rerender(
    <SavedConnections
      visible={false}
      close={() => {}}
      actions={value.actions}
    />,
  );
  resolve({
    connections: [{ ...removed, id: newRequestId(), name: "Late removed" }],
  });
  await Promise.resolve();
  expect(
    screen.queryByRole("heading", { name: "Late removed", hidden: true }),
  ).toBeNull();
  expect(value.retainedWorker).not.toHaveBeenCalled();
  expect(value.open).not.toHaveBeenCalled();
});

// A host slot appearing is not an explicit presentation request.
const openDialog = () =>
  [...document.querySelectorAll("dialog")].find((node) =>
    node.hasAttribute("open"),
  );
it("keeps a retained task heading immediate when its Connections slot appears", async () => {
  const value = fixture();
  const view = render(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.click(screen.getByRole("button", { name: /^Rename Saved server/ }));
  const target = document.createElement("div");
  document.body.append(target);
  view.rerender(
    <SavedConnections
      visible
      close={() => {}}
      actions={value.actions}
      target={target}
    />,
  );
  await waitFor(() => expect(openDialog()).toBeTruthy());
  expect(
    openDialog()!.querySelector("header")!.getAttribute("data-entrance"),
  ).toBeNull();
  target.remove();
});

it("plays a Saved server task heading once for an explicit request inside the slot", async () => {
  const value = fixture();
  const target = document.createElement("div");
  document.body.append(target);
  render(
    <SavedConnections
      visible
      close={() => {}}
      actions={value.actions}
      target={target}
    />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.click(screen.getByRole("button", { name: /^Rename Saved server/ }));
  await waitFor(() => expect(openDialog()).toBeTruthy());
  const open = openDialog()!;
  expect(open.querySelector("header")!.getAttribute("data-entrance")).toBe(
    "y4",
  );
  expect(open.hasAttribute("data-entrance")).toBe(false);
  target.remove();
});

// Dismissal belongs to the retained task, not to the host that displayed it.
const dismissedTasks = [
  {
    open: /^Add server$/,
    title: "Add a server connection",
    field: "Connection name",
  },
  {
    open: /^Rename Saved server/,
    title: "Rename Saved server",
    field: "New connection name",
  },
  {
    open: /^Remove Saved server/,
    title: "Confirm connection removal",
    field: undefined,
  },
];
const bodyHidden = (node: HTMLElement) => Boolean(node.closest("[hidden]"));
it.each(
  dismissedTasks.flatMap((task) =>
    ["Close", "Escape"].map((dismissal) => ({ ...task, dismissal })),
  ),
)(
  "keeps dismissed $title hidden in the fallback dialog after $dismissal until Continue",
  async ({ open, title, field, dismissal }) => {
    const value = fixture();
    const target = document.createElement("div");
    document.body.append(target);
    const view = render(
      <SavedConnections
        visible
        close={() => {}}
        actions={value.actions}
        target={target}
      />,
    );
    await screen.findByRole("button", { name: "Open Saved server" });
    fireEvent.click(screen.getByRole("button", { name: open }));
    const dialog = screen.getByRole("dialog", {
      name: title,
    }) as HTMLDialogElement;
    const input = field
      ? (screen.getByLabelText(field) as HTMLInputElement)
      : undefined;
    if (input) fireEvent.change(input, { target: { value: "Retained draft" } });
    if (dismissal === "Close")
      fireEvent.click(screen.getByRole("button", { name: `Close ${title}` }));
    else fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(dialog.open).toBe(false);
    // Move to the main-window fallback host.
    view.rerender(
      <SavedConnections visible close={() => {}} actions={value.actions} />,
    );
    target.remove();
    expect(dialog.open).toBe(false);
    if (input) expect(bodyHidden(input)).toBe(true);
    else
      expect(
        bodyHidden(screen.getByText(/closes all windows/i, { exact: false })),
      ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: `Continue ${title}` }));
    expect(
      screen.queryByRole("button", { name: `Continue ${title}` }),
    ).toBeNull();
    if (input) {
      expect(screen.getByLabelText(field!)).toBe(input);
      expect(bodyHidden(input)).toBe(false);
      expect(input.value).toBe("Retained draft");
    } else {
      expect(
        bodyHidden(screen.getByText(/closes all windows/i, { exact: false })),
      ).toBe(false);
    }
    expect(screen.queryAllByRole("button", { name: /Continue/ })).toHaveLength(
      0,
    );
    expect(value.pair).not.toHaveBeenCalled();
    expect(value.rename).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
    expect(value.open).not.toHaveBeenCalled();
  },
);
it("retains an in-flight pairing when its dismissed Add moves to the fallback dialog", async () => {
  const value = fixture();
  let complete!: () => void;
  value.pair.mockImplementation(
    (id: string) =>
      new Promise((resolve) => {
        complete = () => resolve({ ...value.profile, id });
      }),
  );
  const target = document.createElement("div");
  document.body.append(target);
  const view = render(
    <SavedConnections
      visible
      close={() => {}}
      actions={value.actions}
      target={target}
    />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  const name = screen.getByLabelText("Connection name") as HTMLInputElement;
  fireEvent.change(name, { target: { value: "Pending server" } });
  fireEvent.change(screen.getByLabelText("Private client pairing document"), {
    target: { value: value.grant },
  });
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  expect(value.pair).toHaveBeenCalledTimes(1);
  const original = value.pair.mock.calls[0];
  fireEvent.click(
    screen.getByRole("button", { name: "Close Add a server connection" }),
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  target.remove();
  expect(bodyHidden(name)).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Continue Add a server connection" }),
  );
  expect(bodyHidden(name)).toBe(false);
  expect(name.value).toBe("Pending server");
  expect(name.disabled).toBe(true);
  expect(value.pair).toHaveBeenCalledTimes(1);
  expect(value.pair.mock.calls[0]).toEqual(original);
  complete();
  await screen.findByText(/client pairing is saved/);
  expect(value.pair).toHaveBeenCalledTimes(1);
});
it("does not leave a completed dismissed Add hidden for the fallback dialog", async () => {
  const value = fixture();
  let complete!: () => void;
  value.pair.mockImplementation(
    (id: string) =>
      new Promise((resolve) => {
        complete = () => resolve({ ...value.profile, id });
      }),
  );
  const target = document.createElement("div");
  document.body.append(target);
  const view = render(
    <SavedConnections
      visible
      close={() => {}}
      actions={value.actions}
      target={target}
    />,
  );
  await screen.findByRole("button", { name: "Open Saved server" });
  fireEvent.click(screen.getByRole("button", { name: "Add server" }));
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "Pending server" },
  });
  fireEvent.change(screen.getByLabelText("Private client pairing document"), {
    target: { value: value.grant },
  });
  fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Close Add a server connection" }),
  );
  complete();
  await waitFor(() => expect(screen.queryByText(/saving/i)).toBeNull());
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "Add server" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
  view.rerender(
    <SavedConnections visible close={() => {}} actions={value.actions} />,
  );
  target.remove();
  expect(bodyHidden(screen.getByLabelText("Connection name"))).toBe(false);
  expect(
    screen.queryByRole("button", { name: "Continue Add a server connection" }),
  ).toBeNull();
});
it("keeps the initial fallback Add form available without a prior task", async () => {
  const value = fixture();
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  await screen.findByRole("button", { name: "Open Saved server" });
  expect(bodyHidden(screen.getByLabelText("Connection name"))).toBe(false);
  expect(screen.queryByRole("button", { name: /^Continue/ })).toBeNull();
  expect(value.pair).not.toHaveBeenCalled();
});

it("marks the saved connection removal opener, confirmation and retry Danger while Keep and Show removed stay neutral", async () => {
  const value = fixture();
  value.remove.mockRejectedValueOnce("timed-out");
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  const opener = await screen.findByRole("button", {
    name: "Remove Saved server",
  });
  expectTone(opener, danger);
  expectTone(
    screen.getByRole("button", { name: "Show removed connections" }),
    neutral,
  );
  fireEvent.click(opener);
  expect(value.remove).not.toHaveBeenCalled();
  const confirm = screen.getByRole("button", {
    name: "Confirm connection removal",
  });
  expectTone(confirm, danger);
  expectTone(screen.getByRole("button", { name: "Keep connection" }), neutral);
  fireEvent.click(confirm);
  await screen.findByText(/operation has not been confirmed/);
  expectTone(
    screen.getByRole("button", { name: "Retry original connection removal" }),
    danger,
  );
  expectTone(
    screen.getByRole("button", { name: "Inspect removal state later" }),
    neutral,
  );
});

it("marks the Removing-state retry opener Danger because it retries the retained deletion", async () => {
  const value = fixture();
  value.list.mockResolvedValue([
    {
      ...value.profile,
      state: SavedConnectionState.Removing,
      revision: 2,
      removal: { request_id: newRequestId(), expected_revision: 1 },
    },
  ]);
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  const opener = await screen.findByRole("button", {
    name: "Retry removal of Saved server",
  });
  expectTone(opener, danger);
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(opener);
  expectTone(
    screen.getByRole("button", { name: "Retry original connection removal" }),
    danger,
  );
  expectTone(
    screen.getByRole("button", { name: "Inspect removal state later" }),
    neutral,
  );
});

// Enter submission: Rename and Add server are semantic forms whose inline primary
// is the only submit control. Pointer, Enter and form submission share one
// guarded owner callback per form.
const nameInput = () =>
  screen.getByLabelText("Connection name") as HTMLInputElement;
const grantInput = () =>
  screen.getByLabelText("Private client pairing document") as HTMLInputElement;
const newNameInput = () =>
  screen.getByLabelText("New connection name") as HTMLInputElement;
const formOf = (control: HTMLElement) =>
  control.closest("form") as HTMLFormElement;
const submitters = (form: HTMLFormElement) =>
  [...form.querySelectorAll("button")].filter(
    (button) => button.type === "submit",
  );
async function renderAdd(value: ReturnType<typeof fixture>) {
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  await screen.findByRole("button", { name: "Open Saved server" });
}
function fillAdd(value: ReturnType<typeof fixture>) {
  fireEvent.change(nameInput(), { target: { value: "New server" } });
  fireEvent.change(grantInput(), { target: { value: value.grant } });
}
async function openRename(value: ReturnType<typeof fixture>) {
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Rename Saved server" }),
  );
}

it("owns Add server as one semantic form with a single inline submit control", async () => {
  const value = fixture();
  await renderAdd(value);
  fillAdd(value);
  const form = formOf(nameInput());
  expect(form.id).not.toBe("");
  expect(form.noValidate).toBe(true);
  expect(form.querySelector("form")).toBeNull();
  expect(formOf(grantInput())).toBe(form);
  const primary = screen.getByRole("button", {
    name: "Pair this server",
  }) as HTMLButtonElement;
  expect(primary.form).toBe(form);
  expect(submitters(form)).toEqual([primary]);
  for (const button of form.querySelectorAll("button"))
    if (button !== primary) expect(button.type).toBe("button");
});

it.each([
  ["Connection name", nameInput],
  ["Private client pairing document", grantInput],
])(
  "pairs a server once with Enter from %s using the pointer payload",
  async (_label, field) => {
    const clicked = fixture();
    await renderAdd(clicked);
    fillAdd(clicked);
    fireEvent.click(screen.getByRole("button", { name: "Pair this server" }));
    await waitFor(() => expect(clicked.pair).toHaveBeenCalledTimes(1));
    await screen.findByText(/client pairing is saved/);
    const expected = clicked.pair.mock.calls[0];
    cleanup();

    const value = fixture();
    await renderAdd(value);
    fillAdd(value);
    expect(pressEnter(field())).toBe(1);
    await waitFor(() => expect(value.pair).toHaveBeenCalledTimes(1));
    await screen.findByText(/client pairing is saved/);
    const call = value.pair.mock.calls[0];
    // The fixture grant embeds random identifiers, so compare each against its own.
    expect(expected[1]).toBe("New server");
    expect(expected[2]).toBe(clicked.grant);
    expect(call[1]).toBe(expected[1]);
    expect(call[2]).toBe(value.grant);
    // Every submission allocates its own request identity.
    expect(call[0]).toMatch(/^[0-9a-f-]{36}$/);
    expect(call[0]).not.toBe(expected[0]);
    expect(value.retry).not.toHaveBeenCalled();
    expect(value.open).not.toHaveBeenCalled();
    expect(value.rename).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
  },
);

it("does not pair with Enter while the draft is incomplete or invalid", async () => {
  const value = fixture();
  await renderAdd(value);
  const primary = screen.getByRole("button", {
    name: "Pair this server",
  }) as HTMLButtonElement;
  expect(primary.disabled).toBe(true);
  expect(pressEnter(nameInput())).toBe(0);
  fireEvent.change(nameInput(), { target: { value: "New server" } });
  expect(pressEnter(nameInput())).toBe(0);
  fireEvent.change(grantInput(), { target: { value: "{not-json" } });
  expect(pressEnter(grantInput())).toBe(0);
  // A script-initiated submission reaches the same guarded callback.
  fireEvent.submit(formOf(nameInput()));
  fireEvent.change(nameInput(), { target: { value: "   " } });
  fireEvent.change(grantInput(), { target: { value: value.grant } });
  expect(pressEnter(grantInput())).toBe(0);
  fireEvent.submit(formOf(grantInput()));
  expect(value.pair).not.toHaveBeenCalled();
});

it.each([
  ["composition", { composing: true }],
  ["legacy composition key code", { keyCode: 229 }],
  ["held Enter", { repeat: true }],
  ["Shift+Enter", { shiftKey: true }],
  ["Control+Enter", { ctrlKey: true }],
  ["Alt+Enter", { altKey: true }],
  ["Meta+Enter", { metaKey: true }],
])("does not pair on %s", async (_label, press) => {
  const value = fixture();
  await renderAdd(value);
  fillAdd(value);
  expect(pressEnter(nameInput(), press)).toBe(0);
  expect(pressEnter(grantInput(), press)).toBe(0);
  expect(value.pair).not.toHaveBeenCalled();
  expect(nameInput().value).toBe("New server");
  expect(grantInput().value).toBe(value.grant);
});

it("does not pair with Enter while a rename or removal blocks the primary control", async () => {
  const value = fixture();
  render(<SavedConnections visible close={() => {}} actions={value.actions} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Rename Saved server" }),
  );
  fillAdd(value);
  expect(
    (
      screen.getByRole("button", {
        name: "Pair this server",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(pressEnter(nameInput())).toBe(0);
  fireEvent.submit(formOf(nameInput()));
  expect(value.pair).not.toHaveBeenCalled();
});

it("keeps the exact uncertain pairing retry through form submission and never replaces it", async () => {
  const value = fixture();
  value.pair.mockRejectedValueOnce("timed-out");
  await renderAdd(value);
  fillAdd(value);
  expect(pressEnter(grantInput())).toBe(1);
  await screen.findByText(/operation has not been confirmed/);
  expect(nameInput().disabled).toBe(true);
  expect(grantInput().disabled).toBe(true);
  // The retained attempt changes the primary into an exact retry. Submitting the
  // form again re-sends the original identity, name and private grant.
  const retry = screen.getByRole("button", {
    name: "Retry original server pairing",
  }) as HTMLButtonElement;
  expect(retry.type).toBe("submit");
  fireEvent.submit(formOf(nameInput()));
  await screen.findByText(/client pairing is saved/);
  expect(value.pair).toHaveBeenCalledTimes(2);
  expect(value.pair.mock.calls[0]).toEqual(value.pair.mock.calls[1]);
  expect(value.pair.mock.calls[1][2]).toBe(value.grant);
});

it("does not pair twice when the primary is submitted again while pairing is pending", async () => {
  const value = fixture();
  let release: () => void = () => {};
  value.pair.mockImplementationOnce(
    (id) =>
      new Promise((resolve) => {
        release = () => resolve({ ...value.profile, id });
      }),
  );
  await renderAdd(value);
  fillAdd(value);
  expect(pressEnter(nameInput())).toBe(1);
  await waitFor(() => expect(value.pair).toHaveBeenCalledTimes(1));
  expect(pressEnter(nameInput())).toBe(0);
  fireEvent.submit(formOf(nameInput()));
  release();
  await screen.findByText(/client pairing is saved/);
  expect(value.pair).toHaveBeenCalledTimes(1);
});

it("owns Rename as one semantic form whose Discard control stays a plain button", async () => {
  const value = fixture();
  await openRename(value);
  const form = formOf(newNameInput());
  expect(form.id).not.toBe("");
  expect(form.noValidate).toBe(true);
  expect(form.querySelector("form")).toBeNull();
  const save = screen.getByRole("button", {
    name: "Save connection name",
  }) as HTMLButtonElement;
  expect(save.form).toBe(form);
  expect(submitters(form)).toEqual([save]);
  const discard = screen.getByRole("button", {
    name: "Discard name edit",
  }) as HTMLButtonElement;
  expect(discard.form).toBe(form);
  expect(discard.type).toBe("button");
  fireEvent.click(discard);
  expect(value.rename).not.toHaveBeenCalled();
  expect(screen.queryByLabelText("New connection name")).toBeNull();
});

it("renames once with Enter using the pointer payload", async () => {
  const clicked = fixture();
  await openRename(clicked);
  fireEvent.change(newNameInput(), { target: { value: "Renamed server" } });
  fireEvent.click(screen.getByRole("button", { name: "Save connection name" }));
  await waitFor(() => expect(clicked.rename).toHaveBeenCalledTimes(1));
  await screen.findByText(/Current connection name: Renamed server/);
  const expected = clicked.rename.mock.calls[0];
  cleanup();

  const value = fixture();
  await openRename(value);
  fireEvent.change(newNameInput(), { target: { value: "Renamed server" } });
  expect(pressEnter(newNameInput())).toBe(1);
  await waitFor(() => expect(value.rename).toHaveBeenCalledTimes(1));
  await screen.findByText(/Current connection name: Renamed server/);
  const call = value.rename.mock.calls[0];
  expect(call).toEqual([
    value.profile.id,
    expect.stringMatching(/^[0-9a-f-]{36}$/),
    1,
    "Renamed server",
  ]);
  expect(call.slice(2)).toEqual(expected.slice(2));
  expect(call[1]).not.toBe(expected[1]);
  expect(value.pair).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("does not rename with Enter for a blank, oversized or stale name", async () => {
  const value = fixture();
  await openRename(value);
  fireEvent.change(newNameInput(), { target: { value: "   " } });
  expect(pressEnter(newNameInput())).toBe(0);
  fireEvent.submit(formOf(newNameInput()));
  fireEvent.change(newNameInput(), { target: { value: "é".repeat(129) } });
  expect(pressEnter(newNameInput())).toBe(0);
  fireEvent.submit(formOf(newNameInput()));
  fireEvent.change(newNameInput(), { target: { value: "My draft" } });
  value.list.mockResolvedValue([
    { ...value.profile, revision: 2, name: "Peer edit" },
  ]);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh saved servers" }),
  );
  await screen.findByText(/This connection changed while you were editing/);
  expect(pressEnter(newNameInput())).toBe(0);
  fireEvent.submit(formOf(newNameInput()));
  expect(value.rename).not.toHaveBeenCalled();
  expect(newNameInput().value).toBe("My draft");
});

it.each([
  ["composition", { composing: true }],
  ["legacy composition key code", { keyCode: 229 }],
  ["held Enter", { repeat: true }],
  ["Shift+Enter", { shiftKey: true }],
])("does not rename on %s", async (_label, press) => {
  const value = fixture();
  await openRename(value);
  fireEvent.change(newNameInput(), { target: { value: "Renamed server" } });
  expect(pressEnter(newNameInput(), press)).toBe(0);
  expect(value.rename).not.toHaveBeenCalled();
  expect(newNameInput().value).toBe("Renamed server");
});

it("keeps the exact uncertain rename retry through form submission", async () => {
  const value = fixture();
  value.rename.mockRejectedValueOnce("timed-out");
  await openRename(value);
  fireEvent.change(newNameInput(), { target: { value: "First edit" } });
  expect(pressEnter(newNameInput())).toBe(1);
  await screen.findByText(/operation has not been confirmed/);
  expect(newNameInput().disabled).toBe(true);
  fireEvent.submit(formOf(newNameInput()));
  await screen.findByText(/Current connection name: First edit/);
  expect(value.rename).toHaveBeenCalledTimes(2);
  expect(value.rename.mock.calls[0]).toEqual(value.rename.mock.calls[1]);
});
