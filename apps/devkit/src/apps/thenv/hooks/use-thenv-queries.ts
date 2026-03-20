"use client";

import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";

import {
  listBundleVersions,
  pullActiveBundle,
  pushBundleVersion,
  activateBundleVersion,
  rotateBundleVersion,
} from "@/gen/thenv/v1/thenv-BundleService_connectquery";
import { getPolicy, setPolicy } from "@/gen/thenv/v1/thenv-PolicyService_connectquery";
import { listAuditEvents } from "@/gen/thenv/v1/thenv-AuditService_connectquery";
import type { Scope } from "@/gen/thenv/v1/thenv_pb";
import { AuditEventType } from "@/gen/thenv/v1/thenv_pb";

export function useListBundleVersions(scope: Scope | undefined, limit = 20) {
  return useQuery(listBundleVersions, scope ? { scope, limit, cursor: "" } : undefined, {
    enabled: !!scope,
  });
}

export function usePullBundleVersion(scope: Scope | undefined, bundleVersionId: string | undefined) {
  return useQuery(
    pullActiveBundle,
    scope
      ? {
          scope,
          bundleVersionId: bundleVersionId ?? "",
        }
      : undefined,
    {
      enabled: !!scope,
    },
  );
}

export function usePushBundleVersionMutation() {
  const queryClient = useQueryClient();
  return useMutation(pushBundleVersion, {
    onSuccess: () => {
      queryClient.invalidateQueries();
    },
  });
}

export function useActivateBundleVersionMutation() {
  const queryClient = useQueryClient();
  return useMutation(activateBundleVersion, {
    onSuccess: () => {
      queryClient.invalidateQueries();
    },
  });
}

export function useRotateBundleVersionMutation() {
  const queryClient = useQueryClient();
  return useMutation(rotateBundleVersion, {
    onSuccess: () => {
      queryClient.invalidateQueries();
    },
  });
}

export function useGetPolicy(scope: Scope | undefined) {
  return useQuery(getPolicy, scope ? { scope } : undefined, {
    enabled: !!scope,
  });
}

export function useSetPolicyMutation() {
  const queryClient = useQueryClient();
  return useMutation(setPolicy, {
    onSuccess: () => {
      queryClient.invalidateQueries();
    },
  });
}

export function useListAuditEvents(
  scope: Scope | undefined,
  eventType: AuditEventType = AuditEventType.UNSPECIFIED,
  limit = 20,
) {
  return useQuery(
    listAuditEvents,
    scope ? { scope, eventType, actor: "", limit, cursor: "" } : undefined,
    { enabled: !!scope },
  );
}
