// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceQuery, SystemCapability, SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionActions } from "./session-presentation";
import { NativeShellAction } from "./native-shell-action";

const state = vi.hoisted(() => ({ sends: [] as unknown[], uncertain: false }));
vi.mock("./mutation", () => ({
  useRetainedMutation: (key: string) => ({ busy: false, uncertain: state.uncertain && key.startsWith("native-shell:"), error: undefined, send: (request: unknown) => { state.sends.push(request); }, acceptObserved: () => false }),
  useRetainedMutationIntents: () => [],
}));
vi.mock("@connectrpc/connect-query", async importOriginal => {
  const original = await importOriginal<typeof import("@connectrpc/connect-query")>();
  return { ...original, useQuery: (method: unknown) => ({
    data: method === SystemQuery.getStatus ? { capabilities: [SystemCapability.CODEX_NATIVE_SHELL_V1] } : method === ResourceQuery.getResource ? { resource: create(ResourceSchema, { id: machineId, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({ worker_capabilities: ["codex-native-shell-v1"] }) }) } : undefined,
    isPending: false, isFetching: false, error: undefined, refetch: vi.fn(),
  }) };
});
const machineId = newRequestId();
function fixture() {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Original session", machine_id: machineId, initial_execution: { configuration: { harness: "codex" } }, execution: { native_thread_id: "original-thread", cleanup_verified: true }, archive: "active", recovery: "none" }) });
}

test("opening Shell cannot execute; exact command and explicit full-access confirmation authorize one user send", () => {
  state.sends = []; state.uncertain = false;
  const session = fixture();
  render(<SessionActions><NativeShellAction session={session} active blocked={false} /></SessionActions>);
  fireEvent.click(screen.getByRole("button", { name: "Session actions" }));
  fireEvent.click(screen.getByRole("button", { name: "Shell" }));
  expect(screen.getByText(/outside the thread sandbox/)).toBeDefined();
  const command = screen.getByRole("textbox", { name: "Command" });
  expect(document.activeElement).toBe(command);
  expect(state.sends).toHaveLength(0);
  fireEvent.change(command, { target: { value: "  printf hello  " } });
  const run = screen.getByRole("button", { name: "Run command" });
  expect((run as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(run);
  expect(state.sends).toEqual([expect.objectContaining({ mutation: expect.objectContaining({ id: session.id, expectedRevision: 1n }), command: "  printf hello  ", fullAccessConfirmed: true })]);
  expect(screen.getByRole("checkbox").getAttribute("checked")).toBeNull();
});

test("source revision changes and uncertain original delivery block fresh execution; Escape preserves native work", () => {
  state.sends = []; state.uncertain = false;
  const session = fixture(), view = render(<NativeShellAction session={session} active blocked={false} />);
  const opener = screen.getByRole("button", { name: "Shell" });
  opener.focus(); fireEvent.click(opener);
  fireEvent.change(screen.getByRole("textbox", { name: "Command" }), { target: { value: "printf hello" } });
  fireEvent.click(screen.getByRole("checkbox"));
  view.rerender(<NativeShellAction session={create(ResourceSchema, { ...session, revision: 2n })} active blocked={false} />);
  expect((screen.getByRole("button", { name: "Run command" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText(/original session changed/)).toBeDefined();
  state.uncertain = true;
  view.rerender(<NativeShellAction session={session} active blocked={false} />);
  expect(screen.getByText(/do not send the command again/)).toBeDefined();
  const dialog = screen.getByRole("dialog");
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
  expect(state.sends).toHaveLength(0);
});
