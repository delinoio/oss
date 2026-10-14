// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { Desktop } from "./desktop";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
} from "./entrance-test-support";
import { LocalServerState } from "./local-server";
import {
  SavedConnectionState,
  type SavedConnection,
} from "./saved-connections";

const bridge = vi.hoisted(() => ({
  invoke: vi.fn(),
  listen: vi.fn(),
}));
vi.mock("@tauri-apps/api/event", () => ({ listen: bridge.listen }));
vi.mock("@tauri-apps/api/core", () => ({
  isTauri: () => true,
  invoke: bridge.invoke,
}));

const Y2 = "entrance-y2";

interface Deferred {
  promise: Promise<unknown>;
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
}
function deferred(): Deferred {
  const value = {} as Deferred;
  value.promise = new Promise((resolve, reject) => {
    value.resolve = resolve;
    value.reject = reject;
  });
  return value;
}
function count(command: string) {
  return bridge.invoke.mock.calls.filter(([name]) => name === command).length;
}
/** Local window whose launch and retry calls are held or failed by the test. */
function localFixture() {
  const launch = deferred();
  const retry = deferred();
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return null;
    if (command === "local_server_status")
      return { state: LocalServerState.Blocked, attempts: 1, retry_ms: 60000 };
    if (command === "launch_local") return launch.promise;
    if (command === "retry_local") return retry.promise;
    throw new Error("Unexpected native authority");
  });
  return { launch, retry };
}
function savedProfile(): SavedConnection {
  return {
    version: 1,
    revision: 1,
    id: newRequestId(),
    name: "Remote fixture",
    endpoint: "https://fixture.example.test",
    server_id: newRequestId(),
    device_id: newRequestId(),
    pairing_id: newRequestId(),
    state: SavedConnectionState.Paired,
    created_at: "2026-09-25T00:00:00Z",
  };
}
/** Saved window whose identity read and connection are held by the test. */
function savedFixture() {
  const profile = savedProfile();
  const context = deferred();
  const connect = deferred();
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return context.promise;
    if (command === "connect_saved") return connect.promise;
    throw new Error("Unexpected native authority");
  });
  return { profile, context, connect };
}
function page() {
  const node = document.querySelector("main.connect-page");
  expect(node).not.toBeNull();
  return node as HTMLElement;
}
async function settleReads() {
  await act(async () => {
    await Promise.resolve();
  });
}
beforeEach(() => {
  bridge.invoke.mockReset();
  bridge.listen.mockReset().mockResolvedValue(() => {});
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  }));
  delete document.documentElement.dataset.animation;
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one Y2 entrance on a fresh local window and never replays on failure or retry", async () => {
  const { launch, retry } = localFixture();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  const node = page();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(node.getAttribute("style")).toBeNull();
  // Busy to error arrives mid-motion: same element, still the one entrance.
  await act(async () => launch.reject("timed-out"));
  const retryButton = await screen.findByRole("button", { name: "Retry" });
  expect(page()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(count("launch_local")).toBe(1);
  dispatchAnimation(node, Y2);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  // Explicit Retry and its held result are instantaneous and never replay.
  fireEvent.click(retryButton);
  await settleReads();
  expect(count("retry_local")).toBe(1);
  expect(page()).toBe(node);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  await act(async () => retry.reject("timed-out"));
  await screen.findByRole("button", { name: "Retry" });
  expect(node.hasAttribute("data-entrance")).toBe(false);
  expect(count("retry_local")).toBe(1);
});

it("keeps the entrance unchanged for a stopped launch and the Troubleshooting modal", async () => {
  const { launch } = localFixture();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  const node = page();
  await act(async () => launch.reject("stopped"));
  await screen.findByRole("button", { name: "Start local server" });
  expect(page()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBe("y2");
  fireEvent.click(screen.getByRole("button", { name: "Troubleshooting" }));
  const dialog = await screen.findByRole("dialog");
  // The diagnostics Modal is a sibling of the animated page, not a descendant.
  expect(node.contains(dialog)).toBe(false);
  expect(dialog.closest("[data-entrance]")).toBeNull();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  // Unrelated bubbling animations do not settle the entrance.
  dispatchAnimation(node, "spin");
  expect(node.getAttribute("data-entrance")).toBe("y2");
  dispatchAnimation(node, Y2);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  expect(count("launch_local")).toBe(1);
  expect(count("retry_local")).toBe(0);
  expect(count("connect_local")).toBe(0);
});

it("settles through the fallback timer without waiting for animation events", async () => {
  vi.useFakeTimers();
  try {
    const { launch } = localFixture();
    render(<Desktop />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const node = page();
    expect(node.getAttribute("data-entrance")).toBe("y2");
    await act(async () => launch.reject("timed-out"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(node.hasAttribute("data-entrance")).toBe(false);
    expect(count("launch_local")).toBe(1);
  } finally {
    vi.useRealTimers();
  }
});

it("does not animate a local window when Animation Off is set and never replays on re-enable", async () => {
  await setAnimation("false");
  const { launch } = localFixture();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  const node = page();
  expect(node.hasAttribute("data-entrance")).toBe(false);
  await setAnimation("true");
  expect(node.hasAttribute("data-entrance")).toBe(false);
  await act(async () => launch.reject("timed-out"));
  await screen.findByRole("button", { name: "Retry" });
  expect(node.hasAttribute("data-entrance")).toBe(false);
});

it("clears an in-flight entrance when Animation Off arrives and never replays on re-enable", async () => {
  const { launch } = localFixture();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  const node = page();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  await setAnimation("false");
  expect(node.hasAttribute("data-entrance")).toBe(false);
  await setAnimation("true");
  expect(node.hasAttribute("data-entrance")).toBe(false);
  await act(async () => launch.reject("timed-out"));
  await screen.findByRole("button", { name: "Retry" });
  expect(node.hasAttribute("data-entrance")).toBe(false);
});

it("suppresses the entrance under OS reduced motion, including mid-flight changes", async () => {
  const set = reducedMotion(true);
  const { launch } = localFixture();
  const first = render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  expect(page().hasAttribute("data-entrance")).toBe(false);
  set(false);
  expect(page().hasAttribute("data-entrance")).toBe(false);
  first.unmount();
  const second = render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  const node = page();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  set(true);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  set(false);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  second.unmount();
  await act(async () => launch.reject("timed-out"));
});

it("plays one entrance for a saved window after its static identity read", async () => {
  const { profile, context, connect } = savedFixture();
  render(<Desktop />);
  await screen.findByText("Reading this window's server identity…");
  // The identity-read page stays static and consumes nothing.
  expect(page().hasAttribute("data-entrance")).toBe(false);
  await act(async () => context.resolve(profile));
  await screen.findByText("Connecting…");
  const node = page();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(count("connect_saved")).toBe(1);
  await act(async () => connect.reject("failed"));
  await screen.findByRole("alert");
  expect(page()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBe("y2");
  dispatchAnimation(node, Y2);
  expect(node.hasAttribute("data-entrance")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Troubleshooting" }));
  await screen.findByRole("dialog");
  expect(node.hasAttribute("data-entrance")).toBe(false);
  expect(count("connect_saved")).toBe(1);
});

it("keeps a saved window static when its identity read fails and is retried", async () => {
  const profile = savedProfile();
  let reads = 0;
  const connect = deferred();
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") {
      reads += 1;
      if (reads === 1) throw "context-unavailable";
      return profile;
    }
    if (command === "connect_saved") return connect.promise;
    throw new Error("Unexpected native authority");
  });
  render(<Desktop />);
  const retry = await screen.findByRole("button", {
    name: "Retry window context",
  });
  expect(page().hasAttribute("data-entrance")).toBe(false);
  fireEvent.click(retry);
  await screen.findByText("Connecting…");
  expect(page().getAttribute("data-entrance")).toBe("y2");
  expect(count("connect_saved")).toBe(1);
  await act(async () => connect.reject("failed"));
});
