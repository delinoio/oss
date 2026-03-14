/**
 * Hook for managing workspace event stream connection.
 * Starts stream on mount and updates connection status.
 */

import { useEffect, useRef } from "react";
import { EventStreamClient } from "../lib/event-stream";
import type { EventStreamStatus } from "../lib/event-stream";

interface UseWorkspaceStreamOptions {
  workspaceId: string;
  onStatusChange: (status: EventStreamStatus) => void;
}

/**
 * Hook that manages the workspace event stream lifecycle.
 * Connects on mount and disconnects on unmount.
 * Updates connection status for UI display.
 */
export function useWorkspaceStream({ workspaceId, onStatusChange }: UseWorkspaceStreamOptions): void {
  const clientRef = useRef<EventStreamClient | null>(null);

  useEffect(() => {
    const client = new EventStreamClient();
    clientRef.current = client;

    client.connect(
      workspaceId,
      (event) => {
        // Handle incoming events - in scaffold mode, no events are received.
        // Real implementation would update React Query cache or local state here.
        console.log("[WorkspaceStream] Event received:", event.eventType, "seq:", event.sequence);
      },
      onStatusChange,
    );

    return () => {
      client.disconnect();
      clientRef.current = null;
    };
  }, [workspaceId, onStatusChange]);
}
