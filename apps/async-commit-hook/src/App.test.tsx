import { fireEvent, render, screen } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import {
  ExecutionState,
  FailureSchema,
} from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { Confirm, ErrorNotice, FailureList, Status } from "./App";
import * as connection from "./connection";
const { readConnection, describeError } = connection;
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
