/**
 * Event stream consumer for workspace events.
 * Subscribes to StreamWorkspaceEvents and handles reconnection with exponential backoff.
 */

import { StreamEventType, SessionOutputKind } from "./status";

/**
 * Simplified stream event response matching the proto StreamWorkspaceEventsResponse shape.
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
  | { kind: "notificationCreated"; notificationId: string; type: string }
  | { kind: "unknown" };

export type EventStreamStatus = "connected" | "disconnected" | "reconnecting";

const MAX_BACKOFF_MS = 30_000;
const INITIAL_BACKOFF_MS = 1_000;

/**
 * Event stream client that subscribes to workspace events.
 * In the current scaffold phase, this simulates connectivity for UI development.
 * Real implementation will use Connect streaming RPC client.
 */
export class EventStreamClient {
  private lastSequence = 0;
  private abortController: AbortController | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private backoffMs = INITIAL_BACKOFF_MS;
  private onStatusChange: ((status: EventStreamStatus) => void) | null = null;

  /**
   * Connect to workspace event stream.
   * In scaffold mode, this sets up the connection state without actual server communication.
   */
  connect(
    workspaceId: string,
    onEvent: (event: StreamWorkspaceEventsResponse) => void,
    onStatus?: (status: EventStreamStatus) => void,
  ): void {
    this.disconnect();
    this.onStatusChange = onStatus ?? null;
    this.abortController = new AbortController();

    console.log("[EventStream] Connecting to workspace:", workspaceId, "from sequence:", this.lastSequence);
    this.notifyStatus("connected");
    this.backoffMs = INITIAL_BACKOFF_MS;

    // In scaffold mode, we simulate being connected.
    // Real implementation would use:
    //   const stream = client.streamWorkspaceEvents({ workspaceId, fromSequence: this.lastSequence });
    //   for await (const event of stream) { ... }
    // with reconnection on error.

    void this.simulateStream(workspaceId, onEvent);
  }

  /**
   * Disconnect from the event stream.
   */
  disconnect(): void {
    if (this.abortController) {
      this.abortController.abort();
      this.abortController = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.notifyStatus("disconnected");
    console.log("[EventStream] Disconnected");
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
      this.connect(workspaceId, onEvent, this.onStatusChange ?? undefined);
    }, this.backoffMs);
    this.backoffMs = Math.min(this.backoffMs * 2, MAX_BACKOFF_MS);
  }

  /**
   * Simulate stream for scaffold/dev mode.
   * Emits no events but maintains connection state.
   */
  private async simulateStream(
    _workspaceId: string,
    _onEvent: (event: StreamWorkspaceEventsResponse) => void,
  ): Promise<void> {
    // In real implementation, this would be the streaming loop.
    // For scaffold mode, we just stay "connected" until disconnect.
  }
}
