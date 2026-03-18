/**
 * Hook for updating the menu bar tray icon status.
 * Calls the Tauri update_tray_status command when workspace work status changes.
 */

import { useEffect, useRef } from "react";
import { useGetWorkspaceWorkStatus } from "./use-dexdex-queries";
import { WorkspaceWorkStatus as ProtoWorkspaceWorkStatus } from "../gen/v1/dexdex_pb";

const STATUS_TO_STRING: Record<number, string> = {
  [ProtoWorkspaceWorkStatus.FAILED]: "FAILED",
  [ProtoWorkspaceWorkStatus.ACTION_REQUIRED]: "ACTION_REQUIRED",
  [ProtoWorkspaceWorkStatus.WAITING_FOR_INPUT]: "WAITING_FOR_INPUT",
  [ProtoWorkspaceWorkStatus.RUNNING]: "RUNNING",
  [ProtoWorkspaceWorkStatus.IDLE]: "IDLE",
  [ProtoWorkspaceWorkStatus.DISCONNECTED]: "DISCONNECTED",
};

async function invokeTrayUpdate(status: string): Promise<void> {
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    await invoke("update_tray_status", { status });
  } catch {
    // Silently ignore in non-Tauri environments (e.g. browser dev, tests)
  }
}

export function useTrayStatus(workspaceId: string): void {
  const { data } = useGetWorkspaceWorkStatus(workspaceId);
  const prevStatusRef = useRef<number | null>(null);
  const normalizedWorkspaceId = workspaceId.trim();

  useEffect(() => {
    if (!normalizedWorkspaceId) {
      prevStatusRef.current = null;
      return;
    }

    const status = data?.status ?? ProtoWorkspaceWorkStatus.IDLE;
    if (prevStatusRef.current === status) return;
    prevStatusRef.current = status;

    const statusStr = STATUS_TO_STRING[status] ?? "IDLE";
    invokeTrayUpdate(statusStr);
  }, [data?.status, normalizedWorkspaceId]);
}
