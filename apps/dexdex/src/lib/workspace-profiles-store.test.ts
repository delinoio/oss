import { beforeEach, describe, expect, it } from "vitest";
import { WorkspaceMode } from "../contracts/workspace-mode";
import {
  deleteWorkspaceProfile,
  listSavedWorkspaceProfiles,
  upsertWorkspaceProfile,
  WORKSPACE_PROFILES_STORAGE_KEY,
} from "./workspace-profiles-store";

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key) {
      return values.has(key) ? values.get(key) ?? null : null;
    },
    key(index) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key) {
      values.delete(key);
    },
    setItem(key, value) {
      values.set(key, value);
    },
  };
}

describe("workspace profiles store", () => {
  beforeEach(() => {
    Object.defineProperty(window, "localStorage", {
      value: createMemoryStorage(),
      configurable: true,
    });
    window.localStorage.clear();
  });

  it("returns empty list when no profiles are stored", () => {
    expect(listSavedWorkspaceProfiles()).toEqual([]);
  });

  it("keeps profiles deduped and recency ordered on upsert", () => {
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-a",
        mode: WorkspaceMode.Local,
      },
      { now: new Date("2026-03-07T00:00:00.000Z") },
    );
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-b",
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://dexdex.example/rpc",
      },
      { now: new Date("2026-03-07T01:00:00.000Z") },
    );
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-a",
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://override.example/rpc",
      },
      { now: new Date("2026-03-07T02:00:00.000Z") },
    );

    expect(listSavedWorkspaceProfiles()).toEqual([
      {
        workspaceId: "workspace-a",
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://override.example/rpc",
        lastUsedAt: "2026-03-07T02:00:00.000Z",
      },
      {
        workspaceId: "workspace-b",
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://dexdex.example/rpc",
        lastUsedAt: "2026-03-07T01:00:00.000Z",
      },
    ]);
  });

  it("deletes profile by workspace id", () => {
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-a",
        mode: WorkspaceMode.Local,
      },
      { now: new Date("2026-03-07T00:00:00.000Z") },
    );
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-b",
        mode: WorkspaceMode.Local,
      },
      { now: new Date("2026-03-07T01:00:00.000Z") },
    );

    deleteWorkspaceProfile("workspace-a");

    expect(listSavedWorkspaceProfiles()).toEqual([
      {
        workspaceId: "workspace-b",
        mode: WorkspaceMode.Local,
        remoteEndpointUrl: undefined,
        lastUsedAt: "2026-03-07T01:00:00.000Z",
      },
    ]);
  });

  it("never persists token fields to local storage payloads", () => {
    upsertWorkspaceProfile(
      {
        workspaceId: "workspace-a",
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://dexdex.example/rpc",
        token: "should-not-be-persisted",
      } as unknown as {
        workspaceId: string;
        mode: WorkspaceMode;
        remoteEndpointUrl?: string;
      },
      { now: new Date("2026-03-07T00:00:00.000Z") },
    );

    const raw = window.localStorage.getItem(WORKSPACE_PROFILES_STORAGE_KEY);
    expect(raw).toBeTruthy();
    expect(raw).not.toContain("token");
  });
});
