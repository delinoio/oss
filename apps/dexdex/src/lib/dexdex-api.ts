import type { ResolvedWorkspaceConnection } from "../contracts/workspace-connection";

const PROCEDURE = {
  createUnitTask: "/dexdex.v1.TaskService/CreateUnitTask",
  listUnitTasks: "/dexdex.v1.TaskService/ListUnitTasks",
  startSubTask: "/dexdex.v1.TaskService/StartSubTask",
  listSubTasks: "/dexdex.v1.TaskService/ListSubTasks",
  submitPlanDecision: "/dexdex.v1.TaskService/SubmitPlanDecision",
  getPullRequest: "/dexdex.v1.PrManagementService/GetPullRequest",
  listReviewComments: "/dexdex.v1.ReviewCommentService/ListReviewComments",
  getSessionOutput: "/dexdex.v1.SessionService/GetSessionOutput",
  listNotifications: "/dexdex.v1.NotificationService/ListNotifications",
} as const;

export type DexDexApiClient = {
  createUnitTask: (workspaceId: string, title: string) => Promise<unknown>;
  listUnitTasks: (workspaceId: string) => Promise<unknown>;
  startSubTask: (workspaceId: string, unitTaskId: string, prompt: string) => Promise<unknown>;
  listSubTasks: (workspaceId: string, unitTaskId: string) => Promise<unknown>;
  submitPlanDecision: (
    workspaceId: string,
    subTaskId: string,
    decision: "PLAN_DECISION_APPROVE" | "PLAN_DECISION_REVISE" | "PLAN_DECISION_REJECT",
    revisionNote?: string,
  ) => Promise<unknown>;
  getPullRequest: (workspaceId: string, prTrackingId: string) => Promise<unknown>;
  listReviewComments: (workspaceId: string, prTrackingId: string) => Promise<unknown>;
  getSessionOutput: (workspaceId: string, sessionId: string) => Promise<unknown>;
  listNotifications: (workspaceId: string) => Promise<unknown>;
};

export function createDexDexApiClient(connection: ResolvedWorkspaceConnection): DexDexApiClient {
  const endpoint = connection.endpointUrl.replace(/\/$/, "");

  async function callUnary<T>(procedure: string, payload: Record<string, unknown>): Promise<T> {
    const response = await fetch(`${endpoint}${procedure}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(connection.token ? { Authorization: `Bearer ${connection.token}` } : {}),
      },
      body: JSON.stringify(payload),
    });

    if (!response.ok) {
      const body = await response.text();
      throw new Error(`Request failed (${response.status}): ${body || response.statusText}`);
    }

    return (await response.json()) as T;
  }

  return {
    createUnitTask(workspaceId, title) {
      return callUnary(PROCEDURE.createUnitTask, {
        workspaceId,
        title,
      });
    },
    listUnitTasks(workspaceId) {
      return callUnary(PROCEDURE.listUnitTasks, {
        workspaceId,
        pageSize: 50,
      });
    },
    startSubTask(workspaceId, unitTaskId, prompt) {
      return callUnary(PROCEDURE.startSubTask, {
        workspaceId,
        unitTaskId,
        type: "SUB_TASK_TYPE_INITIAL_IMPLEMENTATION",
        prompt,
      });
    },
    listSubTasks(workspaceId, unitTaskId) {
      return callUnary(PROCEDURE.listSubTasks, {
        workspaceId,
        unitTaskId,
        pageSize: 50,
      });
    },
    submitPlanDecision(workspaceId, subTaskId, decision, revisionNote) {
      return callUnary(PROCEDURE.submitPlanDecision, {
        workspaceId,
        subTaskId,
        decision,
        revisionNote: revisionNote ?? "",
      });
    },
    getPullRequest(workspaceId, prTrackingId) {
      return callUnary(PROCEDURE.getPullRequest, {
        workspaceId,
        prTrackingId,
      });
    },
    listReviewComments(workspaceId, prTrackingId) {
      return callUnary(PROCEDURE.listReviewComments, {
        workspaceId,
        prTrackingId,
      });
    },
    getSessionOutput(workspaceId, sessionId) {
      return callUnary(PROCEDURE.getSessionOutput, {
        workspaceId,
        sessionId,
      });
    },
    listNotifications(workspaceId) {
      return callUnary(PROCEDURE.listNotifications, {
        workspaceId,
      });
    },
  };
}
