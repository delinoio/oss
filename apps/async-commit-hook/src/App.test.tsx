import { fireEvent, render, screen } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import {
  ExecutionState,
  FailureSchema,
} from "@delinoio/async-commit-hook-api-client";
import { Code, ConnectError } from "@connectrpc/connect";
import { expect, it, vi } from "vitest";
import { Confirm, ErrorNotice, FailureList, Status } from "./App";
import { readConnection, describeError } from "./connection";
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
  const connection = readConnection();
  expect(connection).toEqual({ run: "receipt" });
  expect(location.hash).toBe("#run=receipt");
  expect(describeError(new Error("fetch failed"))).toMatch(/Run ach ui/);
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
