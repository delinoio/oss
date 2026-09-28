import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { useLocalWorkerProof } from "./local-worker";

it("rejects a replaced machine, malformed token and completion after connection disposal", async () => {
  const machine = newRequestId();
  const read = vi.fn(async () => ({ machineId: newRequestId(), token: "A".repeat(43) }));
  const hook = renderHook(() => useLocalWorkerProof(read));
  await act(async () => { expect(await hook.result.current.load(machine)).toBeUndefined(); });
  expect(hook.result.current.problem).toContain("another Worker will not be selected");
  read.mockResolvedValue({ machineId: machine, token: "not-canonical" });
  await act(async () => { expect(await hook.result.current.load(machine)).toBeUndefined(); });
  let resolve!: (value: { machineId: string; token: string }) => void;
  read.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
  let pending: ReturnType<typeof hook.result.current.load>;
  act(() => { pending = hook.result.current.load(machine); });
  hook.unmount();
  resolve({ machineId: machine, token: "A".repeat(43) });
  expect(await pending!).toBeUndefined();
});
