// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import {
  ImageMediaType,
  newRequestId,
  type ImageAttachment,
} from "@delinoio/delidev-api-client";
import { GeneratedImageExport } from "./generated-image-export";
const bridge = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => bridge);
const reference = (): ImageAttachment => ({
  $typeName: "delidev.v1.ImageAttachment",
  id: newRequestId(),
  machineId: newRequestId(),
  mediaType: ImageMediaType.PNG,
  sha256: "a".repeat(64),
  byteLength: 3n,
});
it("exports only on explicit action with original immutable reference and bytes", async () => {
  bridge.invoke.mockResolvedValueOnce("saved");
  const ref = reference(),
    sessionId = newRequestId();
  render(
    <GeneratedImageExport
      bytes={new Uint8Array([1, 2, 3])}
      reference={ref}
      sessionId={sessionId}
      number={1}
      active
    />,
  );
  expect(bridge.invoke).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Export original image 1" }),
  );
  await screen.findByText("Original image saved.");
  expect(bridge.invoke).toHaveBeenCalledWith("export_generated_image", {
    request: {
      operationId: expect.any(String),
      sessionId,
      attachmentId: ref.id,
      sha256: ref.sha256,
      byteLength: 3,
      png: "AQID",
    },
  });
  bridge.invoke.mockClear();
});
it("observes the original operation after a lost acknowledgment without replaying Save", async () => {
  bridge.invoke.mockRejectedValueOnce(new Error("ack lost"));
  const ref = reference(),
    sessionId = newRequestId();
  const view = () => (
    <GeneratedImageExport
      bytes={new Uint8Array([1, 2, 3])}
      reference={ref}
      sessionId={sessionId}
      number={2}
      active
    />
  );
  const first = render(view());
  fireEvent.click(
    screen.getByRole("button", { name: "Export original image 2" }),
  );
  await screen.findByText(
    "The save result is unknown. Check the original save before exporting again.",
  );
  const operationId = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
  first.unmount();
  render(view());
  expect(
    (
      screen.getByRole("button", {
        name: "Export original image 2",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  bridge.invoke.mockResolvedValueOnce("saved");
  fireEvent.click(screen.getByRole("button", { name: "Check original save" }));
  await screen.findByText("Original image saved.");
  expect(bridge.invoke).toHaveBeenLastCalledWith(
    "read_generated_image_export",
    { operationId },
  );
  await waitFor(() =>
    expect(
      bridge.invoke.mock.calls.filter(
        ([command]) => command === "export_generated_image",
      ).length,
    ).toBe(1),
  );
  bridge.invoke.mockClear();
});

it.each(["busy", "stopped", "invalid-input"])(
  "permits only explicit fresh Save after proved %s non-admission across remount",
  async (reason) => {
    bridge.invoke.mockImplementationOnce(async (_command, args) => ({
      status: "not-admitted",
      operationId: args.request.operationId,
      reason,
    }));
    const ref = reference(),
      sessionId = newRequestId();
    const view = () => (
      <GeneratedImageExport
        bytes={new Uint8Array([1, 2, 3])}
        reference={ref}
        sessionId={sessionId}
        number={3}
        active
      />
    );
    const first = render(view());
    fireEvent.click(
      screen.getByRole("button", { name: "Export original image 3" }),
    );
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Export original image 3",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    const original = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
    first.unmount();
    render(view());
    expect(
      screen.queryByRole("button", { name: "Check original save" }),
    ).toBeNull();
    expect(bridge.invoke).toHaveBeenCalledTimes(1);
    bridge.invoke.mockResolvedValueOnce("saved");
    fireEvent.click(
      screen.getByRole("button", { name: "Export original image 3" }),
    );
    await screen.findByText("Original image saved.");
    expect(bridge.invoke.mock.calls.at(-1)?.[1].request.operationId).not.toBe(
      original,
    );
    bridge.invoke.mockClear();
  },
);

it.each([
  { status: "not-admitted", operationId: "foreign", reason: "busy" },
  { status: "not-admitted", reason: "busy" },
  { status: "not-admitted", operationId: "original", reason: "scope-mismatch" },
  {
    status: "not-admitted",
    operationId: "original",
    reason: "busy",
    extra: true,
  },
  "not-admitted",
  "permission-denied",
])("keeps malformed or foreign non-admission fenced: %j", async (value) => {
  bridge.invoke.mockImplementationOnce(async (_command, args) =>
    typeof value === "object" && value.operationId === "original"
      ? { ...value, operationId: args.request.operationId }
      : value,
  );
  const ref = reference(),
    sessionId = newRequestId();
  render(
    <GeneratedImageExport
      bytes={new Uint8Array([1, 2, 3])}
      reference={ref}
      sessionId={sessionId}
      number={4}
      active
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Export original image 4" }),
  );
  await screen.findByText(
    "The save result is unknown. Check the original save before exporting again.",
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Export original image 4",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  bridge.invoke.mockClear();
});

it("never releases an admitted lost-acknowledgment fence from a non-admission Check response", async () => {
  bridge.invoke.mockRejectedValueOnce(
    new Error("lost original acknowledgment"),
  );
  const ref = reference(),
    sessionId = newRequestId();
  render(
    <GeneratedImageExport
      bytes={new Uint8Array([1, 2, 3])}
      reference={ref}
      sessionId={sessionId}
      number={5}
      active
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Export original image 5" }),
  );
  await screen.findByText(
    "The save result is unknown. Check the original save before exporting again.",
  );
  const operationId = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
  bridge.invoke.mockResolvedValueOnce({
    status: "not-admitted",
    operationId,
    reason: "busy",
  });
  fireEvent.click(screen.getByRole("button", { name: "Check original save" }));
  await waitFor(() =>
    expect(bridge.invoke).toHaveBeenLastCalledWith(
      "read_generated_image_export",
      { operationId },
    ),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Export original image 5",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    bridge.invoke.mock.calls.filter(
      ([command]) => command === "export_generated_image",
    ),
  ).toHaveLength(1);
  bridge.invoke.mockClear();
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const exporter = (
  ref: ImageAttachment,
  sessionId: string,
  number: number,
) => (
  <GeneratedImageExport
    bytes={new Uint8Array([1, 2, 3])}
    reference={ref}
    sessionId={sessionId}
    number={number}
    active
  />
);
const saveButton = (number: number) =>
  screen.getByRole("button", {
    name: `Export original image ${number}`,
  }) as HTMLButtonElement;

it.each(["busy", "stopped", "invalid-input"])(
  "releases the remounted exporter when proved %s non-admission settles after the remount",
  async (reason) => {
    const response = deferred<unknown>();
    bridge.invoke.mockImplementationOnce(() => response.promise);
    const ref = reference(),
      sessionId = newRequestId();
    const first = render(exporter(ref, sessionId, 6));
    fireEvent.click(saveButton(6));
    const original = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
    first.unmount();
    render(exporter(ref, sessionId, 6));
    expect(saveButton(6).disabled).toBe(true);
    screen.getByText("Saving original image…");
    response.resolve({
      status: "not-admitted",
      operationId: original,
      reason,
    });
    await waitFor(() => expect(saveButton(6).disabled).toBe(false));
    screen.getByText(
      "The image was not saved. Choose a new filename and try again.",
    );
    expect(bridge.invoke).toHaveBeenCalledTimes(1);
    bridge.invoke.mockResolvedValueOnce("saved");
    fireEvent.click(saveButton(6));
    await screen.findByText("Original image saved.");
    expect(bridge.invoke.mock.calls.at(-1)?.[1].request.operationId).not.toBe(
      original,
    );
    bridge.invoke.mockClear();
  },
);

it("keeps the uncertainty fence for a remounted exporter after a transport failure", async () => {
  const response = deferred<unknown>();
  bridge.invoke.mockImplementationOnce(() => response.promise);
  const ref = reference(),
    sessionId = newRequestId();
  const first = render(exporter(ref, sessionId, 7));
  fireEvent.click(saveButton(7));
  first.unmount();
  render(exporter(ref, sessionId, 7));
  response.resolve(new Error("not a result"));
  await screen.findByText(
    "The save result is unknown. Check the original save before exporting again.",
  );
  expect(saveButton(7).disabled).toBe(true);
  bridge.invoke.mockClear();
});

it("updates every mounted observer of one key and leaves other keys independent", async () => {
  const response = deferred<unknown>();
  bridge.invoke.mockImplementationOnce(() => response.promise);
  const ref = reference(),
    sessionId = newRequestId();
  const other = { ...reference(), id: newRequestId() };
  render(
    <>
      <div data-testid="a">{exporter(ref, sessionId, 8)}</div>
      <div data-testid="b">{exporter(ref, sessionId, 9)}</div>
      <div data-testid="c">{exporter(other, sessionId, 10)}</div>
      <div data-testid="d">{exporter(ref, newRequestId(), 11)}</div>
    </>,
  );
  fireEvent.click(saveButton(8));
  expect(saveButton(8).disabled).toBe(true);
  expect(saveButton(9).disabled).toBe(true);
  expect(saveButton(10).disabled).toBe(false);
  expect(saveButton(11).disabled).toBe(false);
  const original = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
  response.resolve({
    status: "not-admitted",
    operationId: original,
    reason: "busy",
  });
  await waitFor(() => expect(saveButton(9).disabled).toBe(false));
  expect(saveButton(8).disabled).toBe(false);
  expect(bridge.invoke).toHaveBeenCalledTimes(1);
  bridge.invoke.mockClear();
});

it("does not let an older Check response settle a newer operation for the same key", async () => {
  const firstSave = deferred<unknown>();
  const staleCheck = deferred<unknown>();
  const secondSave = deferred<unknown>();
  bridge.invoke.mockImplementationOnce(() => firstSave.promise);
  const ref = reference(),
    sessionId = newRequestId();
  render(exporter(ref, sessionId, 12));
  fireEvent.click(saveButton(12));
  const original = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
  bridge.invoke.mockImplementationOnce(() => staleCheck.promise);
  fireEvent.click(screen.getByRole("button", { name: "Check original save" }));
  firstSave.resolve({
    status: "not-admitted",
    operationId: original,
    reason: "busy",
  });
  await waitFor(() => expect(saveButton(12).disabled).toBe(false));
  bridge.invoke.mockImplementationOnce(() => secondSave.promise);
  fireEvent.click(saveButton(12));
  const newer = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
  expect(newer).not.toBe(original);
  staleCheck.resolve("saved");
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Check original save",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  expect(saveButton(12).disabled).toBe(true);
  expect(screen.queryByText("Original image saved.")).toBeNull();
  secondSave.resolve("saved");
  await screen.findByText("Original image saved.");
  bridge.invoke.mockClear();
});
