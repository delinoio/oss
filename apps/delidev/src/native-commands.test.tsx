// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  EntityKind,
  ResourceSchema,
  SystemQuery,
  ResourceQuery,
  SystemCapability,
  NativeShellAccess,
} from "@delinoio/delidev-api-client";
import {
  fireEvent,
  render,
  screen,
  within,
  cleanup,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  NativeCommands,
  commandTimeout,
  nativeCommandEligible,
  nativeCommandRecord,
} from "./native-commands";
import { encode } from "./documents";

const sessionId = "019d0000-0000-7000-8000-000000000001";
const actionId = "019d0000-0000-7000-8000-000000000002";
const jobId = "019d0000-0000-7000-8000-000000000003";
const executionId = "019d0000-0000-7000-8000-000000000004";
const machineId = "019d0000-0000-7000-8000-000000000005";
const fixture = vi.hoisted(() => ({
  send: vi.fn(),
  saved: undefined as object | undefined,
}));
vi.mock("./mutation", () => ({
  useRetainedMutationPresentation: () => ({
    value: undefined,
    read: () => fixture.saved,
    save: (value: object) => {
      fixture.saved = value;
    },
  }),
  useRetainedMutation: () => ({
    send: fixture.send,
    busy: false,
    uncertain: false,
  }),
}));
vi.mock("@connectrpc/connect-query", () => ({
  useQuery: (method: unknown) => ({
    data:
      method === SystemQuery.getStatus
        ? { capabilities: [SystemCapability.CODEX_NATIVE_SHELL_V1] }
        : method === ResourceQuery.getResource
          ? {
              resource: create(ResourceSchema, {
                id: machineId,
                kind: EntityKind.MACHINE,
                schemaVersion: 1,
                revision: 1n,
                documentJson: encode({
                  worker_capabilities: ["codex-native-shell-v1"],
                }),
              }),
            }
          : undefined,
    refetch: vi.fn(),
    isFetching: false,
  }),
}));
const sessionDocument = {
  machine_id: machineId,
  archive: "active",
  recovery: "none",
  dispatch: "ready",
  outcome: "succeeded",
  initial_execution: { configuration: { harness: "codex" } },
  execution: { cleanup_verified: true },
};
const session = (value = sessionDocument) =>
  create(ResourceSchema, {
    id: sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 9n,
    documentJson: encode(value),
  });
const action = (patch: object = {}) =>
  create(ResourceSchema, {
    id: actionId,
    sessionId,
    kind: EntityKind.NATIVE_ACTION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      version: 1,
      action_id: actionId,
      session_id: sessionId,
      execution_id: executionId,
      job_id: jobId,
      command: { command: "printf original", full_access: true },
      disclosure: "Full access",
      state: "queued",
      ...patch,
    }),
  });
beforeEach(() => {
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
  fixture.saved = undefined;
});

describe("explicit native commands", () => {
  it("preserves default, immediate and bounded explicit timeout semantics", () => {
    expect(commandTimeout("")).toBeUndefined();
    expect(commandTimeout("0")).toBe(0n);
    expect(commandTimeout("3600")).toBe(3600000n);
    for (const value of ["-1", "1.5", "3601", "1e3", "1; exit"])
      expect(commandTimeout(value)).toBeNull();
  });
  it("rejects busy, recovery, foreign, terminal and missing full-access evidence", () => {
    expect(nativeCommandEligible(session(), true)).toBe(true);
    expect(nativeCommandEligible(session(), false)).toBe(false);
    for (const patch of [
      { active_execution_id: executionId },
      { compaction_job_id: jobId },
      { pending_inputs: 1 },
      { recovery: "required" },
      { execution: { cleanup_verified: false } },
    ])
      expect(
        nativeCommandEligible(session({ ...sessionDocument, ...patch }), true),
      ).toBe(false);
    expect(nativeCommandRecord(action(), sessionId, actionId)).toBeDefined();
    expect(
      nativeCommandRecord(
        action({ command: { command: "a".repeat(65536), full_access: true } }),
        sessionId,
        actionId,
      ),
    ).toBeDefined();
    expect(
      nativeCommandRecord(
        action({ command: { command: "a".repeat(65537), full_access: true } }),
        sessionId,
        actionId,
      ),
    ).toBeUndefined();
    expect(nativeCommandRecord(action(), machineId, actionId)).toBeUndefined();
    expect(
      nativeCommandRecord(action(), sessionId, executionId),
    ).toBeUndefined();
    expect(
      nativeCommandRecord(
        action({ command: { command: "printf original", full_access: false } }),
        sessionId,
        actionId,
      ),
    ).toBeUndefined();
    expect(
      nativeCommandRecord(
        create(ResourceSchema, { ...action(), kind: EntityKind.TERMINAL }),
        sessionId,
        actionId,
      ),
    ).toBeUndefined();
  });
  it("requires the separate confirmation click and preserves zero timeout", () => {
    render(<NativeCommands session={session()} active close={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Command"), {
      target: { value: "  printf original  " },
    });
    fireEvent.change(screen.getByLabelText("Timeout seconds (optional)"), {
      target: { value: "0" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(fixture.send).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/outside the thread sandbox/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Run" }));
    expect(fixture.send).toHaveBeenCalledTimes(1);
    expect(fixture.send.mock.calls[0][0]).toMatchObject({
      command: "  printf original  ",
      timeoutMs: 0n,
      access: NativeShellAccess.FULL_ACCESS,
      mutation: { id: sessionId, expectedRevision: 9n },
    });
  });
  it("dismisses confirmation without authorizing a command", () => {
    render(<NativeCommands session={session()} active close={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Command"), {
      target: { value: "printf original" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Cancel",
      }),
    );
    expect(fixture.send).not.toHaveBeenCalled();
  });
});
