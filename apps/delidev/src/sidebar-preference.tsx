// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  PresentationFailureLog,
  PresentationPhase,
} from "./presentation-diagnostics";
export enum SidebarPreference {
  Expanded = "expanded",
  Collapsed = "collapsed",
}
export enum SidebarProblem {
  Unavailable = "unavailable",
  ReadFailed = "read-failed",
  InvalidDocument = "invalid-document",
  UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed",
  OutcomeUnknown = "outcome-unknown",
  Changed = "changed",
}
// A read, inspection or document problem belongs to the read phase; only the
// classifications of a rejected, uncertain or contradicted save belong to the
// save phase.
const sidebarProblemPhases: Record<SidebarProblem, PresentationPhase> = {
  [SidebarProblem.Unavailable]: PresentationPhase.SidebarRead,
  [SidebarProblem.ReadFailed]: PresentationPhase.SidebarRead,
  [SidebarProblem.InvalidDocument]: PresentationPhase.SidebarRead,
  [SidebarProblem.UnsupportedVersion]: PresentationPhase.SidebarRead,
  [SidebarProblem.WriteFailed]: PresentationPhase.SidebarSave,
  [SidebarProblem.OutcomeUnknown]: PresentationPhase.SidebarSave,
  [SidebarProblem.Changed]: PresentationPhase.SidebarSave,
};
const sidebarPhases = [
  PresentationPhase.SidebarRead,
  PresentationPhase.SidebarSave,
] as const;
// Only these classifications describe the outcome of the original save, so only
// they fence a reply that is older than an already accepted event.
const saveFailureProblems: ReadonlySet<SidebarProblem> = new Set([
  SidebarProblem.Changed,
  SidebarProblem.WriteFailed,
  SidebarProblem.OutcomeUnknown,
]);
enum SidebarOperation {
  Reading = "reading",
  Saving = "saving",
}
export interface SidebarSnapshot {
  revision: number;
  sidebar_preference: SidebarPreference;
  problem: SidebarProblem | null;
}
export interface SidebarBridge {
  read: () => Promise<unknown>;
  update: (
    sidebar_preference: SidebarPreference,
    revision: number,
  ) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultSnapshot: SidebarSnapshot = {
  revision: 0,
  sidebar_preference: SidebarPreference.Expanded,
  problem: SidebarProblem.Unavailable,
};
const nativeBridge: SidebarBridge = {
  read: () =>
    isTauri()
      ? invoke("read_sidebar_preference")
      : Promise.resolve(defaultSnapshot),
  update: (sidebar_preference, revision) =>
    invoke("update_sidebar_preference", {
      sidebarPreference: sidebar_preference,
      expectedRevision: revision,
    }),
  subscribe: async (changed) =>
    isTauri()
      ? listen<unknown>("sidebar-preference-changed", (event) =>
          changed(event.payload),
        )
      : () => {},
};
export function parseSidebar(value: unknown): SidebarSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid sidebar-preference snapshot");
  const data = value as Record<string, unknown>;
  if (
    Object.keys(data).sort().join(",") !==
      "problem,revision,sidebar_preference" ||
    !Number.isInteger(data.revision) ||
    Number(data.revision) < 0 ||
    Number(data.revision) > 0xffffffff ||
    !Object.values(SidebarPreference).includes(
      data.sidebar_preference as SidebarPreference,
    ) ||
    (data.problem !== null &&
      !Object.values(SidebarProblem).includes(data.problem as SidebarProblem))
  )
    throw new Error("Invalid sidebar-preference snapshot");
  return data as unknown as SidebarSnapshot;
}

interface SidebarController {
  snapshot: SidebarSnapshot;
  operation?: SidebarOperation;
  select: (sidebar_preference: SidebarPreference) => boolean;
  reload: () => void;
}
const SidebarContext = createContext<SidebarController | undefined>(undefined);

export function SidebarProvider({
  children,
  bridge: suppliedBridge,
}: {
  children: ReactNode;
  bridge?: SidebarBridge;
}) {
  const [fallbackBridge] = useState(() =>
    isTauri() ? nativeBridge : memorySidebarBridge(),
  );
  const bridge = suppliedBridge ?? fallbackBridge;
  const [snapshot, setSnapshot] = useState(defaultSnapshot);
  const current = useRef(defaultSnapshot);
  const [operation, setOperation] = useState<SidebarOperation | undefined>(
    SidebarOperation.Reading,
  );
  const running = useRef(false);
  const generation = useRef(0);
  const subscription = useRef<(() => void) | undefined>(undefined);
  // The product window shows no sidebar problem, so the shared reducer records
  // each phase transition. The initial Unavailable value is only the loading
  // placeholder: it is never recorded, while a real Unavailable snapshot is
  // recorded like any other problem.
  const [diagnostics] = useState(() => new PresentationFailureLog());
  const record = (next: SidebarSnapshot) => {
    if (next.problem) diagnostics.failed(sidebarProblemPhases[next.problem]);
    else sidebarPhases.forEach((phase) => diagnostics.recovered(phase));
  };
  const accept = (value: unknown) => {
    const next = parseSidebar(value);
    if (next.revision < current.current.revision) return;
    if (
      next.revision === current.current.revision &&
      next.sidebar_preference !== current.current.sidebar_preference
    )
      throw new Error("Contradictory sidebar-preference revision");
    record(next);
    current.current = next;
    setSnapshot(next);
  };
  const failed = (problem: SidebarProblem) => {
    const next = { ...current.current, problem };
    record(next);
    current.current = next;
    setSnapshot(next);
  };
  const subscribe = async (owned: number) => {
    const remove = await bridge.subscribe((value) => {
      // A later delivery does not prove an uncertain write. Recovery remains
      // explicit even when another window publishes a healthy snapshot.
      if (
        generation.current !== owned ||
        (current.current.problem &&
          current.current.problem !== SidebarProblem.Unavailable)
      )
        return;
      try {
        accept(value);
      } catch {
        failed(SidebarProblem.OutcomeUnknown);
      }
    });
    if (generation.current !== owned) {
      remove();
      return false;
    }
    subscription.current = remove;
    return true;
  };
  useEffect(() => {
    const owned = ++generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(SidebarOperation.Reading);
    void (async () => {
      try {
        // Listen before reading so no committed change is lost in the gap.
        if (!(await subscribe(owned))) return;
        const value = await bridge.read();
        if (generation.current === owned) accept(value);
      } catch {
        if (generation.current === owned && current.current.revision <= before)
          failed(SidebarProblem.ReadFailed);
      } finally {
        if (generation.current === owned) {
          running.current = false;
          setOperation(undefined);
        }
      }
    })();
    return () => {
      generation.current++;
      subscription.current?.();
      subscription.current = undefined;
    };
  }, [bridge]);
  const run = async (sidebar_preference?: SidebarPreference) => {
    if (
      running.current ||
      (sidebar_preference !== undefined && current.current.problem)
    )
      return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(
      sidebar_preference === undefined
        ? SidebarOperation.Reading
        : SidebarOperation.Saving,
    );
    try {
      if (!subscription.current && !(await subscribe(owned))) return;
      const value =
        sidebar_preference === undefined
          ? await bridge.read()
          : await bridge.update(sidebar_preference, current.current.revision);
      if (generation.current === owned) {
        if (sidebar_preference !== undefined) {
          // A structured save failure for the original save is not an ordinary
          // stale snapshot. Even when a newer healthy event was already
          // accepted, fence on the latest presentation without regressing it.
          // Inspection failures stay ordinary stale snapshots.
          const reply = parseSidebar(value);
          if (
            reply.problem &&
            saveFailureProblems.has(reply.problem) &&
            reply.revision < current.current.revision
          ) {
            failed(reply.problem);
            return;
          }
        }
        accept(value);
      }
    } catch {
      // A newer event has no correlation with this save. Its revision cannot
      // settle a rejected original write or admit another save.
      if (
        generation.current === owned &&
        (sidebar_preference !== undefined || current.current.revision <= before)
      )
        failed(
          sidebar_preference === undefined
            ? SidebarProblem.ReadFailed
            : SidebarProblem.OutcomeUnknown,
        );
    } finally {
      if (generation.current === owned) {
        running.current = false;
        setOperation(undefined);
      }
    }
  };
  useEffect(() => {
    const inspect = () => {
      // Recover a missed event when a retained window becomes visible. Errors
      // and uncertain saves still require the explicit Reload action.
      if (document.visibilityState === "visible" && !current.current.problem)
        void run();
    };
    window.addEventListener("focus", inspect);
    document.addEventListener("visibilitychange", inspect);
    return () => {
      window.removeEventListener("focus", inspect);
      document.removeEventListener("visibilitychange", inspect);
    };
  }, [bridge]);
  return (
    <SidebarContext.Provider
      value={{
        snapshot,
        operation,
        select: (sidebar_preference) => {
          if (running.current || current.current.problem) return false;
          void run(sidebar_preference);
          return true;
        },
        reload: () => {
          void run();
        },
      }}
    >
      {children}
    </SidebarContext.Provider>
  );
}

// Browser fixtures use only process memory and confer no restart persistence.
export function memorySidebarBridge(): SidebarBridge {
  let current: SidebarSnapshot = {
    revision: 1,
    sidebar_preference: SidebarPreference.Expanded,
    problem: null,
  };
  const listeners = new Set<(value: unknown) => void>();
  return {
    read: async () => current,
    update: async (sidebar_preference, revision) => {
      if (revision !== current.revision)
        return { ...current, problem: SidebarProblem.Changed };
      current = { revision: revision + 1, sidebar_preference, problem: null };
      listeners.forEach((changed) => changed(current));
      return current;
    },
    subscribe: async (changed) => {
      listeners.add(changed);
      return () => {
        listeners.delete(changed);
      };
    },
  };
}
export function SidebarPreferenceBoundary({
  children,
}: {
  children: ReactNode;
}) {
  return useContext(SidebarContext) ? (
    children
  ) : (
    <SidebarProvider>{children}</SidebarProvider>
  );
}
export function useSidebarPreference() {
  const controller = useContext(SidebarContext);
  if (!controller) throw new Error("Missing sidebar preference owner");
  return controller;
}
export function useWideSidebar() {
  const [wide, setWide] = useState(
    () =>
      typeof window.matchMedia !== "function" ||
      !window.matchMedia("(max-width: 759px)").matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(max-width: 759px)");
    const update = () => setWide(!media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return wide;
}
