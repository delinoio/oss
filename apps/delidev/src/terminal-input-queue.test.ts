// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { TerminalInputQueue, binaryInput } from "./terminal-input-queue";
import { terminalDockHeight } from "./terminal-dock-size";

it("atomically rejects a complete paste overflow without altering original bytes", () => {
  const queue = new TerminalInputQueue(), original = new Uint8Array(65535).fill(7);
  expect(queue.enqueue(original)).toBe(true); original.fill(0);
  expect(queue.enqueue(new Uint8Array([1, 2]))).toBe(false);
  expect(queue.size).toBe(65535);
  const first = queue.next()!; expect("input" in first && first.input).toEqual(new Uint8Array(32768).fill(7));
  const second = queue.next()!; expect("input" in second && second.input).toEqual(new Uint8Array(32767).fill(7));
  expect(queue.next()).toBeUndefined();
});
it("orders input before the latest valid coalesced resize and discards only unsent state", () => {
  const queue = new TerminalInputQueue(); queue.resize(24, 80); queue.resize(37, 91); queue.resize(501, 91); queue.resize(37, 0);
  queue.enqueue(new Uint8Array([27, 91, 65, 9, 13, 3])); expect(queue.next()).toEqual({ input: new Uint8Array([27,91,65,9,13,3]) });
  expect(queue.next()).toEqual({rows:37,columns:91}); queue.acknowledge({rows:37,columns:91}); expect(queue.next()).toBeUndefined();
  queue.resize(500,1000);queue.enqueue(new Uint8Array([255]));expect(queue.discard()).toBe(1);expect(queue.next()).toBeUndefined();
  expect(binaryInput("\u0000\u0080\u00ff")).toEqual(new Uint8Array([0,128,255]));
});
it("defaults to40 percent, clamps resized docks and maximizes narrow or short initial docks", () => {
  expect(terminalDockHeight(1000,800,false)).toBe(320);expect(terminalDockHeight(1000,800,false,1)).toBe(200);expect(terminalDockHeight(1000,800,false,999)).toBe(560);
  expect(terminalDockHeight(899,800,false)).toBe(800);expect(terminalDockHeight(1000,599,false)).toBe(599);expect(terminalDockHeight(1000,800,true)).toBe(800);
  expect(terminalDockHeight(480,320,false,128)).toBe(200);
});
