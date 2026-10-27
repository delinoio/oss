// SPDX-License-Identifier: Apache-2.0
import {
  useCallback,
  createContext,
  useContext,
  useState,
  type ReactNode,
} from "react";
import {
  createClient,
  ConnectError,
  Code,
  type Transport,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { TransportProvider, useTransport } from "@connectrpc/connect-query";
import {
  QueryClient,
  QueryClientProvider,
  useQueryClient,
} from "@tanstack/react-query";
import {
  HostedSettingsService,
  AccountService,
  OrganizationService,
  MembershipService,
  InvitationService,
  OwnershipService,
  ProviderService,
  PoolService,
  RoutingService,
  KeyService,
  UsageService,
  AuditService,
  OperatorService,
  DeviceApprovalService,
  type Organization,
  type GetSessionResponse,
} from "@aiprouter/client";
export type PageProps = {
  organization: Organization;
  session: GetSessionResponse;
};
export const pageSize = 100;
export const transport = createConnectTransport({
  baseUrl:
    typeof window === "undefined" ? "http://localhost" : window.location.origin,
  fetch: (input, init) => fetch(input, { ...init, credentials: "same-origin" }),
});
export function makeClients(t: Transport) {
  return {
    hosted: createClient(HostedSettingsService, t),
    account: createClient(AccountService, t),
    organization: createClient(OrganizationService, t),
    membership: createClient(MembershipService, t),
    invitation: createClient(InvitationService, t),
    ownership: createClient(OwnershipService, t),
    provider: createClient(ProviderService, t),
    pool: createClient(PoolService, t),
    routing: createClient(RoutingService, t),
    key: createClient(KeyService, t),
    usage: createClient(UsageService, t),
    audit: createClient(AuditService, t),
    operator: createClient(OperatorService, t),
    device: createClient(DeviceApprovalService, t),
  };
}
export function useApi() {
  const t = useTransport();
  const [state] = useState(() => ({ t, clients: makeClients(t) }));
  return state.t === t ? state.clients : makeClients(t);
}
export const api = makeClients(transport);
export function ApiProvider({
  children,
  rpcTransport = transport,
  queryClient,
}: {
  children: ReactNode;
  rpcTransport?: Transport;
  queryClient?: QueryClient;
}) {
  const [client] = useState(
    () =>
      queryClient ??
      new QueryClient({
        defaultOptions: {
          queries: { retry: false, staleTime: 15_000 },
          mutations: { retry: false, gcTime: 0 },
        },
      }),
  );
  return (
    <QueryClientProvider client={client}>
      <TransportProvider transport={rpcTransport}>{children}</TransportProvider>
    </QueryClientProvider>
  );
}
export function useRefresh() {
  const client = useQueryClient();
  return useCallback(() => client.invalidateQueries(), [client]);
}
export function errorMessage(error: unknown): string {
  const e = ConnectError.from(error);
  if (e.code === Code.Unauthenticated)
    return "Your session expired. Sign in again. Your action was not retried.";
  if (e.code === Code.PermissionDenied)
    return "Your permissions changed. This action is no longer available.";
  if (e.code === Code.Unimplemented)
    return "This operation is not available on this server.";
  if (e.code === Code.Unavailable)
    return "The server is unavailable. Try again when the connection is restored.";
  if (e.code === Code.ResourceExhausted)
    return "Too many requests. Wait before trying again.";
  // Server diagnostics may contain submitted values; only display safe, fixed messages.
  if (e.code === Code.InvalidArgument)
    return "Check the entered values and the allowed destination policy, then try again.";
  if (e.code === Code.NotFound) return "This resource is no longer available.";
  if (
    e.code === Code.AlreadyExists ||
    e.code === Code.FailedPrecondition ||
    e.code === Code.Aborted
  )
    return "This action conflicts with the current state. Refresh and check dependencies before trying again.";
  return "The action could not be completed. Try again.";
}
export const DirtyContext = createContext<(dirty: boolean) => void>(() => {});
export function useDirtyForm() {
  return useContext(DirtyContext);
}
