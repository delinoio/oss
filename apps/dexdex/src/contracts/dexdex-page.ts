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
    description: "Workspace overview, repository groups, and risk summary",
  },
  {
    id: DexDexPageId.Threads,
    path: "/threads",
    label: "Threads",
    description: "Inbox, detail timeline, and execution workflows",
  },
  {
    id: DexDexPageId.Review,
    path: "/review",
    label: "Review",
    description: "Pull request queue with review context",
  },
  {
    id: DexDexPageId.Automations,
    path: "/automations",
    label: "Automations",
    description: "Persistent automation records and schedule management",
  },
  {
    id: DexDexPageId.Worktrees,
    path: "/worktrees",
    label: "Worktrees",
    description: "Session runs and live stream timeline",
  },
  {
    id: DexDexPageId.LocalEnvironments,
    path: "/local-environments",
    label: "Local Environments",
    description: "Endpoint profiles and diagnostics",
  },
  {
    id: DexDexPageId.Settings,
    path: "/settings",
    label: "Settings",
    description: "Persistent desktop preferences",
  },
];
