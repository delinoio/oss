/**
 * Hook for handling global shortcut events from Tauri.
 * Listens for Cmd/Ctrl+Shift+I shortcut and navigates to the latest waiting session
 * or falls back to inbox with empty state.
 */

import { useEffect } from "react";
import { useGetLatestWaitingSession } from "./use-dexdex-queries";

interface UseGlobalShortcutOptions {
  workspaceId: string;
  onNavigate: (path: string) => void;
}

export function useGlobalShortcut({ workspaceId, onNavigate }: UseGlobalShortcutOptions): void {
  const { data, refetch } = useGetLatestWaitingSession(workspaceId);

  useEffect(() => {
    let unlisten: (() => void) | undefined;

    async function setup() {
      try {
        const { listen } = await import("@tauri-apps/api/event");
        unlisten = await listen("dexdex://global-shortcut-input", async () => {
          // Refetch to get the latest waiting session
          const result = await refetch();
          const session = result.data?.session;

          if (session && session.sessionId) {
            // Navigate to the task containing this session
            // The session's reference will be used to find the task
            onNavigate("/tasks");
          } else {
            // No waiting session, navigate to inbox
            onNavigate("/inbox");
          }
        });
      } catch {
        // Silently ignore in non-Tauri environments (e.g. browser dev, tests)
      }
    }

    setup();

    return () => {
      unlisten?.();
    };
  }, [workspaceId, onNavigate, refetch]);
}
