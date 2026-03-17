/**
 * Hook for managing workspace event stream connection.
 * Starts stream on mount and updates connection status.
 * Invalidates React Query caches based on received stream events.
 */

import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTransport } from "@connectrpc/connect-query";
import { EventStreamClient } from "../lib/event-stream";
import type { EventStreamStatus } from "../lib/event-stream";
import { StreamEventType } from "../lib/status";

interface UseWorkspaceStreamOptions {
  workspaceId: string;
  onStatusChange: (status: EventStreamStatus) => void;
  onNotification?: (params: {
    workspaceId: string;
    sequence: number;
    notificationType: string;
    title: string;
    body: string;
    referenceId?: string;
  }) => void;
}

/**
 * Hook that manages the workspace event stream lifecycle.
 * Connects on mount and disconnects on unmount.
 * Updates connection status for UI display and invalidates
 * React Query caches when server-side data changes.
 */
export function useWorkspaceStream({ workspaceId, onStatusChange, onNotification }: UseWorkspaceStreamOptions): void {
  const clientRef = useRef<EventStreamClient | null>(null);
  const queryClient = useQueryClient();
  const transport = useTransport();

  useEffect(() => {
    const client = new EventStreamClient();
    clientRef.current = client;

    client.connect(
      workspaceId,
      (event) => {
        console.log("[WorkspaceStream] Event received:", event.eventType, "seq:", event.sequence);

        // Invalidate relevant query caches based on event type
        switch (event.eventType) {
          case StreamEventType.TASK_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.TaskService"] });
            break;
          case StreamEventType.SUBTASK_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.TaskService"] });
            break;
          case StreamEventType.SESSION_OUTPUT:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
            break;
          case StreamEventType.SESSION_STATE_CHANGED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
            break;
          case StreamEventType.NOTIFICATION_CREATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.NotificationService"] });
            // Dispatch Web Notification if handler is provided
            if (onNotification && event.payload.kind === "notificationCreated") {
              onNotification({
                workspaceId,
                sequence: Number(event.sequence),
                notificationType: event.payload.type,
                title: event.payload.title,
                body: event.payload.body,
                referenceId: event.payload.referenceId,
              });
            }
            break;
          case StreamEventType.PR_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.TaskService"] });
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.PrManagementService"] });
            break;
          case StreamEventType.REVIEW_ASSIST_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewAssistService"] });
            break;
          case StreamEventType.INLINE_COMMENT_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.ReviewCommentService"] });
            break;
          case StreamEventType.SESSION_FORK_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.SessionService"] });
            break;
          case StreamEventType.WORKSPACE_WORK_STATUS_UPDATED:
            queryClient.invalidateQueries({ queryKey: ["dexdex.v1.WorkspaceService"] });
            break;
          default:
            break;
        }
      },
      onStatusChange,
      transport,
    );

    return () => {
      client.disconnect();
      clientRef.current = null;
    };
  }, [workspaceId, onStatusChange, onNotification, queryClient, transport]);
}
