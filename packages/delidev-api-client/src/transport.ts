import { Code, ConnectError, type Interceptor, type Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { serverOrigin } from "./validation.js";

export interface ConnectionOptions {
  origin: string;
  // The caller owns authentication and lifecycle. Never persist this value in
  // Query keys, browser storage, generated configuration, or diagnostic state.
  getToken: () => string | undefined | Promise<string | undefined>;
  fetch?: typeof globalThis.fetch;
}

export function createDeliDevTransport(options: ConnectionOptions): Transport {
  const origin = serverOrigin(options.origin);
  const authenticate: Interceptor = (next) => async (request) => {
    if (!request.service.typeName.startsWith("delidev.v1.")) {
      throw new ConnectError("This connection is limited to DeliDev RPC.", Code.PermissionDenied);
    }
    const token = await options.getToken();
    if (!token || !/^[A-Za-z0-9_-]{43}$/.test(token)) {
      throw new ConnectError("Pair this client with the selected server.", Code.Unauthenticated);
    }
    request.header.set("Authorization", `Bearer ${token}`);
    return next(request);
  };
  const fetcher = options.fetch ?? globalThis.fetch;
  return createConnectTransport({
    baseUrl: origin,
    useBinaryFormat: true,
    useHttpGet: false,
    interceptors: [authenticate],
    fetch: async (input, init) => {
      try {
        return await fetcher(input, { ...init, redirect: "error", credentials: "omit", cache: "no-store" });
      } catch {
        // Fetch's TypeError may contain the request URL or platform details.
        // Classify it without retaining the original value, so stream readers
        // can reconnect while mutations remain explicitly caller-controlled.
        throw new ConnectError("The selected server connection is unavailable.", init?.signal?.aborted ? Code.Canceled : Code.Unavailable);
      }
    },
  });
}
