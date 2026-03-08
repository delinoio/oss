export enum DexDexPageId {
  Threads = "THREADS",
  Review = "REVIEW",
  Automations = "AUTOMATIONS",
  Settings = "SETTINGS",
}

export type DexDexPageDefinition = {
  id: DexDexPageId;
  path: string;
  label: string;
};

export const dexdexPageDefinitions: ReadonlyArray<DexDexPageDefinition> = [
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
    id: DexDexPageId.Settings,
    path: "/settings",
    label: "Settings",
  },
];
