/**
 * Hook for handling global shortcut events from Tauri.
 * Listens for Cmd/Ctrl+Shift+I shortcut and navigates to the latest waiting session's
 * task detail, or falls back to inbox with empty state.
 */

import { useEffect } from "react";
import { useGetLatestWaitingSession } from "./use-dexdex-queries";

interface UseGlobalShortcutOptions {
  workspaceId: string;
  onNavigate: (path: string) => void;
  onFocusInput?: () => void;
}

export function useGlobalShortcut({ workspaceId, onNavigate, onFocusInput }: UseGlobalShortcutOptions): void {
  const { refetch } = useGetLatestWaitingSession(workspaceId);
  const normalizedWorkspaceId = workspaceId.trim();

  useEffect(() => {
    let unlisten: (() => void) | undefined;

    async function setup() {
      try {
        const { listen } = await import("@tauri-apps/api/event");
        unlisten = await listen("dexdex://global-shortcut-input", async () => {
          if (!normalizedWorkspaceId) {
            onNavigate("/inbox");
            return;
          }

          // Refetch to get the latest waiting session
          const result = await refetch();
          const session = result.data?.session;

          if (session && session.sessionId) {
            // Navigate to the tasks page with the waiting session context.
            // The session ID is passed as a query parameter so the task detail
            // view can highlight and auto-focus the input form.
            onNavigate(`/tasks?waitingSession=${session.sessionId}`);
            // Auto-focus the input form after navigation.
            if (onFocusInput) {
              setTimeout(onFocusInput, 100);
            }
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
  }, [normalizedWorkspaceId, onNavigate, onFocusInput, refetch]);
}
