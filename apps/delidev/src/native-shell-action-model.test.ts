// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "vitest";
import { readNativeShellObservation, shellText, NativeShellDelivery, NativeShellProcessStatus } from "./native-shell-action-model";
const fixture = () => ({ version: 1, action_id: "original-action", native_thread_id: "original-thread", delivery: NativeShellDelivery.Acknowledged, terminal: false, cleanup_verified: false, sequence: 1, processes: [{ item_id: "original-item", process_id: "original-process", command: "printf hello", cwd: "/original", status: NativeShellProcessStatus.Running, output: "hello" }] });

test("retains original process output and distinguishes terminal outcome from cleanup", () => {
  const original = fixture();
  expect(readNativeShellObservation(original)).toEqual(original);
  const terminal = { ...original, terminal: true, processes: [{ ...original.processes[0], status: NativeShellProcessStatus.Completed, exit_code: 0 }] };
  expect(readNativeShellObservation({ ...terminal, processes: [{ ...terminal.processes[0], aggregated_output: "final native output" }] })?.processes[0].aggregated_output).toBe("final native output");
  expect(readNativeShellObservation(terminal)?.cleanup_verified).toBe(false);
  expect(readNativeShellObservation({ ...terminal, cleanup_verified: true })?.cleanup_verified).toBe(true);
  expect(readNativeShellObservation({ ...original, cleanup_verified: true })).toBeUndefined();
  expect(readNativeShellObservation({ ...original, terminal: true })).toBeUndefined();
});

test("rejects malformed, duplicated, foreign-shape and unbounded native observations", () => {
  for (const changed of [null, {}, { ...fixture(), sequence: 100001 }, { ...fixture(), sequence: Number.MAX_SAFE_INTEGER + 1 }, { ...fixture(), delivery: "sandboxed" }, { ...fixture(), version: 2 }, { ...fixture(), extra: "unowned" }, { ...fixture(), processes: [fixture().processes[0], fixture().processes[0]] }, { ...fixture(), processes: [{ ...fixture().processes[0], output: "x".repeat(262145) }] }, { ...fixture(), processes: [{ ...fixture().processes[0], exit_code: 1.5 }] }]) expect(readNativeShellObservation(changed)).toBeUndefined();
  const process = fixture().processes[0];
  expect(readNativeShellObservation({ ...fixture(), processes: [{ ...process, command: "xx", output: "x".repeat(262144), aggregated_output: "x".repeat(262144) }] })).toBeUndefined();
  expect(readNativeShellObservation({ ...fixture(), processes: Array.from({ length: 128 }, (_, index) => ({ ...process, item_id: `item-${index}`, output: "x".repeat(5000) })) })).toBeUndefined();
});

test("bounds exact UTF-8 command bytes without changing the entered command", () => {
  expect(shellText("  printf hello  ", 65536, true)).toBe(true);
  expect(shellText(" ", 65536, true)).toBe(false);
  expect(shellText("가".repeat(22000), 65536, true)).toBe(false);
  expect(shellText("bad\0command", 65536, true)).toBe(false);
  expect(shellText("🙂", 65536, true)).toBe(true);
});
