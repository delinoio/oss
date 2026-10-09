// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { fireEvent, render, screen, waitFor, act } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { RunnerTaskRemediation } from "./session-runner-remediation";
const { open } = vi.hoisted(() => ({ open: vi.fn() }));
vi.mock("./runner-remediation", () => ({ useRunnerRemediation: () => open }));
beforeEach(() => { open.mockClear(); Object.assign(open, { locked: false, pending: false, pendingFor: undefined }); });
function fixture() {
 const id = newRequestId(), other = newRequestId();
 const machine = create(ResourceSchema, { id, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name: "Original Runner" }) });
 const read = vi.fn(async (_request: unknown) => ({ resource: machine }));
 const transport = createRouterTransport(router => router.service(ResourceService, { getResource: read }));
 const view = (value = id, active = true) => <TransportProvider transport={transport}><RunnerTaskRemediation active={active} machineId={value} /></TransportProvider>;
 return { id, other, machine, read, view };
}
it("reads only on explicit action and passes exact original Runner evidence", async () => {
 const f = fixture(); render(f.view());
 expect(f.read).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Inspect this original Runner" }));
 await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
 expect(f.read.mock.calls[0][0]).toMatchObject({ kind: EntityKind.MACHINE, id: f.id });
 expect(open.mock.calls[0][0].revision).toBe(9007199254740993n);
});
it.each(["foreign", "zero", "malformed"])("rejects %s original Runner observations and leaves explicit read retry", async fault => {
 const f = fixture(); f.read.mockResolvedValue({ resource: create(ResourceSchema, { ...f.machine, ...(fault === "foreign" ? { id: f.other } : fault === "zero" ? { revision: 0n } : { documentJson: new TextEncoder().encode("{private-native-output") }) }) });
 render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Inspect this original Runner" }));
 await screen.findByText(/original Runner observation is unreadable/);
 expect(open).not.toHaveBeenCalled();
 expect(screen.getByRole("button", { name: "Inspect this original Runner" })).toHaveProperty("disabled", false);
});
it("fences a late original read after disposal and after identity departure and return", async () => {
 const f = fixture(); let resolve!: (value: { resource: typeof f.machine }) => void;
 f.read.mockImplementation(() => new Promise(done => { resolve = done; }));
 const rendered = render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Inspect this original Runner" }));
 await waitFor(() => expect(f.read).toHaveBeenCalledTimes(1));
 rendered.rerender(f.view(f.other)); rendered.rerender(f.view());
 await act(async () => { resolve({ resource: f.machine }); });
 expect(open).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Inspect this original Runner" }));
 await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2)); rendered.unmount();
 await act(async () => { resolve({ resource: f.machine }); });
 expect(open).not.toHaveBeenCalled();
});

it("fences a read when the owning task suspends and returns", async () => {
 const f = fixture(); let resolve!: (value: { resource: typeof f.machine }) => void;
 f.read.mockImplementation(() => new Promise(done => { resolve = done; }));
 const rendered = render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Inspect this original Runner" }));
 await waitFor(() => expect(f.read).toHaveBeenCalledTimes(1));
 rendered.rerender(f.view(f.id, false)); rendered.rerender(f.view());
 await act(async () => { resolve({ resource: f.machine }); });
 expect(open).not.toHaveBeenCalled();
});

it("holds a different retained Runner operation and reports exact original pending ownership", async () => {
 const f = fixture(); const pending = vi.fn();
 Object.assign(open, { locked: true, pendingFor: (id: string) => id === f.other });
 const view = (id: string) => <TransportProvider transport={createRouterTransport(router => router.service(ResourceService, { getResource: f.read }))}><RunnerTaskRemediation machineId={id} onPending={pending} /></TransportProvider>;
 const rendered = render(view(f.id));
 expect(screen.getByRole("button", { name: "Inspect this original Runner" })).toHaveProperty("disabled", true);
 expect(screen.getByText(/Another original Runner inspection is pending/)).toBeTruthy();
 expect(pending).toHaveBeenLastCalledWith(false);
 rendered.rerender(view(f.other));
 await waitFor(() => expect(pending).toHaveBeenLastCalledWith(true));
 expect(f.read).not.toHaveBeenCalled();
});
