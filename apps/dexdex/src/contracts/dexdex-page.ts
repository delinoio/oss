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
};

export const dexdexPageDefinitions: ReadonlyArray<DexDexPageDefinition> = [
  {
    id: DexDexPageId.Projects,
    path: "/projects",
    label: "Projects",
  },
  {
    id: DexDexPageId.Threads,
    path: "/threads",
    label: "Threads",
  },
  {
    id: DexDexPageId.Review,
    path: "/review",
    label: "Review",
  },
  {
    id: DexDexPageId.Automations,
    path: "/automations",
    label: "Automations",
  },
  {
    id: DexDexPageId.Worktrees,
    path: "/worktrees",
    label: "Worktrees",
  },
  {
    id: DexDexPageId.LocalEnvironments,
    path: "/local-environments",
    label: "Local Environments",
  },
  {
    id: DexDexPageId.Settings,
    path: "/settings",
    label: "Settings",
  },
];
