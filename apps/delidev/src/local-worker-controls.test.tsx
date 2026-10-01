import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { LocalWorkerAction, LocalWorkerControls, LocalWorkerPresentation, LocalWorkerState, type LocalWorkerStatus } from "./local-worker-controls";

afterEach(cleanup);
const running = (): LocalWorkerStatus => ({ state: LocalWorkerState.Running, machine_id: newRequestId(), generation: newRequestId(), controller_active: true });
it("registers separately from explicit startup and never automatically retries an uncertain start", async () => {
  let value: LocalWorkerStatus | undefined;
  const control = vi.fn(async (action: LocalWorkerAction) => {
    if (action === LocalWorkerAction.Register) value = { ...running(), generation: undefined, state: LocalWorkerState.NotStarted, controller_active: false };
    if (action === LocalWorkerAction.Start) { value = { ...value!, generation: newRequestId(), state: LocalWorkerState.Starting, controller_active: true }; throw new Error("unknown launch"); }
    if (!value) throw new Error("not registered");
    return value;
  });
  render(<LocalWorkerControls control={control} active changed={() => {}} />);
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Register this computer" }));
  await screen.findByText("Registered on this computer; not started yet.");
  expect(control.mock.calls.filter(([action]) => action === LocalWorkerAction.Start)).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Start local Worker" }));
  await screen.findByText(/Starting; waiting for the server/);
  expect(screen.queryByRole("button", { name: "Start local Worker" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  await waitFor(() => expect(control.mock.calls.filter(([action]) => action === LocalWorkerAction.Start)).toHaveLength(1));
});
it.each(Object.values(LocalWorkerPresentation))("retains an uncertain original stop after the visible Worker generation changes (%s)", async presentation => {
  const original = running(), replacement = running();
  let value = original;
  const control = vi.fn(async (action: LocalWorkerAction, _generation?: string) => {
    if (action === LocalWorkerAction.Stop) { value = replacement; throw new Error("unknown stop"); }
    return value;
  });
  const view = render(<LocalWorkerControls presentation={presentation} control={control} active changed={() => {}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Stop local Worker" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm Worker stop" }));
  await screen.findByRole("button", { name: "Retry original Worker stop" });
  await screen.findByText(`Execution machine: ${replacement.machine_id}`);
  view.rerender(<LocalWorkerControls presentation={presentation} control={control} active={false} changed={() => {}} />);
  view.rerender(<LocalWorkerControls presentation={presentation} control={control} active changed={() => {}} />);
  await waitFor(() => expect((screen.getByRole("button", { name: "Retry original Worker stop" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Retry original Worker stop" }));
  await waitFor(() => expect(control.mock.calls.filter(([action]) => action === LocalWorkerAction.Stop)).toEqual([[LocalWorkerAction.Stop, original.generation], [LocalWorkerAction.Stop, original.generation]]));
});
it.each(Object.values(LocalWorkerPresentation))("blocks a new stop when its confirmation belongs to a superseded generation (%s)", async presentation => {
  let value = running();
  const control = vi.fn(async (_action: LocalWorkerAction, _generation?: string) => value);
  render(<LocalWorkerControls presentation={presentation} control={control} active changed={() => {}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Stop local Worker" }));
  value = running();
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  await screen.findByText(/The Worker generation changed/);
  expect((screen.getByRole("button", { name: "Confirm Worker stop" }) as HTMLButtonElement).disabled).toBe(true);
  expect(control.mock.calls.every(([action]) => action === LocalWorkerAction.Status)).toBe(true);
});

it.each([
  [LocalWorkerState.NotStarted, "Not started", "Registered on this computer; not started yet.", false, true, false],
  [LocalWorkerState.Starting, "Starting", "Starting; waiting for the server to accept this Worker. A previous connection lease can delay readiness.", true, false, true],
  [LocalWorkerState.Running, "Controller running", "Worker controller running. Server connectivity and harness readiness are shown separately below.", true, false, true],
  [LocalWorkerState.Stopping, "Stopping", "Stop intent saved; waiting for the original Worker controller to exit.", true, false, true],
  [LocalWorkerState.Exited, "Exited", "Worker controller exited. Existing session cleanup and recovery remain separate.", false, true, false],
  [LocalWorkerState.Uncertain, "Exit unconfirmed", "Worker exit is unconfirmed. Inspect its private log and original session recovery before explicitly replacing the controller.", false, true, true],
] as const)("presents the truthful %s badge with the existing lifecycle predicates", async (state, badge, description, controller_active, start, stop) => {
  const value = { ...running(), state, controller_active, generation: state === LocalWorkerState.NotStarted ? undefined : running().generation };
  render(<LocalWorkerControls presentation={LocalWorkerPresentation.RunnerDevices} control={async () => value} active changed={() => {}} />);
  const label = await screen.findByText(badge);
  expect(label.getAttribute("role")).toBeNull();
  expect(screen.getAllByText(description)).toHaveLength(1);
  expect(screen.getByRole("status").textContent).toBe(description);
  expect(screen.getByText(`Execution machine: ${value.machine_id}`)).toBeTruthy();
  expect(Boolean(screen.queryByRole("button", { name: "Start local Worker" }))).toBe(start);
  expect(Boolean(screen.queryByRole("button", { name: "Stop local Worker" }))).toBe(stop);
});
it("does not infer a lifecycle badge from unreadable status and explicitly refreshes", async () => {
  const value = running(), control = vi.fn().mockRejectedValueOnce(new Error("private detail")).mockResolvedValue(value);
  render(<LocalWorkerControls presentation={LocalWorkerPresentation.RunnerDevices} control={control} active changed={() => {}} />);
  expect((await screen.findByRole("alert")).textContent).toBe("The local Worker could not be inspected. Register it if this computer has no Worker, or inspect its original private scope and device registration.");
  expect(document.querySelector(".settings-runner-badge")).toBeNull();
  expect(screen.queryByRole("status")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  await screen.findByText("Controller running");
});
it("keeps the default non-registering consumer's appearance and pending callbacks", async () => {
  const pendingChanged = vi.fn();
  render(<LocalWorkerControls control={async () => running()} active allowRegistration={false} pendingChanged={pendingChanged} changed={() => {}} />);
  await screen.findByText(/Worker controller running/);
  expect(document.querySelector(".settings-runner-worker")).toBeNull();
  expect(document.querySelector(".settings-runner-badge")).toBeNull();
  expect(screen.queryByRole("button", { name: "Register this computer" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Stop local Worker" }));
  await waitFor(() => expect(pendingChanged).toHaveBeenLastCalledWith(true));
});
