/**
 * Global keyboard shortcuts hook.
 * Registers shortcuts for navigation and actions.
 */

import { useCallback, useEffect, useRef } from "react";

interface KeyboardShortcutsConfig {
  onCommandPalette: () => void;
  onToggleSidebar: () => void;
  onNavigate: (path: string) => void;
  onCreateTask: () => void;
}

/**
 * Hook that registers global keyboard shortcuts.
 *
 * Shortcuts:
 * - Cmd/Ctrl+K: Open command palette
 * - Cmd/Ctrl+B: Toggle sidebar
 * - G then T: Go to tasks
 * - G then I: Go to inbox
 * - C: Create new task (when not in an input)
 */
export function useKeyboardShortcuts(config: KeyboardShortcutsConfig): void {
  const pendingG = useRef(false);
  const gTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearPendingG = useCallback(() => {
    pendingG.current = false;
    if (gTimer.current) {
      clearTimeout(gTimer.current);
      gTimer.current = null;
    }
  }, []);

  useEffect(() => {
    function isInputFocused(): boolean {
      const active = document.activeElement;
      if (!active) return false;
      const tag = active.tagName.toLowerCase();
      return tag === "input" || tag === "textarea" || (active as HTMLElement).isContentEditable;
    }

    function handleKeyDown(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;

      // Cmd/Ctrl+K: Command palette
      if (meta && e.key === "k") {
        e.preventDefault();
        config.onCommandPalette();
        clearPendingG();
        return;
      }

      // Cmd/Ctrl+B: Toggle sidebar
      if (meta && e.key === "b") {
        e.preventDefault();
        config.onToggleSidebar();
        clearPendingG();
        return;
      }

      // Skip single-char shortcuts when input is focused
      if (isInputFocused()) {
        clearPendingG();
        return;
      }

      // G then T / G then I sequences
      if (pendingG.current) {
        clearPendingG();
        if (e.key === "t" || e.key === "T") {
          e.preventDefault();
          config.onNavigate("/tasks");
          return;
        }
        if (e.key === "i" || e.key === "I") {
          e.preventDefault();
          config.onNavigate("/inbox");
          return;
        }
        // Any other key cancels the G sequence
        return;
      }

      // Start G sequence
      if ((e.key === "g" || e.key === "G") && !meta && !e.altKey) {
        pendingG.current = true;
        // Auto-cancel after 1 second
        gTimer.current = setTimeout(clearPendingG, 1000);
        return;
      }

      // C: Create new task
      if ((e.key === "c" || e.key === "C") && !meta && !e.altKey && !e.shiftKey) {
        e.preventDefault();
        config.onCreateTask();
        return;
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      clearPendingG();
    };
  }, [config, clearPendingG]);
}
