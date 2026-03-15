/**
 * Draft form state store with localStorage persistence.
 * Preserves form state per workspace across dialog open/close cycles.
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";
import { AgentCliType } from "../gen/v1/dexdex_pb";

interface CreateTaskDraft {
  prompt: string;
  repositoryGroupId: string;
  agentCliType: AgentCliType;
  usePlanMode: boolean;
}

interface DraftState {
  drafts: Record<string, CreateTaskDraft>;
  setDraft: (workspaceId: string, draft: CreateTaskDraft) => void;
  clearDraft: (workspaceId: string) => void;
  getDraft: (workspaceId: string) => CreateTaskDraft | null;
}

export const useDraftStore = create<DraftState>()(
  persist(
    (set, get) => ({
      drafts: {},
      setDraft: (workspaceId: string, draft: CreateTaskDraft) => {
        set((state) => ({
          drafts: { ...state.drafts, [workspaceId]: draft },
        }));
      },
      clearDraft: (workspaceId: string) => {
        set((state) => {
          const { [workspaceId]: _, ...rest } = state.drafts;
          return { drafts: rest };
        });
      },
      getDraft: (workspaceId: string) => {
        return get().drafts[workspaceId] ?? null;
      },
    }),
    {
      name: "dexdex-draft-forms",
    },
  ),
);
