// SPDX-License-Identifier: Apache-2.0
import type { NativeConnection } from "./local-registration";
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const secret = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
function encode(bytes: Uint8Array) { return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", ""); }
export function validateDesktopRuntime(value: NativeConnection) {
  const url = new URL(value.endpoint);
  if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port || Number(url.port) === 0 || url.origin !== value.endpoint || !id.test(value.server_id) || !id.test(value.runtime_generation ?? "") || !secret.test(value.runtime_key ?? "")) throw "invalid-evidence";
}
// Keep the proof secret in the current native connection closure only. A
// replaced listener must prove its original generation before bearer release.
export function createDesktopFetch(connection: NativeConnection, send: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  validateDesktopRuntime(connection);
  const { endpoint, server_id: server, runtime_generation: generation, runtime_key: key } = connection;
  return async (input, init) => {
    const request = new Request(input, init);
    if (new URL(request.url).origin !== endpoint) throw "invalid-evidence";
    const challenge = encode(crypto.getRandomValues(new Uint8Array(32)));
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(2000)]);
    const response = await send(`${endpoint}/__delidev_desktop_runtime?challenge=${challenge}`, { method: "GET", redirect: "error", credentials: "omit", cache: "no-store", signal });
    if (!response.ok || !response.body) throw "invalid-evidence";
    const reader = response.body.getReader();
    let answer = "";
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (answer.length + value.length > 43) throw "invalid-evidence";
        answer += new TextDecoder().decode(value);
      }
    } finally { await reader.cancel(); reader.releaseLock(); }
    const material = Uint8Array.from(atob(key!.replaceAll("-", "+").replaceAll("_", "/")), (char) => char.charCodeAt(0));
    const hmac = await crypto.subtle.importKey("raw", material, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
    material.fill(0);
    const proof = encode(new Uint8Array(await crypto.subtle.sign("HMAC", hmac, new TextEncoder().encode(`delidev-desktop-v2\n${generation}\n${server}\n${challenge}`))));
    if (answer !== proof || request.signal.aborted) throw "invalid-evidence";
    const authorization = request.headers.get("authorization");
    if (authorization) {
      if (!authorization.startsWith("Bearer ") || authorization.length > 256) throw "invalid-evidence";
      // Wrap only the transport copy. The stable credential and the server's
      // existing bearer authorization remain unchanged. A raced port occupant
      // receives ciphertext even if the browser reconnects after the proof.
      const rawKey = await crypto.subtle.sign("HMAC", hmac, new TextEncoder().encode("delidev-desktop-auth-v2"));
      const aes = await crypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["encrypt"]);
      new Uint8Array(rawKey).fill(0);
      const nonce = crypto.getRandomValues(new Uint8Array(12));
      const url = new URL(request.url);
      const aad = new TextEncoder().encode(`delidev-desktop-auth-v2\n${generation}\n${server}\n${request.method}\n${url.pathname}${url.search}`);
      const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce, additionalData: aad }, aes, new TextEncoder().encode(authorization));
      request.headers.set("authorization", `Bearer runtime-v2.${generation}.${encode(nonce)}.${encode(new Uint8Array(ciphertext))}`);
    }
    return send(request, { redirect: "error", credentials: "omit", cache: "no-store" });
  };
}
