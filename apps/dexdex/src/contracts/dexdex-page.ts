export enum DexDexPageId {
  Projects = "PROJECTS",
  Threads = "THREADS",
  Review = "REVIEW",
  Automations = "AUTOMATIONS",
  Worktrees = "WORKTREES",
  LocalEnvironments = "LOCAL_ENVIRONMENTS",
  Settings = "SETTINGS",
}

export type DexDexPageDefinition = {
  id: DexDexPageId;
  path: string;
  label: string;
  description: string;
};

export const dexdexPageDefinitions: ReadonlyArray<DexDexPageDefinition> = [
  {
    id: DexDexPageId.Projects,
    path: "/projects",
    label: "Projects",
    description: "Workspace and repository contract views",
  },
  {
    id: DexDexPageId.Threads,
    path: "/threads",
    label: "Threads",
    description: "Task, session, and plan decision workflows",
  },
  {
    id: DexDexPageId.Review,
    path: "/review",
    label: "Review",
    description: "PR, review assist, and comment lookups",
  },
  {
    id: DexDexPageId.Automations,
    path: "/automations",
    label: "Automations",
    description: "Scheduled workflow skeleton view",
  },
  {
    id: DexDexPageId.Worktrees,
    path: "/worktrees",
    label: "Worktrees",
    description: "Session adapter and stream monitoring tools",
  },
  {
    id: DexDexPageId.LocalEnvironments,
    path: "/local-environments",
    label: "Local Environments",
    description: "Workspace mode and endpoint resolution",
  },
  {
    id: DexDexPageId.Settings,
    path: "/settings",
    label: "Settings",
    description: "Desktop policy and preference skeleton view",
  },
];

