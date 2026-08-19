import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  AdminService,
  AdminQuery,
  BootstrapService,
  BootstrapQuery,
  type GetBootstrapResponse,
} from "@delinoio/devhud-api-client";
import type { AdminAuth } from "./auth";

export type AdminClient = Client<typeof AdminService>;

export function apiBaseUrl(origin = window.location.origin): string {
  return origin === "http://localhost:46306"
    ? "http://127.0.0.1:46307"
    : origin;
}

export async function getBootstrap(): Promise<GetBootstrapResponse> {
  const transport = createConnectTransport({ baseUrl: apiBaseUrl() });
  const service = {
    ...BootstrapService,
    method: { getBootstrap: BootstrapQuery.getBootstrap },
  } satisfies typeof BootstrapService;
  return createClient(service, transport).getBootstrap({});
}

export function createAdminClient(auth: AdminAuth): AdminClient {
  const transport = createConnectTransport({
    baseUrl: apiBaseUrl(),
    interceptors: [
      (next) => async (request) => {
        request.header.set("Authorization", `Bearer ${await auth.accessToken()}`);
        return next(request);
      },
    ],
  });
  const service = {
    ...AdminService,
    method: {
      listUsers: AdminQuery.listUsers,
      setUserBlocked: AdminQuery.setUserBlocked,
      getUserUsage: AdminQuery.getUserUsage,
      listUploads: AdminQuery.listUploads,
      quarantineUpload: AdminQuery.quarantineUpload,
      deleteUpload: AdminQuery.deleteUpload,
      listAuditEvents: AdminQuery.listAuditEvents,
    },
  } satisfies typeof AdminService;
  return createClient(service, transport);
}
