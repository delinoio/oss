import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { verifyLocalServer } from "./connection";

it("retries only transient status reads and still validates the retained server identity", async () => {
  const id = newRequestId();
  const read = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1, serverId: id }));
  read.mockRejectedValueOnce(new ConnectError("temporary read failure", Code.Unavailable));
  const stop = vi.fn();
  const transport = createRouterTransport((router) => router.service(SystemService, { getStatus: read, stopServer: stop }));
  const log = vi.spyOn(console, "warn").mockImplementation(() => {});
  try {
    await verifyLocalServer(transport, id);
    expect(read).toHaveBeenCalledTimes(2);
    expect(stop).not.toHaveBeenCalled();
    await expect(verifyLocalServer(transport, newRequestId())).rejects.toBe("incompatible");
    read.mockRejectedValueOnce(new ConnectError("invalid auth", Code.Unauthenticated));
    await expect(verifyLocalServer(transport, id)).rejects.toMatchObject({ code: Code.Unauthenticated });
    expect(read).toHaveBeenCalledTimes(4);
  } finally { log.mockRestore(); }
});
