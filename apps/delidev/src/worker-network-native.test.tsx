import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { pressEnter } from "./test-form-enter";
import {
  WorkerNetworkAction,
  WorkerNetworkControlProvider,
  WorkerNetworkNative,
} from "./worker-network-native";

// Enter submission: the encrypted import is one semantic form. Pointer, Enter in
// the digest input and form submission share one guarded owner callback, the
// bundle textarea keeps Enter as a newline, and the verified-digest checkbox
// still gates every submission.
const digest = "a".repeat(64);
const bundle = btoa("synthetic encrypted fixture");

type NativeControl = (
  machine: string,
  action: WorkerNetworkAction,
  bytes: Uint8Array,
  expectedDigest: string,
) => Promise<unknown>;

function fixture(
  native = vi.fn<NativeControl>(
    async (
      _machine: string,
      action: WorkerNetworkAction,
      _bytes: Uint8Array,
      expectedDigest: string,
    ) => {
      if (action !== WorkerNetworkAction.Import)
        throw new Error("No prepare or status call is expected");
      return { version: 1, ciphertext_digest: expectedDigest, generation: "2" };
    },
  ),
) {
  const machine = newRequestId();
  const prepared = vi.fn();
  const view = render(
    <WorkerNetworkControlProvider control={native}>
      <WorkerNetworkNative
        machine={machine}
        authority={{
          endpoint: "https://server.example",
          serverId: newRequestId(),
        }}
        prepared={prepared}
      />
    </WorkerNetworkControlProvider>,
  );
  return { native, machine, prepared, view };
}
const bundleInput = () =>
  screen.getByLabelText(
    "Encrypted bundle to import (Base64)",
  ) as HTMLTextAreaElement;
const digestInput = () =>
  screen.getByLabelText("Separately authenticated digest") as HTMLInputElement;
const confirmBox = () => screen.getByRole("checkbox") as HTMLInputElement;
const importButton = () =>
  screen.getByRole("button", {
    name: /^(Import confirmed encrypted configuration|Retry original encrypted import)$/,
  }) as HTMLButtonElement;
const formOf = (control: HTMLElement) =>
  control.closest("form") as HTMLFormElement;
function fill(value: { digest?: string; confirm?: boolean } = {}) {
  fireEvent.change(bundleInput(), { target: { value: bundle } });
  fireEvent.change(digestInput(), {
    target: { value: value.digest ?? digest },
  });
  if (value.confirm ?? true) fireEvent.click(confirmBox());
}
const importedBytes = (
  native: ReturnType<typeof fixture>["native"],
  call = 0,
) => [...(native.mock.calls[call][2] as Uint8Array)];

it("owns the encrypted import as one semantic form with a single submit control", () => {
  const value = fixture();
  fill();
  const form = formOf(digestInput());
  expect(form.id).not.toBe("");
  expect(form.noValidate).toBe(true);
  expect(form.querySelector("form")).toBeNull();
  expect(formOf(bundleInput())).toBe(form);
  expect(formOf(confirmBox())).toBe(form);
  expect(importButton().form).toBe(form);
  expect(
    [...form.querySelectorAll("button")].filter(
      (button) => button.type === "submit",
    ),
  ).toEqual([importButton()]);
  // Prepare and Inspect stay plain buttons and never import.
  for (const name of [
    "Prepare protected recipient",
    "Inspect original public recipient",
  ]) {
    const button = screen.getByRole("button", { name }) as HTMLButtonElement;
    expect(button.type).toBe("button");
    expect(button.form).toBeNull();
  }
  expect(value.native).not.toHaveBeenCalled();
});

it("imports once with Enter from the digest input using the pointer payload", async () => {
  const clicked = fixture();
  fill();
  fireEvent.click(importButton());
  await waitFor(() => expect(clicked.native).toHaveBeenCalledTimes(1));
  await screen.findByText(/generation/i);
  const expected = clicked.native.mock.calls[0];
  const expectedBytes = importedBytes(clicked.native);
  cleanup();

  const value = fixture();
  fill();
  expect(pressEnter(digestInput())).toBe(1);
  await waitFor(() => expect(value.native).toHaveBeenCalledTimes(1));
  await screen.findByText(/generation/i);
  const call = value.native.mock.calls[0];
  expect(call[0]).toBe(value.machine);
  expect(call[1]).toBe(WorkerNetworkAction.Import);
  expect(call[1]).toBe(expected[1]);
  expect(importedBytes(value.native)).toEqual(expectedBytes);
  expect(call[3]).toBe(digest);
  expect(call[3]).toBe(expected[3]);
  expect(value.prepared).not.toHaveBeenCalled();
  // A successful import clears the disposable inputs and the confirmation.
  expect(bundleInput().value).toBe("");
  expect(digestInput().value).toBe("");
  expect(confirmBox().checked).toBe(false);
});

it("keeps Enter in the bundle textarea as a newline", () => {
  const value = fixture();
  fill();
  bundleInput().focus();
  const proceeded = fireEvent.keyDown(bundleInput(), {
    key: "Enter",
    code: "Enter",
    keyCode: 13,
  });
  expect(proceeded).toBe(true);
  expect(pressEnter(bundleInput())).toBe(0);
  expect(value.native).not.toHaveBeenCalled();
  expect(bundleInput().value).toBe(bundle);
});

it("does not import with Enter before the digest is confirmed", () => {
  const value = fixture();
  fill({ confirm: false });
  expect(importButton().disabled).toBe(true);
  expect(pressEnter(digestInput())).toBe(0);
  fireEvent.submit(formOf(digestInput()));
  expect(value.native).not.toHaveBeenCalled();
  // Editing an input after confirming clears the confirmation, so Enter stays inert.
  fireEvent.click(confirmBox());
  fireEvent.change(digestInput(), { target: { value: "b".repeat(64) } });
  expect(confirmBox().checked).toBe(false);
  expect(pressEnter(digestInput())).toBe(0);
  fireEvent.submit(formOf(digestInput()));
  expect(value.native).not.toHaveBeenCalled();
});

it("does not mutate for a confirmed but invalid digest and reports the same problem as a click", async () => {
  const value = fixture();
  fill({ digest: "not-a-digest" });
  expect(pressEnter(digestInput())).toBe(1);
  await screen.findByText(/Paste the encrypted Base64 bundle/);
  expect(value.native).not.toHaveBeenCalled();
  expect(digestInput().value).toBe("not-a-digest");
  expect(bundleInput().value).toBe(bundle);
  expect(importButton().disabled).toBe(false);
});

it.each([
  ["composition", { composing: true }],
  ["legacy composition key code", { keyCode: 229 }],
  ["held Enter", { repeat: true }],
  ["Shift+Enter", { shiftKey: true }],
  ["Control+Enter", { ctrlKey: true }],
  ["Alt+Enter", { altKey: true }],
  ["Meta+Enter", { metaKey: true }],
])("does not import on %s", (_label, press) => {
  const value = fixture();
  fill();
  expect(pressEnter(digestInput(), press)).toBe(0);
  expect(value.native).not.toHaveBeenCalled();
  expect(digestInput().value).toBe(digest);
  expect(confirmBox().checked).toBe(true);
});

it("does not import with Enter on the confirmation checkbox", () => {
  const value = fixture();
  fill();
  const proceeded = fireEvent.keyDown(confirmBox(), {
    key: "Enter",
    keyCode: 13,
  });
  expect(proceeded).toBe(false);
  expect(pressEnter(confirmBox())).toBe(0);
  expect(value.native).not.toHaveBeenCalled();
});

it("imports once while a pending import ignores repeated Enter and submission", async () => {
  let release: () => void = () => {};
  const native = vi.fn<NativeControl>(
    (
      _machine: string,
      _action: WorkerNetworkAction,
      _bytes: Uint8Array,
      expectedDigest: string,
    ) =>
      new Promise((resolve) => {
        release = () =>
          resolve({
            version: 1,
            ciphertext_digest: expectedDigest,
            generation: "2",
          });
      }),
  );
  fixture(native);
  fill();
  expect(pressEnter(digestInput())).toBe(1);
  await waitFor(() => expect(native).toHaveBeenCalledTimes(1));
  expect(importButton().disabled).toBe(true);
  expect(pressEnter(digestInput())).toBe(0);
  fireEvent.submit(formOf(digestInput()));
  release();
  await screen.findByText(/generation/i);
  expect(native).toHaveBeenCalledTimes(1);
});

it("retains the exact uncertain import and retries it through form submission", async () => {
  const native = vi.fn<NativeControl>(
    async (
      _machine: string,
      _action: WorkerNetworkAction,
      _bytes: Uint8Array,
      expectedDigest: string,
    ) => {
      if (native.mock.calls.length === 1) throw new Error("Lost native result");
      return { version: 1, ciphertext_digest: expectedDigest, generation: "2" };
    },
  );
  fixture(native);
  fill();
  expect(pressEnter(digestInput())).toBe(1);
  await waitFor(() => expect(native).toHaveBeenCalledTimes(1));
  await waitFor(() =>
    expect(importButton().textContent).toContain(
      "Retry original encrypted import",
    ),
  );
  expect(digestInput().disabled).toBe(true);
  expect(bundleInput().disabled).toBe(true);
  expect(importButton().type).toBe("submit");
  fireEvent.submit(formOf(digestInput()));
  await screen.findByText(/generation/i);
  expect(native).toHaveBeenCalledTimes(2);
  expect([...(native.mock.calls[1][2] as Uint8Array)]).toEqual([
    ...(native.mock.calls[0][2] as Uint8Array),
  ]);
  expect(native.mock.calls[1][3]).toBe(native.mock.calls[0][3]);
});
