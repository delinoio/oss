import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import {
  LocalWorkerIdentityStatus,
  useLocalWorkerIdentity,
  useLocalWorkerProof,
} from "./local-worker";

it("rejects a replaced machine, malformed token and completion after connection disposal", async () => {
  const machine = newRequestId();
  const read = vi.fn(async () => ({
    machineId: newRequestId(),
    token: "A".repeat(43),
  }));
  const hook = renderHook(() => useLocalWorkerProof(read));
  await act(async () => {
    expect(await hook.result.current.load(machine)).toBeUndefined();
  });
  expect(hook.result.current.problem).toContain(
    "another Worker will not be selected",
  );
  read.mockResolvedValue({ machineId: machine, token: "not-canonical" });
  await act(async () => {
    expect(await hook.result.current.load(machine)).toBeUndefined();
  });
  let resolve!: (value: { machineId: string; token: string }) => void;
  read.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  let pending: ReturnType<typeof hook.result.current.load>;
  act(() => {
    pending = hook.result.current.load(machine);
  });
  hook.unmount();
  resolve({ machineId: machine, token: "A".repeat(43) });
  expect(await pending!).toBeUndefined();
});

it("keeps a verified identity across parent renders that reuse the same reader", async () => {
  const machineId = newRequestId();
  const read = vi.fn(async () => ({ machineId, token: "A".repeat(43) }));
  const hook = renderHook(() => useLocalWorkerIdentity(read, true));
  await act(async () => {});
  expect(hook.result.current.identity.status).toBe(
    LocalWorkerIdentityStatus.Verified,
  );
  hook.rerender();
  hook.rerender();
  expect(hook.result.current.identity.status).toBe(
    LocalWorkerIdentityStatus.Verified,
  );
  expect(read).toHaveBeenCalledTimes(1);
  await act(async () => hook.result.current.refresh());
  expect(read).toHaveBeenCalledTimes(2);
});
