/**
 * Event stream consumer for workspace events.
 * Subscribes to StreamWorkspaceEvents via Connect RPC streaming and handles
 * reconnection with exponential backoff.
 */

import { type Transport, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { EventStreamService, StreamEventType as ProtoStreamEventType } from "../gen/v1/dexdex_pb";
import { StreamEventType, SessionOutputKind } from "./status";

/**
 * Simplified stream event response for internal consumption.
 */
export interface StreamWorkspaceEventsResponse {
  sequence: number;
  workspaceId: string;
  eventType: StreamEventType;
  occurredAt: string;
  payload: StreamEventPayload;
}

export type StreamEventPayload =
  | { kind: "task"; unitTaskId: string; status: string }
  | { kind: "subtask"; subTaskId: string; unitTaskId: string; status: string }
  | { kind: "sessionOutput"; sessionId: string; outputKind: SessionOutputKind; body: string }
  | { kind: "sessionStateChanged"; sessionId: string; status: string }
  | { kind: "prUpdated"; prTrackingId: string; status: string }
  | { kind: "notificationCreated"; notificationId: string; type: string; title: string; body: string; referenceId?: string }
  | { kind: "sessionForkUpdated"; sessionId: string; forkStatus: string }
  | { kind: "workspaceWorkStatusUpdated"; workspaceId: string; status: string }
  | { kind: "unknown" };

export type EventStreamStatus = "connected" | "disconnected" | "reconnecting";

const MAX_BACKOFF_MS = 30_000;
const INITIAL_BACKOFF_MS = 1_000;
const DEFAULT_ENDPOINT = "http://127.0.0.1:7878";

/** Map proto StreamEventType numeric to view string enum */
const STREAM_EVENT_TYPE_MAP: Record<number, StreamEventType> = {
  [ProtoStreamEventType.UNSPECIFIED]: StreamEventType.UNSPECIFIED,
  [ProtoStreamEventType.TASK_UPDATED]: StreamEventType.TASK_UPDATED,
  [ProtoStreamEventType.SUBTASK_UPDATED]: StreamEventType.SUBTASK_UPDATED,
  [ProtoStreamEventType.SESSION_OUTPUT]: StreamEventType.SESSION_OUTPUT,
  [ProtoStreamEventType.SESSION_STATE_CHANGED]: StreamEventType.SESSION_STATE_CHANGED,
  [ProtoStreamEventType.PR_UPDATED]: StreamEventType.PR_UPDATED,
  [ProtoStreamEventType.REVIEW_ASSIST_UPDATED]: StreamEventType.REVIEW_ASSIST_UPDATED,
  [ProtoStreamEventType.INLINE_COMMENT_UPDATED]: StreamEventType.INLINE_COMMENT_UPDATED,
  [ProtoStreamEventType.NOTIFICATION_CREATED]: StreamEventType.NOTIFICATION_CREATED,
  [ProtoStreamEventType.SESSION_FORK_UPDATED]: StreamEventType.SESSION_FORK_UPDATED,
  [ProtoStreamEventType.WORKSPACE_WORK_STATUS_UPDATED]: StreamEventType.WORKSPACE_WORK_STATUS_UPDATED,
};

/**
 * Event stream client that subscribes to workspace events via Connect RPC streaming.
 */
export class EventStreamClient {
  private lastSequence = 0;
  private abortController: AbortController | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private backoffMs = INITIAL_BACKOFF_MS;
  private onStatusChange: ((status: EventStreamStatus) => void) | null = null;
  private transport: Transport | null = null;

  /**
   * Connect to workspace event stream via Connect RPC server streaming.
   * Accepts an optional transport; falls back to default HTTP transport.
   */
  connect(
    workspaceId: string,
    onEvent: (event: StreamWorkspaceEventsResponse) => void,
    onStatus?: (status: EventStreamStatus) => void,
    transport?: Transport,
  ): void {
    this.cleanup(false);
    this.onStatusChange = onStatus ?? null;
    if (transport !== undefined) {
      this.transport = transport;
    }
    this.abortController = new AbortController();

    console.log("[EventStream] Connecting to workspace:", workspaceId, "from sequence:", this.lastSequence);

    void this.runStream(workspaceId, onEvent);
  }

  /**
   * Disconnect from the event stream.
   */
  disconnect(): void {
    this.cleanup(true);
  }

  private cleanup(notifyDisconnected: boolean): void {
    if (this.abortController) {
      this.abortController.abort();
      this.abortController = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (notifyDisconnected) {
      this.notifyStatus("disconnected");
      console.log("[EventStream] Disconnected");
    }
  }

  /**
   * Get the last processed sequence number for resume.
   */
  getLastSequence(): number {
    return this.lastSequence;
  }

  private notifyStatus(status: EventStreamStatus): void {
    this.onStatusChange?.(status);
  }

  private scheduleReconnect(
    workspaceId: string,
    onEvent: (event: StreamWorkspaceEventsResponse) => void,
  ): void {
    this.notifyStatus("reconnecting");
    console.log("[EventStream] Reconnecting in", this.backoffMs, "ms");
    this.reconnectTimer = setTimeout(() => {
      this.connect(workspaceId, onEvent, this.onStatusChange ?? undefined, this.transport ?? undefined);
    }, this.backoffMs);
    this.backoffMs = Math.min(this.backoffMs * 2, MAX_BACKOFF_MS);
  }

  /**
   * Run the real Connect RPC server streaming loop.
   */
  private async runStream(
    workspaceId: string,
    onEvent: (event: StreamWorkspaceEventsResponse) => void,
  ): Promise<void> {
    const streamTransport = this.transport ?? createConnectTransport({ baseUrl: DEFAULT_ENDPOINT });
    const client = createClient(EventStreamService, streamTransport);

    try {
      // Treat stream open as connected even when there are no events yet.
      this.notifyStatus("connected");
      for await (const response of client.streamWorkspaceEvents(
        {
          workspaceId,
          fromSequence: BigInt(this.lastSequence),
        },
        { signal: this.abortController?.signal },
      )) {
        this.lastSequence = Number(response.sequence);
        const event = this.convertProtoEvent(workspaceId, response);
        onEvent(event);
        this.backoffMs = INITIAL_BACKOFF_MS;
      }
      // Unexpected stream end: reconnect unless we were explicitly aborted.
      if (this.abortController?.signal.aborted) {
        return;
      }
      this.scheduleReconnect(workspaceId, onEvent);
    } catch (err) {
      if (this.abortController?.signal.aborted) return;
      console.error("[EventStream] Stream error:", err);
      this.scheduleReconnect(workspaceId, onEvent);
    }
  }

  /**
   * Convert a proto StreamWorkspaceEventsResponse to our local type.
   */
  private convertProtoEvent(
    workspaceId: string,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    response: any,
  ): StreamWorkspaceEventsResponse {
    const eventType = STREAM_EVENT_TYPE_MAP[response.eventType] ?? StreamEventType.UNSPECIFIED;
    let payload: StreamEventPayload = { kind: "unknown" };

    if (response.payload.case === "task" && response.payload.value) {
      payload = {
        kind: "task",
        unitTaskId: response.payload.value.unitTaskId,
        status: String(response.payload.value.status),
      };
    } else if (response.payload.case === "subTask" && response.payload.value) {
      payload = {
        kind: "subtask",
        subTaskId: response.payload.value.subTaskId,
        unitTaskId: response.payload.value.unitTaskId,
        status: String(response.payload.value.status),
      };
    } else if (response.payload.case === "sessionOutput" && response.payload.value) {
      payload = {
        kind: "sessionOutput",
        sessionId: response.payload.value.sessionId,
        outputKind: SessionOutputKind.TEXT,
        body: response.payload.value.body,
      };
    } else if (response.payload.case === "sessionStateChanged" && response.payload.value) {
      payload = {
        kind: "sessionStateChanged",
        sessionId: response.payload.value.sessionId,
        status: String(response.payload.value.status),
      };
    } else if (response.payload.case === "prUpdated" && response.payload.value?.pullRequest) {
      payload = {
        kind: "prUpdated",
        prTrackingId: response.payload.value.pullRequest.prTrackingId,
        status: String(response.payload.value.pullRequest.status),
      };
    } else if (response.payload.case === "notificationCreated" && response.payload.value?.notification) {
      payload = {
        kind: "notificationCreated",
        notificationId: response.payload.value.notification.notificationId,
        type: String(response.payload.value.notification.type),
        title: response.payload.value.notification.title,
        body: response.payload.value.notification.body,
        referenceId: response.payload.value.notification.referenceId || undefined,
      };
    } else if (response.payload.case === "sessionForkUpdated" && response.payload.value?.sessionSummary) {
      payload = {
        kind: "sessionForkUpdated",
        sessionId: response.payload.value.sessionSummary.sessionId,
        forkStatus: String(response.payload.value.sessionSummary.forkStatus),
      };
    } else if (response.payload.case === "workspaceWorkStatusUpdated" && response.payload.value) {
      payload = {
        kind: "workspaceWorkStatusUpdated",
        workspaceId: response.payload.value.workspaceId,
        status: String(response.payload.value.status),
      };
    }

    return {
      sequence: Number(response.sequence),
      workspaceId,
      eventType,
      occurredAt: new Date().toISOString(),
      payload,
    };
  }
}
