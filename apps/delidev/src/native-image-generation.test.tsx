// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { webcrypto } from "node:crypto";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { AttachmentService, newRequestId } from "@delinoio/delidev-api-client";
import {
  NativeImageGeneration,
  generationObservation,
} from "./native-image-generation";
import { imageDigest } from "./image-input";
const bridge = vi.hoisted(() => ({
  tauri: false,
  invoke: vi.fn(),
}));
vi.mock("@tauri-apps/api/core", () => ({
  isTauri: () => bridge.tauri,
  invoke: bridge.invoke,
}));
const bytes = new Uint8Array([1, 2, 3]);
beforeEach(() => {
  bridge.tauri = false;
  bridge.invoke.mockReset();
  vi.stubGlobal("crypto", webcrypto);
  const BaseURL = URL;
  vi.stubGlobal(
    "URL",
    class extends BaseURL {
      static createObjectURL = vi.fn(() => "blob:original");
      static revokeObjectURL = vi.fn();
    },
  );
});
afterEach(() => vi.unstubAllGlobals());
async function fixtures() {
  const read = vi.fn(async () => ({
    data: bytes,
    sha256: await imageDigest(bytes),
    complete: true,
  }));
  const transport = createRouterTransport((router) =>
    router.service(AttachmentService, { readAttachment: read }),
  );
  const started = {
    kind: "image-generation",
    image_generation: { status: "in_progress", outputs: [] },
  };
  const output = {
    id: newRequestId(),
    machine_id: newRequestId(),
    media_type: "image/png",
    byte_length: bytes.length,
    sha256: await imageDigest(bytes),
  };
  const completed = {
    kind: "image-generation",
    image_generation: {
      status: "completed",
      revised_prompt: "Original native revision",
      transparent_background: true,
      outputs: [output],
    },
  };
  const id = newRequestId(),
    view = (artifact: Record<string, unknown>) => (
      <TransportProvider transport={transport}>
        <NativeImageGeneration artifact={artifact} sessionId={id} active />
      </TransportProvider>
    );
  return { read, started, completed, output, id, view };
}
it("retains native lifecycle and offers no browser export outside the desktop host", async () => {
  const { read, started, completed, view } = await fixtures();
  const mounted = render(view({ started }));
  expect(
    screen.getByText("Native image generation is in progress."),
  ).toBeTruthy();
  expect(read).not.toHaveBeenCalled();
  expect(screen.queryByRole("link")).toBeNull();
  mounted.rerender(view({ started, completed }));
  const image = await screen.findByRole("img");
  expect(image.getAttribute("src")).toBe("blob:original");
  expect(read).toHaveBeenCalledOnce();
  expect(screen.queryByRole("link")).toBeNull();
  expect(screen.queryByRole("button", { name: /Export original/ })).toBeNull();
  expect(mounted.container.querySelector("a")).toBeNull();
  expect(mounted.container.querySelector("[download]")).toBeNull();
  expect(bridge.invoke).not.toHaveBeenCalled();
  mounted.unmount();
  await waitFor(() =>
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:original"),
  );
});
it("offers only the explicit native Save control with the original receipt in the desktop host", async () => {
  bridge.tauri = true;
  bridge.invoke.mockResolvedValue("saved");
  const { read, started, completed, output, id, view } = await fixtures();
  const mounted = render(view({ started, completed }));
  const save = await screen.findByRole("button", {
    name: "Export original image 1",
  });
  expect(await screen.findByRole("img")).toBeTruthy();
  expect(read).toHaveBeenCalledOnce();
  expect(screen.queryByRole("link")).toBeNull();
  expect(mounted.container.querySelector("[download]")).toBeNull();
  expect(bridge.invoke).not.toHaveBeenCalled();
  fireEvent.click(save);
  await waitFor(() => expect(bridge.invoke).toHaveBeenCalledOnce());
  expect(bridge.invoke).toHaveBeenCalledWith(
    "export_generated_image",
    expect.objectContaining({
      request: expect.objectContaining({
        sessionId: id,
        attachmentId: output.id,
        sha256: output.sha256,
        byteLength: bytes.length,
      }),
    }),
  );
  mounted.unmount();
});
it("shows no export action while readback is loading or has failed", async () => {
  bridge.tauri = true;
  const { started, completed, id } = await fixtures();
  const failing = createRouterTransport((router) =>
    router.service(AttachmentService, {
      readAttachment: async () => ({
        data: bytes,
        sha256: "wrong",
        complete: true,
      }),
    }),
  );
  vi.spyOn(console, "warn").mockImplementation(() => {});
  render(
    <TransportProvider transport={failing}>
      <NativeImageGeneration
        artifact={{ started, completed }}
        sessionId={id}
        active
      />
    </TransportProvider>,
  );
  expect(screen.queryByRole("button", { name: /Export original/ })).toBeNull();
  await screen.findByRole("button", { name: "Retry loading image" });
  expect(screen.queryByRole("img")).toBeNull();
  expect(screen.queryByRole("button", { name: /Export original/ })).toBeNull();
  expect(screen.queryByRole("link")).toBeNull();
});
it("preserves native failure and rejects fabricated success and unknown proof", () => {
  const started = {
    kind: "image-generation",
    image_generation: { status: "in_progress", outputs: [] },
  };
  render(
    <NativeImageGeneration
      artifact={{
        started,
        completed: {
          kind: "image-generation",
          image_generation: {
            status: "failed",
            outputs: [],
            failure: {
              type: "usageLimitExceeded",
              limit_id: "original",
              resets_at: null,
            },
          },
        },
      }}
      sessionId={newRequestId()}
      active={false}
    />,
  );
  expect(screen.getByText("Native image generation failed.")).toBeTruthy();
  expect(screen.queryByRole("link")).toBeNull();
  for (const value of [
    { status: "completed", outputs: [] },
    { status: "in_progress", outputs: [], usage: {} },
    { status: "failed", outputs: [], failure: { type: "unavailable" } },
  ])
    expect(generationObservation(value)).toBeUndefined();
});
