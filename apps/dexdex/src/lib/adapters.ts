/**
 * Proto-to-view-model adapters for DexDex desktop app.
 * Converts generated protobuf types (numeric enums, Timestamp objects)
 * to view-model types (string enums, ISO strings) used by UI components.
 */

import type {
  UnitTask as ProtoUnitTask,
  SubTask as ProtoSubTask,
  NotificationRecord as ProtoNotification,
  SessionOutputEvent as ProtoSessionOutput,
} from "../gen/v1/dexdex_pb";
import {
  UnitTaskStatus as ProtoUnitTaskStatus,
  SubTaskType as ProtoSubTaskType,
  SubTaskStatus as ProtoSubTaskStatus,
  SubTaskCompletionReason as ProtoCompletionReason,
  SessionOutputKind as ProtoOutputKind,
  NotificationType as ProtoNotificationType,
} from "../gen/v1/dexdex_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { UnitTask, SubTask, SessionOutputEvent, Notification } from "./mock-data";
import {
  UnitTaskStatus,
  SubTaskType,
  SubTaskStatus,
  SubTaskCompletionReason,
  SessionOutputKind,
  NotificationType,
} from "./status";

// Enum mapping helpers: proto numeric -> view string enum

const UNIT_TASK_STATUS_MAP: Record<number, UnitTaskStatus> = {
  [ProtoUnitTaskStatus.UNSPECIFIED]: UnitTaskStatus.UNSPECIFIED,
  [ProtoUnitTaskStatus.QUEUED]: UnitTaskStatus.QUEUED,
  [ProtoUnitTaskStatus.IN_PROGRESS]: UnitTaskStatus.IN_PROGRESS,
  [ProtoUnitTaskStatus.ACTION_REQUIRED]: UnitTaskStatus.ACTION_REQUIRED,
  [ProtoUnitTaskStatus.BLOCKED]: UnitTaskStatus.BLOCKED,
  [ProtoUnitTaskStatus.COMPLETED]: UnitTaskStatus.COMPLETED,
  [ProtoUnitTaskStatus.FAILED]: UnitTaskStatus.FAILED,
  [ProtoUnitTaskStatus.CANCELLED]: UnitTaskStatus.CANCELLED,
};

const SUB_TASK_TYPE_MAP: Record<number, SubTaskType> = {
  [ProtoSubTaskType.UNSPECIFIED]: SubTaskType.UNSPECIFIED,
  [ProtoSubTaskType.INITIAL_IMPLEMENTATION]: SubTaskType.INITIAL_IMPLEMENTATION,
  [ProtoSubTaskType.REQUEST_CHANGES]: SubTaskType.REQUEST_CHANGES,
  [ProtoSubTaskType.PR_CREATE]: SubTaskType.PR_CREATE,
  [ProtoSubTaskType.PR_REVIEW_FIX]: SubTaskType.PR_REVIEW_FIX,
  [ProtoSubTaskType.PR_CI_FIX]: SubTaskType.PR_CI_FIX,
  [ProtoSubTaskType.MANUAL_RETRY]: SubTaskType.MANUAL_RETRY,
};

const SUB_TASK_STATUS_MAP: Record<number, SubTaskStatus> = {
  [ProtoSubTaskStatus.UNSPECIFIED]: SubTaskStatus.UNSPECIFIED,
  [ProtoSubTaskStatus.QUEUED]: SubTaskStatus.QUEUED,
  [ProtoSubTaskStatus.IN_PROGRESS]: SubTaskStatus.IN_PROGRESS,
  [ProtoSubTaskStatus.WAITING_FOR_PLAN_APPROVAL]: SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
  [ProtoSubTaskStatus.WAITING_FOR_USER_INPUT]: SubTaskStatus.WAITING_FOR_USER_INPUT,
  [ProtoSubTaskStatus.COMPLETED]: SubTaskStatus.COMPLETED,
  [ProtoSubTaskStatus.FAILED]: SubTaskStatus.FAILED,
  [ProtoSubTaskStatus.CANCELLED]: SubTaskStatus.CANCELLED,
};

const COMPLETION_REASON_MAP: Record<number, SubTaskCompletionReason> = {
  [ProtoCompletionReason.UNSPECIFIED]: SubTaskCompletionReason.UNSPECIFIED,
  [ProtoCompletionReason.SUCCEEDED]: SubTaskCompletionReason.SUCCEEDED,
  [ProtoCompletionReason.REVISED]: SubTaskCompletionReason.REVISED,
  [ProtoCompletionReason.PLAN_REJECTED]: SubTaskCompletionReason.PLAN_REJECTED,
  [ProtoCompletionReason.FAILED]: SubTaskCompletionReason.FAILED,
  [ProtoCompletionReason.CANCELLED_BY_USER]: SubTaskCompletionReason.CANCELLED_BY_USER,
};

const OUTPUT_KIND_MAP: Record<number, SessionOutputKind> = {
  [ProtoOutputKind.UNSPECIFIED]: SessionOutputKind.UNSPECIFIED,
  [ProtoOutputKind.TEXT]: SessionOutputKind.TEXT,
  [ProtoOutputKind.PLAN_UPDATE]: SessionOutputKind.PLAN_UPDATE,
  [ProtoOutputKind.TOOL_CALL]: SessionOutputKind.TOOL_CALL,
  [ProtoOutputKind.TOOL_RESULT]: SessionOutputKind.TOOL_RESULT,
  [ProtoOutputKind.PROGRESS]: SessionOutputKind.PROGRESS,
  [ProtoOutputKind.WARNING]: SessionOutputKind.WARNING,
  [ProtoOutputKind.ERROR]: SessionOutputKind.ERROR,
};

const NOTIFICATION_TYPE_MAP: Record<number, NotificationType> = {
  [ProtoNotificationType.UNSPECIFIED]: NotificationType.UNSPECIFIED,
  [ProtoNotificationType.TASK_ACTION_REQUIRED]: NotificationType.TASK_ACTION_REQUIRED,
  [ProtoNotificationType.PLAN_ACTION_REQUIRED]: NotificationType.PLAN_ACTION_REQUIRED,
  [ProtoNotificationType.PR_REVIEW_ACTIVITY]: NotificationType.PR_REVIEW_ACTIVITY,
  [ProtoNotificationType.PR_CI_FAILURE]: NotificationType.PR_CI_FAILURE,
  [ProtoNotificationType.AGENT_SESSION_FAILED]: NotificationType.AGENT_SESSION_FAILED,
};

/**
 * Convert a protobuf Timestamp to an ISO string.
 * Returns current time if timestamp is undefined.
 */
function timestampToISO(ts: Timestamp | undefined): string {
  if (ts) {
    return timestampDate(ts).toISOString();
  }
  return new Date().toISOString();
}

/**
 * Convert a proto UnitTask to a view-model UnitTask.
 * Proto UnitTask does not contain subTasks - those must be fetched separately.
 */
export function toViewUnitTask(proto: ProtoUnitTask, subTasks: SubTask[] = []): UnitTask {
  return {
    unitTaskId: proto.unitTaskId,
    title: proto.title || "Untitled",
    description: proto.description || "",
    status: UNIT_TASK_STATUS_MAP[proto.status] ?? UnitTaskStatus.UNSPECIFIED,
    repositoryUrl: "",
    branchRef: "",
    createdAt: timestampToISO(proto.createdAt),
    updatedAt: timestampToISO(proto.updatedAt),
    subTasks,
  };
}

/**
 * Convert a proto SubTask to a view-model SubTask.
 */
export function toViewSubTask(proto: ProtoSubTask): SubTask {
  return {
    subTaskId: proto.subTaskId,
    unitTaskId: proto.unitTaskId,
    type: SUB_TASK_TYPE_MAP[proto.type] ?? SubTaskType.UNSPECIFIED,
    status: SUB_TASK_STATUS_MAP[proto.status] ?? SubTaskStatus.UNSPECIFIED,
    completionReason: COMPLETION_REASON_MAP[proto.completionReason] ?? SubTaskCompletionReason.UNSPECIFIED,
    sessionId: proto.sessionId || "",
    createdAt: timestampToISO(proto.createdAt),
    updatedAt: timestampToISO(proto.updatedAt),
    planSummary: proto.title || undefined,
  };
}

/**
 * Convert a proto NotificationRecord to a view-model Notification.
 */
export function toViewNotification(proto: ProtoNotification): Notification {
  return {
    notificationId: proto.notificationId,
    type: NOTIFICATION_TYPE_MAP[proto.type] ?? NotificationType.UNSPECIFIED,
    title: proto.title || "",
    body: proto.body || "",
    taskId: proto.referenceId || undefined,
    read: proto.read,
    createdAt: timestampToISO(proto.createdAt),
  };
}

/**
 * Convert a proto SessionOutputEvent to a view-model SessionOutputEvent.
 */
export function toViewSessionOutput(proto: ProtoSessionOutput): SessionOutputEvent {
  return {
    sessionId: proto.sessionId,
    kind: OUTPUT_KIND_MAP[proto.kind] ?? SessionOutputKind.UNSPECIFIED,
    body: proto.body,
    timestamp: new Date().toISOString(),
  };
}
