/**
 * React Query hooks for DexDex Connect RPC services.
 * Wraps generated connect-query descriptors with view-model adapters.
 */

import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listUnitTasks, listSubTasks, createUnitTask, submitPlanDecision } from "../gen/v1/dexdex-TaskService_connectquery";
import { listNotifications, markNotificationRead } from "../gen/v1/dexdex-NotificationService_connectquery";
import { getWorkspaceWorkStatus, getWorkspaceSettings, updateWorkspaceSettings } from "../gen/v1/dexdex-WorkspaceService_connectquery";
import {
  getSessionOutput,
  listSessionCapabilities,
  forkSession,
  listForkedSessions,
  archiveForkedSession,
  getLatestWaitingSession,
  submitSessionInput,
} from "../gen/v1/dexdex-SessionService_connectquery";
import {
  getRepository,
  listRepositories,
  createRepository,
  updateRepository,
  deleteRepository,
  getRepositoryGroup,
  listRepositoryGroups,
  createRepositoryGroup,
  updateRepositoryGroup,
  deleteRepositoryGroup,
} from "../gen/v1/dexdex-RepositoryService_connectquery";
import { getPullRequest, listPullRequests } from "../gen/v1/dexdex-PrManagementService_connectquery";
import { listReviewAssistItems } from "../gen/v1/dexdex-ReviewAssistService_connectquery";
import {
  listReviewComments,
  createReviewComment,
  updateReviewComment,
  deleteReviewComment,
  resolveReviewComment,
  reopenReviewComment,
} from "../gen/v1/dexdex-ReviewCommentService_connectquery";
import { getBadgeTheme } from "../gen/v1/dexdex-BadgeThemeService_connectquery";
import { toViewUnitTask, toViewSubTask, toViewNotification, toViewSessionOutput, toViewSessionSummary, toViewAgentCapability, toViewReviewComment } from "../lib/adapters";
import type { UnitTask, SubTask, Notification, SessionOutputEvent, SessionSummary, AgentCapability, ReviewComment } from "../lib/mock-data";

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

/**
 * Mutation to mark a notification as read on the server.
 * Invalidates the notifications list on success.
 */
export function useMarkNotificationReadMutation() {
  const queryClient = useQueryClient();
  return useMutation(markNotificationRead, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.NotificationService"] });
    },
  });
}

/**
 * Fetch workspace work status for tray/status display.
 */
export function useGetWorkspaceWorkStatus(workspaceId: string) {
  return useQuery(getWorkspaceWorkStatus, { workspaceId });
}

/**
 * Fetch workspace settings.
 */
export function useGetWorkspaceSettings(workspaceId: string) {
  return useQuery(getWorkspaceSettings, { workspaceId });
}

/**
 * Mutation to update workspace settings.
 */
export function useUpdateWorkspaceSettingsMutation() {
  const queryClient = useQueryClient();
  return useMutation(updateWorkspaceSettings, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.WorkspaceService"] });
    },
  });
}

/**
 * Fetch session capabilities for a workspace (fork support, agent type).
 */
export function useListSessionCapabilities(workspaceId: string) {
  return useQuery(listSessionCapabilities, { workspaceId });
}

/**
 * Mutation to fork a session.
 * Invalidates session queries on success.
 */
export function useForkSessionMutation() {
  const queryClient = useQueryClient();
  return useMutation(forkSession, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
    },
  });
}

/**
 * Fetch forked sessions for a given parent session.
 */
export function useListForkedSessions(workspaceId: string, parentSessionId: string) {
  const query = useQuery(
    listForkedSessions,
    { workspaceId, parentSessionId },
    { enabled: !!parentSessionId },
  );
  const sessions: SessionSummary[] = (query.data?.sessions ?? []).map(toViewSessionSummary);
  return { ...query, data: sessions };
}

/**
 * Mutation to archive a forked session.
 * Invalidates session queries on success.
 */
export function useArchiveForkedSessionMutation() {
  const queryClient = useQueryClient();
  return useMutation(archiveForkedSession, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
    },
  });
}

/**
 * Fetch the latest waiting session for a workspace.
 */
export function useGetLatestWaitingSession(workspaceId: string) {
  return useQuery(getLatestWaitingSession, { workspaceId });
}

/**
 * Mutation to submit input to a waiting session.
 * Invalidates session queries on success.
 */
export function useSubmitSessionInputMutation() {
  const queryClient = useQueryClient();
  return useMutation(submitSessionInput, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
    },
  });
}

/**
 * Fetch all repository groups for a workspace.
 */
export function useListRepositoryGroups(workspaceId: string) {
  return useQuery(listRepositoryGroups, { workspaceId });
}

/**
 * Fetch all repositories for a workspace.
 */
export function useListRepositories(workspaceId: string) {
  return useQuery(listRepositories, { workspaceId });
}

/**
 * Fetch a repository by ID.
 */
export function useGetRepository(workspaceId: string, repositoryId: string) {
  return useQuery(
    getRepository,
    { workspaceId, repositoryId },
    { enabled: !!repositoryId },
  );
}

/**
 * Mutation to create a repository.
 */
export function useCreateRepositoryMutation() {
  const queryClient = useQueryClient();
  return useMutation(createRepository, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Mutation to update a repository.
 */
export function useUpdateRepositoryMutation() {
  const queryClient = useQueryClient();
  return useMutation(updateRepository, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Mutation to delete a repository.
 */
export function useDeleteRepositoryMutation() {
  const queryClient = useQueryClient();
  return useMutation(deleteRepository, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Fetch a repository group by ID.
 */
export function useGetRepositoryGroup(workspaceId: string, repositoryGroupId: string) {
  return useQuery(
    getRepositoryGroup,
    { workspaceId, repositoryGroupId },
    { enabled: !!repositoryGroupId },
  );
}

/**
 * Mutation to create a repository group.
 */
export function useCreateRepositoryGroupMutation() {
  const queryClient = useQueryClient();
  return useMutation(createRepositoryGroup, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Mutation to update a repository group.
 */
export function useUpdateRepositoryGroupMutation() {
  const queryClient = useQueryClient();
  return useMutation(updateRepositoryGroup, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Mutation to delete a repository group.
 */
export function useDeleteRepositoryGroupMutation() {
  const queryClient = useQueryClient();
  return useMutation(deleteRepositoryGroup, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.RepositoryService"] });
    },
  });
}

/**
 * Fetch all pull requests for a workspace.
 */
export function useListPullRequests(workspaceId: string) {
  return useQuery(listPullRequests, { workspaceId });
}

/**
 * Fetch a pull request by tracking ID.
 */
export function useGetPullRequest(workspaceId: string, prTrackingId: string) {
  return useQuery(
    getPullRequest,
    { workspaceId, prTrackingId },
    { enabled: !!prTrackingId },
  );
}

/**
 * Fetch review assist items for a unit task.
 */
export function useListReviewAssistItems(workspaceId: string, unitTaskId: string) {
  return useQuery(
    listReviewAssistItems,
    { workspaceId, unitTaskId },
    { enabled: !!unitTaskId },
  );
}

/**
 * Fetch review comments for a PR, converted to view-model types.
 */
export function useListReviewComments(workspaceId: string, prTrackingId: string) {
  const query = useQuery(
    listReviewComments,
    { workspaceId, prTrackingId },
    { enabled: !!prTrackingId },
  );
  const comments: ReviewComment[] = (query.data?.comments ?? []).map(toViewReviewComment);
  return { ...query, data: { comments } };
}

/**
 * Mutation to create a review comment.
 */
export function useCreateReviewCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation(createReviewComment, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
    },
  });
}

/**
 * Mutation to update a review comment body.
 */
export function useUpdateReviewCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation(updateReviewComment, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
    },
  });
}

/**
 * Mutation to delete a review comment.
 */
export function useDeleteReviewCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation(deleteReviewComment, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
    },
  });
}

/**
 * Mutation to resolve a review comment.
 */
export function useResolveReviewCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation(resolveReviewComment, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
    },
  });
}

/**
 * Mutation to reopen a review comment.
 */
export function useReopenReviewCommentMutation() {
  const queryClient = useQueryClient();
  return useMutation(reopenReviewComment, {
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
    },
  });
}

/**
 * Fetch badge theme for a workspace.
 */
export function useGetBadgeTheme(workspaceId: string) {
  return useQuery(getBadgeTheme, { workspaceId });
}
