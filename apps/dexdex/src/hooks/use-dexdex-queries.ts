/**
 * React Query hooks for DexDex Connect RPC services.
 * Wraps generated connect-query descriptors with view-model adapters.
 */

import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listUnitTasks, listSubTasks, createUnitTask, submitPlanDecision } from "../gen/v1/dexdex-TaskService_connectquery";
import { listNotifications } from "../gen/v1/dexdex-NotificationService_connectquery";
import { getSessionOutput } from "../gen/v1/dexdex-SessionService_connectquery";
import { toViewUnitTask, toViewSubTask, toViewNotification, toViewSessionOutput } from "../lib/adapters";
import type { UnitTask, SubTask, Notification, SessionOutputEvent } from "../lib/mock-data";

/**
 * Fetch all unit tasks for a workspace, converted to view-model types.
 */
export function useListUnitTasks(workspaceId: string) {
  const query = useQuery(listUnitTasks, { workspaceId });
  const tasks: UnitTask[] = (query.data?.unitTasks ?? []).map((t) => toViewUnitTask(t));
  return { ...query, data: tasks };
}

/**
 * Fetch subtasks for a specific unit task, converted to view-model types.
 */
export function useListSubTasks(workspaceId: string, unitTaskId: string) {
  const query = useQuery(listSubTasks, { workspaceId, unitTaskId }, { enabled: !!unitTaskId });
  const subTasks: SubTask[] = (query.data?.subTasks ?? []).map(toViewSubTask);
  return { ...query, data: subTasks };
}

/**
 * Fetch notifications for a workspace, converted to view-model types.
 */
export function useListNotifications(workspaceId: string) {
  const query = useQuery(listNotifications, { workspaceId });
  const notifications: Notification[] = (query.data?.notifications ?? []).map(toViewNotification);
  return { ...query, data: notifications };
}

/**
 * Fetch session output events for a specific session, converted to view-model types.
 */
export function useGetSessionOutput(workspaceId: string, sessionId: string | undefined) {
  const query = useQuery(
    getSessionOutput,
    { workspaceId, sessionId: sessionId ?? "" },
    { enabled: !!sessionId },
  );
  const events: SessionOutputEvent[] = (query.data?.events ?? []).map(toViewSessionOutput);
  return { ...query, data: events };
}

/**
 * Mutation to create a new unit task.
 * Invalidates the unit tasks list on success.
 */
export function useCreateUnitTaskMutation() {
  const queryClient = useQueryClient();
  return useMutation(createUnitTask, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.TaskService"] });
    },
  });
}

/**
 * Mutation to submit a plan decision for a subtask.
 * Invalidates both subtasks and unit tasks on success.
 */
export function useSubmitPlanDecisionMutation() {
  const queryClient = useQueryClient();
  return useMutation(submitPlanDecision, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.TaskService"] });
    },
  });
}
