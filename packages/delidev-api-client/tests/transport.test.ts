import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { describe, expect, it, vi } from "vitest";
import { clientFailure, FailureCode } from "../src/errors.js";
import { ErrorDetailSchema } from "../src/gen/delidev/v1/worker_pb.js";
import { GetStatusResponseSchema, SystemService } from "../src/gen/delidev/v1/system_pb.js";
import { createDeliDevTransport } from "../src/transport.js";
import { isEntityId, newRequestId, serverOrigin } from "../src/validation.js";

describe("explicit server connection", () => {
  it.each(["http://127.1:80", "http://2130706433", "http://0x7f000001", "http://0177.0.0.1", "http://localhost.evil.test", "http://0.0.0.0", "http://example.test", "http://127.0.0.1:0", "http://localhost:00080", "https://user:secret@example.test", "https://example.test/api", "https://example.test/../", "https://example.test?token=secret", "https://example.test#secret", "https://example.test\\secret", " https://example.test"])('rejects %s before authentication', (value) => {
    expect(() => serverOrigin(value)).toThrow(ConnectError);
  });
  it.each(["http://127.0.0.1:4567", "http://localhost:4567", "http://[::1]:4567", "https://example.test:4567"])("accepts %s", (value) => {
    expect(serverOrigin(`${value}/`)).toBe(value);
  });
  it("uses fresh explicit credentials, POST, no cookies/cache/redirect, and no mutation retry", async () => {
    let token = "a".repeat(43);
    const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
      expect(init?.method).toBe("POST");
      expect(init).toMatchObject({ credentials: "omit", redirect: "error", cache: "no-store" });
      expect(new Headers(init?.headers).get("Authorization")).toBe(`Bearer ${token}`);
      return new Response(toBinary(GetStatusResponseSchema, create(GetStatusResponseSchema, { version: "0.1.0", protocolVersion: 2 })), { headers: { "Content-Type": "application/proto" } });
    });
    const client = createClient(SystemService, createDeliDevTransport({ origin: "http://127.0.0.1:4567", getToken: () => token, fetch: fetcher }));
    expect((await client.getStatus({})).protocolVersion).toBe(2);
    token = "b".repeat(43);
    await client.getStatus({});
    expect(fetcher).toHaveBeenCalledTimes(2);
    fetcher.mockRejectedValue(new TypeError("secret network error"));
    await expect(client.stopServer({ requestId: newRequestId() })).rejects.toBeInstanceOf(ConnectError);
    expect(fetcher).toHaveBeenCalledTimes(3);
    token = "";
    await expect(client.getStatus({})).rejects.toMatchObject({ code: Code.Unauthenticated });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });
  it("keeps browser errors private and preserves typed conflict/correlation", () => {
    expect(JSON.stringify(clientFailure(new Error("secret=https://bad.test/credential")))).not.toContain("secret");
    const correlationId = newRequestId();
    const error = new ConnectError("The entity changed.", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "conflict", guidance: "Refresh the entity.", correlationId }) }]);
    expect(clientFailure(error)).toEqual({ code: FailureCode.Conflict, message: "The entity changed.", guidance: "Refresh the entity.", correlationId });
  });
  it("allocates distinct canonical UUID v7 request identities", () => {
    const ids = Array.from({ length: 1000 }, newRequestId);
    expect(ids.every(isEntityId)).toBe(true);
    expect(new Set(ids).size).toBe(ids.length);
    expect(isEntityId(ids[0].toUpperCase())).toBe(false);
  });
});
