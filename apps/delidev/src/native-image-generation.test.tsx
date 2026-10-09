// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { webcrypto } from "node:crypto";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { AttachmentService, newRequestId } from "@delinoio/delidev-api-client";
import { NativeImageGeneration, generationObservation } from "./native-image-generation";
import { imageDigest } from "./image-input";
const bytes = new Uint8Array([1, 2, 3]);
beforeEach(() => { vi.stubGlobal("crypto", webcrypto); const BaseURL = URL; vi.stubGlobal("URL", class extends BaseURL { static createObjectURL = vi.fn(() => "blob:original"); static revokeObjectURL = vi.fn(); }); });
afterEach(() => vi.unstubAllGlobals());
it("retains native lifecycle and only offers explicit export of verified original bytes", async () => {
 const read = vi.fn(async () => ({ data: bytes, sha256: await imageDigest(bytes), complete: true }));
 const transport = createRouterTransport(router => router.service(AttachmentService, { readAttachment: read }));
 const started = { kind: "image-generation", image_generation: { status: "in_progress", outputs: [] } };
 const output = { id: newRequestId(), machine_id: newRequestId(), media_type: "image/png", byte_length: bytes.length, sha256: await imageDigest(bytes) };
 const completed = { kind: "image-generation", image_generation: { status: "completed", revised_prompt: "Original native revision", transparent_background: true, outputs: [output] } };
 const id = newRequestId(), view = (artifact: Record<string, unknown>) => <TransportProvider transport={transport}><NativeImageGeneration artifact={artifact} sessionId={id} active /></TransportProvider>;
 const mounted = render(view({ started })); expect(screen.getByText("Native image generation is in progress.")).toBeTruthy(); expect(read).not.toHaveBeenCalled();
 mounted.rerender(view({ started, completed })); const link = await screen.findByRole("link", { name: "Export original image 1" }); expect(link.getAttribute("download")).toBe("generated-image-1.png"); expect(link.getAttribute("href")).toBe("blob:original"); expect(read).toHaveBeenCalledOnce(); 
 fireEvent.click(link); mounted.unmount(); await waitFor(() => expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:original"));
});
it("preserves native failure and rejects fabricated success and unknown proof", () => {
 const started = { kind: "image-generation", image_generation: { status: "in_progress", outputs: [] } };
 render(<NativeImageGeneration artifact={{ started, completed: { kind: "image-generation", image_generation: { status: "failed", outputs: [], failure: { type: "usageLimitExceeded", limit_id: "original", resets_at: null } } } }} sessionId={newRequestId()} active={false} />);
 expect(screen.getByText("Native image generation failed.")).toBeTruthy(); expect(screen.queryByRole("link")).toBeNull();
 for (const value of [{ status: "completed", outputs: [] }, { status: "in_progress", outputs: [], usage: {} }, { status: "failed", outputs: [], failure: { type: "unavailable" } }]) expect(generationObservation(value)).toBeUndefined();
});
