// SPDX-License-Identifier: Apache-2.0
import { AppearanceProblem, Theme, parseAppearance, type AppearanceBridge, type AppearanceSnapshot } from "../appearance";

export async function hostRequest<T>(path: string, value?: unknown): Promise<T> {
  const response = await fetch(`/__qa/${path}`, { method: value === undefined ? "GET" : "POST", credentials: "omit", cache: "no-store", redirect: "error", ...(value === undefined ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(value) }) });
  if (!response.ok) throw new Error("QA host operation is unavailable. Refresh the original environment status.");
  return response.json() as Promise<T>;
}
export function appearanceBridge(serverId: string): AppearanceBridge {
  const key = `delidev-qa-appearance:${serverId}`;
  const read = (): AppearanceSnapshot => {
    const raw = window.localStorage.getItem(key);
    return raw ? parseAppearance(JSON.parse(raw)) : { theme: Theme.System, revision: 0, problem: null };
  };
  const subscribers = new Set<(snapshot: unknown) => void>();
  return {
    read: async () => read(),
    update: async (theme, revision) => {
      const current = read();
      if (current.revision !== revision) return { ...current, problem: AppearanceProblem.Changed };
      const next = parseAppearance({ theme, revision: revision + 1, problem: null });
      window.localStorage.setItem(key, JSON.stringify(next)); subscribers.forEach(changed => changed(next)); return next;
    },
    subscribe: async changed => {
      subscribers.add(changed);
      const listener = (event: StorageEvent) => { if (event.storageArea === window.localStorage && event.key === key) { try { changed(read()); } catch { changed({ theme: Theme.System, revision: 0, problem: AppearanceProblem.InvalidDocument }); } } };
      window.addEventListener("storage", listener);
      return () => { subscribers.delete(changed); window.removeEventListener("storage", listener); };
    },
  };
}
