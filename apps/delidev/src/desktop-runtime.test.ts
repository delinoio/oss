// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { createDesktopFetch, validateDesktopRuntime } from "./desktop-runtime";
const connection = () => ({ endpoint: "http://127.0.0.1:51743", server_id: newRequestId(), device_id: newRequestId(), token: "retained-bearer", runtime_generation: newRequestId(), runtime_key: "CQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQk" });
beforeEach(() => { vi.stubGlobal("crypto", webcrypto); });
afterEach(() => vi.unstubAllGlobals());
it("sends no bearer before proving the exact current generation", async () => {
  const current = connection();
  const send = vi.fn<typeof fetch>(async (input, init) => {
    if (typeof input === "string") {
      expect(new Headers(init?.headers).has("authorization")).toBe(false);
      expect(init).toMatchObject({ credentials: "omit", cache: "no-store", redirect: "error" });
      const challenge = new URL(input).searchParams.get("challenge");
      const key = await crypto.subtle.importKey("raw", new Uint8Array(32).fill(9), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
      const proof = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`delidev-desktop-v2\n${current.runtime_generation}\n${current.server_id}\n${challenge}`));
      return new Response(Buffer.from(proof).toString("base64url"));
    }
    const auth = (input as Request).headers.get("authorization")!;
    expect(auth).toMatch(/^Bearer runtime-v2\./); expect(auth).not.toContain("retained-bearer");
    const [generation, nonce, ciphertext] = auth.slice("Bearer runtime-v2.".length).split(".");
    expect(generation).toBe(current.runtime_generation);
    const original = await crypto.subtle.importKey("raw", new Uint8Array(32).fill(9), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
    const raw = await crypto.subtle.sign("HMAC", original, new TextEncoder().encode("delidev-desktop-auth-v2"));
    const aes = await crypto.subtle.importKey("raw", raw, "AES-GCM", false, ["decrypt"]);
    const aad = new TextEncoder().encode(`delidev-desktop-auth-v2\n${generation}\n${current.server_id}\nGET\n/rpc`);
    const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv: Buffer.from(nonce, "base64url"), additionalData: aad }, aes, Buffer.from(ciphertext, "base64url"));
    expect(new TextDecoder().decode(plain)).toBe("Bearer retained-bearer");
    return new Response("ok");
  });
  const secured = createDesktopFetch(current, send);
  for (let i = 0; i < 2; i++) await secured(`${current.endpoint}/rpc`, { headers: { authorization: "Bearer retained-bearer" } });
  expect(send).toHaveBeenCalledTimes(4);
});
it("rejects a stale or forged listener without releasing bearer or falling back", async () => {
  const current = connection();
  for (const response of [new Response("forged"), new Response("x".repeat(44)), new Response(null, { status: 302 })]) {
    const send = vi.fn<typeof fetch>().mockResolvedValue(response);
    await expect(createDesktopFetch(current, send)(`${current.endpoint}/rpc`, { headers: { authorization: "Bearer retained-bearer" } })).rejects.toBe("invalid-evidence");
    expect(send).toHaveBeenCalledTimes(1);
  }
});
it("rejects missing proof and noncanonical or foreign addresses", async () => {
  const current = connection();
  for (const endpoint of ["http://127.0.0.1:0", "http://127.0.0.1:1/path", "http://localhost:1234", "http://127.0.0.1:1234/"]) expect(() => validateDesktopRuntime({ ...current, endpoint })).toThrow();
  expect(() => createDesktopFetch({ ...current, runtime_generation: undefined })).toThrow();
  const send = vi.fn<typeof fetch>();
  await expect(createDesktopFetch(current, send)("http://127.0.0.1:1234/rpc")).rejects.toBe("invalid-evidence");
  expect(send).not.toHaveBeenCalled();
});
