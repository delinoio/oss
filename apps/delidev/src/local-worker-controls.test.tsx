import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { LocalWorkerAction, LocalWorkerControls, LocalWorkerState, type LocalWorkerStatus } from "./local-worker-controls";

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
it("retains an uncertain original stop after the visible Worker generation changes", async () => {
  const original = running(), replacement = running();
  let value = original;
  const control = vi.fn(async (action: LocalWorkerAction, _generation?: string) => {
    if (action === LocalWorkerAction.Stop) { value = replacement; throw new Error("unknown stop"); }
    return value;
  });
  const view = render(<LocalWorkerControls control={control} active changed={() => {}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Stop local Worker" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm Worker stop" }));
  await screen.findByRole("button", { name: "Retry original Worker stop" });
  await screen.findByText(`Execution machine: ${replacement.machine_id}`);
  view.rerender(<LocalWorkerControls control={control} active={false} changed={() => {}} />);
  view.rerender(<LocalWorkerControls control={control} active changed={() => {}} />);
  await waitFor(() => expect((screen.getByRole("button", { name: "Retry original Worker stop" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Retry original Worker stop" }));
  await waitFor(() => expect(control.mock.calls.filter(([action]) => action === LocalWorkerAction.Stop)).toEqual([[LocalWorkerAction.Stop, original.generation], [LocalWorkerAction.Stop, original.generation]]));
});
it("blocks a new stop when its confirmation belongs to a superseded generation", async () => {
  let value = running();
  const control = vi.fn(async (_action: LocalWorkerAction, _generation?: string) => value);
  render(<LocalWorkerControls control={control} active changed={() => {}} />);
  fireEvent.click(await screen.findByRole("button", { name: "Stop local Worker" }));
  value = running();
  fireEvent.click(screen.getByRole("button", { name: "Refresh local Worker" }));
  await screen.findByText(/The Worker generation changed/);
  expect((screen.getByRole("button", { name: "Confirm Worker stop" }) as HTMLButtonElement).disabled).toBe(true);
  expect(control.mock.calls.every(([action]) => action === LocalWorkerAction.Status)).toBe(true);
});
