// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ImageMediaType, newRequestId, type ImageAttachment } from "@delinoio/delidev-api-client";
import { GeneratedImageExport } from "./generated-image-export";
const bridge = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => bridge);
const reference = (): ImageAttachment => ({ $typeName: "delidev.v1.ImageAttachment", id: newRequestId(), machineId: newRequestId(), mediaType: ImageMediaType.PNG, sha256: "a".repeat(64), byteLength: 3n });
it("exports only on explicit action with original immutable reference and bytes", async () => {
 bridge.invoke.mockResolvedValueOnce("saved"); const ref = reference(), sessionId = newRequestId();
 render(<GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={1} active />);
 expect(bridge.invoke).not.toHaveBeenCalled(); fireEvent.click(screen.getByRole("button", { name: "Export original image 1" }));
 await screen.findByText("Original image saved.");
 expect(bridge.invoke).toHaveBeenCalledWith("export_generated_image", { request: { operationId: expect.any(String), sessionId, attachmentId: ref.id, sha256: ref.sha256, byteLength: 3, png: "AQID" } }); bridge.invoke.mockClear();
});
it("observes the original operation after a lost acknowledgment without replaying Save", async () => {
 bridge.invoke.mockRejectedValueOnce(new Error("ack lost")); const ref = reference(), sessionId = newRequestId();
 const view = () => <GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={2} active />;
 const first = render(view()); fireEvent.click(screen.getByRole("button", { name: "Export original image 2" }));
 await screen.findByText("The save result is unknown. Check the original save before exporting again.");
 const operationId = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
 first.unmount(); render(view()); expect((screen.getByRole("button", { name: "Export original image 2" }) as HTMLButtonElement).disabled).toBe(true);
 bridge.invoke.mockResolvedValueOnce("saved"); fireEvent.click(screen.getByRole("button", { name: "Check original save" }));
 await screen.findByText("Original image saved."); expect(bridge.invoke).toHaveBeenLastCalledWith("read_generated_image_export", { operationId });
 await waitFor(() => expect(bridge.invoke.mock.calls.filter(([command]) => command === "export_generated_image").length).toBe(1)); bridge.invoke.mockClear();
});
