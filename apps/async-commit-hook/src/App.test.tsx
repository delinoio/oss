import { fireEvent, render, screen } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  ExecutionState,
  FailureSchema,
  LocalService,
} from "@delinoio/async-commit-hook-api-client";
import { Code, ConnectError } from "@connectrpc/connect";
import { expect, it, vi } from "vitest";
import { App, Confirm, ErrorNotice, FailureList, Status } from "./App";
import * as connection from "./connection";
import { describeError } from "./connection";
it("renders hostile report content as inert text", () => {
  const failure = create(FailureSchema, {
    id: "f",
    check: "test",
    message: "<script>alert('x')</script>",
  });
  const { container } = render(<FailureList failures={[failure]} />);
  expect(container.querySelector("script")).toBeNull();
  expect(screen.getByText("<script>alert('x')</script>")).toBeTruthy();
});
it("communicates every important outcome without relying on color", () => {
  render(
    <>
      <Status state={ExecutionState.INTERRUPTED} />
      <Status state={ExecutionState.EXPIRED} />
      <Status state={ExecutionState.RUNNING} />
    </>,
  );
  expect(screen.getByText(/Interrupted/)).toBeTruthy();
  expect(screen.getByText(/Evidence expired/)).toBeTruthy();
  expect(screen.getByText(/Running/)).toBeTruthy();
});
it("discards retired connection fields and preserves the run deep link", () => {
  history.replaceState(null, "", "/#port=46309&pair=private&run=receipt");
  const parsed = connection.readConnection();
  expect(parsed).toEqual({ run: "receipt" });
  expect(location.hash).toBe("#run=receipt");
  expect(connection.describeError(new Error("fetch failed"))).toMatch(/Run ach ui/);
});
it("focuses the main content without replacing a run deep link", () => {
  history.replaceState(null, "", "/#run=receipt");
  const transport = vi.spyOn(connection, "transportFor").mockImplementation(() =>
    createRouterTransport((router) => router.service(LocalService, {
      getVersion: () => ({ apiVersion: 1 }),
      listRepositories: () => ({ repositories: [] }),
      getRun: () => ({}),
    })),
  );
  const { unmount } = render(<App />);
  try {
    fireEvent.click(screen.getByRole("link", { name: "Skip to content" }));
    expect(location.hash).toBe("#run=receipt");
    expect(document.activeElement).toBe(screen.getByRole("main"));
  } finally {
    unmount();
    transport.mockRestore();
  }
});
it("focuses the incompatible-version recovery content from the skip link", async () => {
  history.replaceState(null, "", "/#run=receipt");
  const transport = vi.spyOn(connection, "transportFor").mockImplementation(() =>
    createRouterTransport((router) => router.service(LocalService, {
      getVersion: () => ({ apiVersion: 2 }),
      listRepositories: () => ({ repositories: [] }),
    })),
  );
  const { unmount } = render(<App />);
  try {
    await screen.findByText("Incompatible local API version. Install a matching ach version.");
    fireEvent.click(screen.getByRole("link", { name: "Skip to content" }));
    expect(location.hash).toBe("#run=receipt");
    expect(document.activeElement).toBe(screen.getByRole("main"));
  } finally {
    unmount();
    transport.mockRestore();
  }
});
it("keeps structured diff-base diagnostics out of connection recovery", () => {
  const error = new ConnectError(
    "diff-base-required: select a local diff base; no remote fetch is performed",
    Code.InvalidArgument,
  );
  expect(describeError(error)).toBe(error.rawMessage);
  expect(describeError(error)).not.toMatch(/Cannot reach ach/);
});
it("recovers from a wrapped browser transport failure", () => {
  const error = new ConnectError(
    "fetch failed",
    Code.Unknown,
    undefined,
    undefined,
    new TypeError("Failed to fetch"),
  );
  expect(describeError(error)).toMatch(/Run ach ui/);
});
it("supports dialog cancellation and restores focus", () => {
  HTMLDialogElement.prototype.showModal = vi.fn();
  HTMLDialogElement.prototype.close = vi.fn();
  const onClose = vi.fn();
  const prior = document.createElement("button");
  document.body.append(prior);
  prior.focus();
  const { container, unmount } = render(
    <Confirm
      title="Cancel execution?"
      onClose={onClose}
      onConfirm={async () => {}}
    >
      Details
    </Confirm>,
  );
  fireEvent(
    container.querySelector("dialog")!,
    new Event("cancel", { bubbles: true, cancelable: true }),
  );
  expect(onClose).toHaveBeenCalled();
  unmount();
  expect(document.activeElement).toBe(prior);
  prior.remove();
});
it("shows local server recovery without requesting network permissions", () => {
  render(<ErrorNotice error={new Error("fetch failed")} />);
  expect(screen.getByText(/Run ach ui/)).toBeTruthy();
});
