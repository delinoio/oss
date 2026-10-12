// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  EntityKind,
  NativeBackgroundOperation,
  ResourceSchema,
} from "@delinoio/delidev-api-client";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { encode } from "./documents";
import {
  NativeBackground,
  nativeBackgroundAction,
  nativeBackgroundEligible,
  nativeBackgroundInventory,
} from "./native-background";
const sessionId = "019d0000-0000-7000-8000-000000000001",
  actionId = "019d0000-0000-7000-8000-000000000002",
  jobId = "019d0000-0000-7000-8000-000000000003",
  executionId = "019d0000-0000-7000-8000-000000000004",
  threadId = "019d0000-0000-7000-8000-000000000005";
const fixture = vi.hoisted(() => ({
  send: vi.fn(),
  refetch: vi.fn(),
  data: undefined as unknown,
  saved: undefined as object | undefined,
  uncertain: false,
}));
vi.mock("@connectrpc/connect-query", () => ({
  useQuery: () => ({
    data: fixture.data,
    refetch: fixture.refetch,
    isFetching: false,
  }),
}));
vi.mock("./mutation", () => ({
  useRetainedMutationPresentation: () => ({
    value: fixture.saved,
    read: () => fixture.saved,
    save: (value: object) => {
      fixture.saved = value;
    },
  }),
  useRetainedMutation: () => ({
    send: fixture.send,
    busy: false,
    uncertain: fixture.uncertain,
  }),
}));
const terminal = {
  item_id: "original-item",
  process_id: "500",
  command: "npm run original",
  cwd: "/original",
  output: "original output",
  output_truncated: false,
  present: true,
};
const inventory = {
  version: 1 as const,
  job_id: jobId,
  execution_id: executionId,
  native_thread_id: threadId,
  sequence: "1",
  terminals: [terminal],
  owner_lost: false,
  native_empty: false,
  actions: 0,
};
const session = (patch: object = {}) =>
  create(ResourceSchema, {
    id: sessionId,
    kind: EntityKind.SESSION,
    revision: 9n,
    schemaVersion: 1,
    documentJson: encode({
      archive: "active",
      active_execution_id: executionId,
      execution: {
        execution_id: executionId,
        job_id: jobId,
        native_thread_id: threadId,
        cleanup_verified: false,
      },
      native_background: inventory,
      ...patch,
    }),
  });
const action = (patch: object = {}) =>
  create(ResourceSchema, {
    id: actionId,
    sessionId,
    kind: EntityKind.NATIVE_ACTION,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      version: 1,
      action_type: "native-background",
      action_id: actionId,
      session_id: sessionId,
      job_id: jobId,
      execution_id: executionId,
      native_thread_id: threadId,
      inventory_sequence: "1",
      operation: "list",
      targets: [],
      state: "queued",
      ...patch,
    }),
  });
beforeEach(() => {
  fixture.data = { session: session() };
  fixture.uncertain = false;
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
});
afterEach(() => {
  cleanup();
  fixture.send.mockClear();
  fixture.refetch.mockClear();
  fixture.saved = undefined;
});
describe("original native background controls", () => {
  it("rejects wrong ownership, duplicate or oversized inventory and invalid targets", () => {
    expect(nativeBackgroundInventory(session(), sessionId)).toEqual(inventory);
    for (const patch of [
      { terminals: [terminal, terminal] },
      { sequence: "18446744073709551616" },
      { terminals: [{ ...terminal, output: "x".repeat(16385) }] },
      { terminals: [{ ...terminal, process_id: "0500" }] },
      { native_empty: true },
    ])
      expect(
        nativeBackgroundInventory(
          session({ native_background: { ...inventory, ...patch } }),
          sessionId,
        ),
      ).toBeUndefined();
    expect(
      nativeBackgroundInventory({ ...session(), id: actionId }, sessionId),
    ).toBeUndefined();
    expect(nativeBackgroundAction(action(), sessionId, actionId)).toBeDefined();
    expect(
      nativeBackgroundAction(
        action({ operation: "terminate" }),
        sessionId,
        actionId,
      ),
    ).toBeUndefined();
    expect(
      nativeBackgroundAction(
        { ...action(), sessionId: threadId },
        sessionId,
        actionId,
      ),
    ).toBeUndefined();
  });
  it("requires original live execution and rejects retired or busy authority", () => {
    expect(nativeBackgroundEligible(session(), inventory, true)).toBe(true);
    expect(
      nativeBackgroundEligible(
        session(),
        { ...inventory, owner_lost: true },
        true,
      ),
    ).toBe(false);
    expect(
      nativeBackgroundEligible(
        session({ active_execution_id: actionId }),
        inventory,
        true,
      ),
    ).toBe(false);
    expect(
      nativeBackgroundEligible(
        session(),
        { ...inventory, pending_action_id: actionId },
        true,
      ),
    ).toBe(false);
  });
  it("mounts and inspects without controls, then explicitly stops exactly one target", () => {
    render(<NativeBackground session={session()} active supported />);
    expect(fixture.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Inspect output" }));
    expect(screen.getByText("original output")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Read status" }));
    expect(fixture.refetch).toHaveBeenCalledOnce();
    expect(fixture.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /^Stop$/ }));
    expect(fixture.send).toHaveBeenCalledOnce();
    expect(fixture.send.mock.calls[0][0]).toMatchObject({
      mutation: { id: sessionId, expectedRevision: 9n },
      executionId,
      inventorySequence: 1n,
      operation: NativeBackgroundOperation.TERMINATE,
      itemId: terminal.item_id,
      processId: terminal.process_id,
    });
  });
  it("requires confirmation for stop all and prevents stale confirmation", () => {
    const view = render(
      <NativeBackground session={session()} active supported />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Stop all" }));
    const modal = screen.getByRole("dialog");
    expect(document.activeElement).toBe(
      within(modal).getByRole("button", { name: "Stop all" }),
    );
    expect(fixture.send).not.toHaveBeenCalled();
    fixture.data = {
      session: session({ native_background: { ...inventory, sequence: "2" } }),
    };
    view.rerender(<NativeBackground session={session()} active supported />);
    expect(
      (
        within(modal).getByRole("button", {
          name: "Stop all",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.keyDown(modal, { key: "Escape" });
    expect(fixture.send).not.toHaveBeenCalled();
  });
  it("rejects a foreign execution receipt while the original intent is uncertain", () => {
    fixture.uncertain = true;
    fixture.saved = {
      pending: {
        action: actionId,
        execution: executionId,
        sequence: 1n,
        revision: 9n,
        operation: NativeBackgroundOperation.LIST,
      },
    };
    fixture.data = {
      session: session(),
      action: action({ execution_id: threadId }),
    };
    render(<NativeBackground session={session()} active supported />);
    expect(
      (screen.getByRole("button", { name: "Refresh" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(fixture.saved).toHaveProperty("pending");
    expect(fixture.send).not.toHaveBeenCalled();
  });
  it("retains uncertainty across remounts and status reads without replay", () => {
    fixture.uncertain = true;
    fixture.saved = {
      pending: {
        action: actionId,
        execution: executionId,
        sequence: 1n,
        revision: 9n,
        operation: NativeBackgroundOperation.CLEAN,
      },
    };
    const view = render(
      <NativeBackground session={session()} active supported />,
    );
    expect(
      (screen.getByRole("button", { name: "Stop all" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    view.unmount();
    render(<NativeBackground session={session()} active supported />);
    fireEvent.click(screen.getByRole("button", { name: "Read status" }));
    expect(fixture.send).not.toHaveBeenCalled();
  });
});
