import type { SavedWorkspaceProfile } from "../contracts/workspace-profile";
import { WorkspaceMode } from "../contracts/workspace-mode";

export const WORKSPACE_PROFILES_STORAGE_KEY = "dexdex.desktop.workspace-profiles.v1";

type WorkspaceProfilesStoreOptions = {
  storage?: Storage;
};

type UpsertWorkspaceProfileOptions = WorkspaceProfilesStoreOptions & {
  now?: Date;
};

export type UpsertWorkspaceProfileInput = {
  workspaceId: string;
  mode: WorkspaceMode;
  remoteEndpointUrl?: string;
};

function resolveStorage(storage?: Storage): Storage | null {
  if (storage) {
    return storage;
  }

  if (typeof window === "undefined") {
    return null;
  }

  return window.localStorage;
}

function isWorkspaceMode(value: unknown): value is WorkspaceMode {
  return value === WorkspaceMode.Local || value === WorkspaceMode.Remote;
}

function normalizeWorkspaceId(workspaceId: string): string {
  const normalized = workspaceId.trim();
  if (normalized.length === 0) {
    throw new Error("workspaceId must not be empty.");
  }
  return normalized;
}

function normalizeRemoteEndpointUrl(
  mode: WorkspaceMode,
  remoteEndpointUrl?: string,
): string | undefined {
  if (mode === WorkspaceMode.Local) {
    return undefined;
  }

  if (typeof remoteEndpointUrl !== "string") {
    return undefined;
  }

  const normalized = remoteEndpointUrl.trim();
  return normalized.length > 0 ? normalized : undefined;
}

function timestampFromISO(value: string): number {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function compareByRecent(first: SavedWorkspaceProfile, second: SavedWorkspaceProfile): number {
  return timestampFromISO(second.lastUsedAt) - timestampFromISO(first.lastUsedAt);
}

function parseSavedProfiles(rawValue: string | null): SavedWorkspaceProfile[] {
  if (!rawValue) {
    return [];
  }

  try {
    const parsed = JSON.parse(rawValue) as unknown;
    if (!Array.isArray(parsed)) {
      return [];
    }

    const profiles: SavedWorkspaceProfile[] = [];
    for (const candidate of parsed) {
      if (typeof candidate !== "object" || candidate === null) {
        continue;
      }

      const workspaceIdRaw = Reflect.get(candidate, "workspaceId");
      const modeRaw = Reflect.get(candidate, "mode");
      const remoteEndpointUrlRaw = Reflect.get(candidate, "remoteEndpointUrl");
      const lastUsedAtRaw = Reflect.get(candidate, "lastUsedAt");

      if (typeof workspaceIdRaw !== "string" || typeof lastUsedAtRaw !== "string") {
        continue;
      }
      if (!isWorkspaceMode(modeRaw)) {
        continue;
      }

      const workspaceId = workspaceIdRaw.trim();
      if (workspaceId.length === 0) {
        continue;
      }

      const remoteEndpointUrl =
        typeof remoteEndpointUrlRaw === "string" && remoteEndpointUrlRaw.trim().length > 0
          ? remoteEndpointUrlRaw.trim()
          : undefined;

      profiles.push({
        workspaceId,
        mode: modeRaw,
        remoteEndpointUrl:
          modeRaw === WorkspaceMode.Remote ? remoteEndpointUrl : undefined,
        lastUsedAt: lastUsedAtRaw,
      });
    }

    profiles.sort(compareByRecent);
    return dedupeProfiles(profiles);
  } catch {
    return [];
  }
}

function dedupeProfiles(profiles: SavedWorkspaceProfile[]): SavedWorkspaceProfile[] {
  const deduped = new Map<string, SavedWorkspaceProfile>();
  for (const profile of profiles) {
    if (!deduped.has(profile.workspaceId)) {
      deduped.set(profile.workspaceId, profile);
    }
  }

  return Array.from(deduped.values());
}

function writeProfiles(storage: Storage, profiles: SavedWorkspaceProfile[]): SavedWorkspaceProfile[] {
  const sortedProfiles = [...profiles].sort(compareByRecent);
  const dedupedProfiles = dedupeProfiles(sortedProfiles);
  storage.setItem(WORKSPACE_PROFILES_STORAGE_KEY, JSON.stringify(dedupedProfiles));
  return dedupedProfiles;
}

export function listSavedWorkspaceProfiles(
  options?: WorkspaceProfilesStoreOptions,
): SavedWorkspaceProfile[] {
  const storage = resolveStorage(options?.storage);
  if (!storage) {
    return [];
  }

  return parseSavedProfiles(storage.getItem(WORKSPACE_PROFILES_STORAGE_KEY));
}

export function upsertWorkspaceProfile(
  input: UpsertWorkspaceProfileInput,
  options?: UpsertWorkspaceProfileOptions,
): SavedWorkspaceProfile[] {
  const storage = resolveStorage(options?.storage);
  if (!storage) {
    return [];
  }

  const workspaceId = normalizeWorkspaceId(input.workspaceId);
  const mode = input.mode;
  const remoteEndpointUrl = normalizeRemoteEndpointUrl(mode, input.remoteEndpointUrl);
  const lastUsedAt = (options?.now ?? new Date()).toISOString();

  const currentProfiles = listSavedWorkspaceProfiles({ storage });
  const nextProfile: SavedWorkspaceProfile = {
    workspaceId,
    mode,
    remoteEndpointUrl,
    lastUsedAt,
  };

  const remaining = currentProfiles.filter((profile) => profile.workspaceId !== workspaceId);
  return writeProfiles(storage, [nextProfile, ...remaining]);
}

export function deleteWorkspaceProfile(
  workspaceId: string,
  options?: WorkspaceProfilesStoreOptions,
): SavedWorkspaceProfile[] {
  const storage = resolveStorage(options?.storage);
  if (!storage) {
    return [];
  }

  const normalizedWorkspaceId = workspaceId.trim();
  const currentProfiles = listSavedWorkspaceProfiles({ storage });
  const remainingProfiles = currentProfiles.filter(
    (profile) => profile.workspaceId !== normalizedWorkspaceId,
  );

  return writeProfiles(storage, remainingProfiles);
}

