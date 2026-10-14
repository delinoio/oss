// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
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
export enum NavigationProblem {
  Unavailable = "unavailable",
  ReadFailed = "read-failed",
  InvalidDocument = "invalid-document",
  UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed",
  OutcomeUnknown = "outcome-unknown",
  Changed = "changed",
}
// Only these classifications describe the outcome of the original save, so only
// they fence a reply that is older than an already accepted event.
const navigationSaveFailureProblems: ReadonlySet<NavigationProblem> = new Set([
  NavigationProblem.Changed,
  NavigationProblem.WriteFailed,
  NavigationProblem.OutcomeUnknown,
]);
enum NavigationOperation {
  Reading = "reading",
  Saving = "saving",
}
export interface NavigationSnapshot {
  revision: number;
  usage_pinned: boolean;
  problem: NavigationProblem | null;
}
export interface NavigationBridge {
  read: () => Promise<unknown>;
  update: (usage_pinned: boolean, revision: number) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultNavigationSnapshot: NavigationSnapshot = {
  revision: 0,
  usage_pinned: false,
  problem: NavigationProblem.Unavailable,
};
const nativeNavigationBridge: NavigationBridge = {
  read: () =>
    isTauri()
      ? invoke("read_navigation_preference")
      : Promise.resolve(defaultNavigationSnapshot),
  update: (usage_pinned, revision) =>
    invoke("update_navigation_preference", {
      usagePinned: usage_pinned,
      expectedRevision: revision,
    }),
  subscribe: async (changed) =>
    isTauri()
      ? listen<unknown>("navigation-preference-changed", (event) =>
          changed(event.payload),
        )
      : () => {},
};
const navigationProblemMessages: Record<NavigationProblem, string> = {
  get [NavigationProblem.Unavailable]() {
    return copy("sidebar.navigation.unavailable");
  },
  get [NavigationProblem.ReadFailed]() {
    return copy("sidebar.navigation.readFailed");
  },
  get [NavigationProblem.InvalidDocument]() {
    return copy("sidebar.navigation.invalidDocument");
  },
  get [NavigationProblem.UnsupportedVersion]() {
    return copy("sidebar.navigation.unsupportedVersion");
  },
  get [NavigationProblem.WriteFailed]() {
    return copy("sidebar.navigation.writeFailed");
  },
  get [NavigationProblem.OutcomeUnknown]() {
    return copy("sidebar.navigation.outcomeUnknown");
  },
  get [NavigationProblem.Changed]() {
    return copy("sidebar.navigation.changed");
  },
};

export function parseNavigation(value: unknown): NavigationSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid navigation-preference snapshot");
  const data = value as Record<string, unknown>;
  if (
    Object.keys(data).sort().join(",") !== "problem,revision,usage_pinned" ||
    !Number.isInteger(data.revision) ||
    Number(data.revision) < 0 ||
    Number(data.revision) > 0xffffffff ||
    typeof data.usage_pinned !== "boolean" ||
    (data.problem !== null &&
      !Object.values(NavigationProblem).includes(
        data.problem as NavigationProblem,
      ))
  )
    throw new Error("Invalid navigation-preference snapshot");
  return data as unknown as NavigationSnapshot;
}

interface NavigationController {
  snapshot: NavigationSnapshot;
  operation?: NavigationOperation;
  select: (usage_pinned: boolean) => boolean;
  reload: () => void;
}
const NavigationContext = createContext<NavigationController | undefined>(
  undefined,
);

export function NavigationProvider({
  children,
  bridge: suppliedBridge,
}: {
  children: ReactNode;
  bridge?: NavigationBridge;
}) {
  useLocale();
  const [fallbackBridge] = useState(() =>
    isTauri() ? nativeNavigationBridge : memoryNavigationBridge(),
  );
  const bridge = suppliedBridge ?? fallbackBridge;
  const [snapshot, setSnapshot] = useState(defaultNavigationSnapshot);
  const current = useRef(defaultNavigationSnapshot);
  const [operation, setOperation] = useState<NavigationOperation | undefined>(
    NavigationOperation.Reading,
  );
  const running = useRef(false);
  const generation = useRef(0);
  const subscription = useRef<(() => void) | undefined>(undefined);
  const accept = (value: unknown) => {
    const next = parseNavigation(value);
    if (next.revision < current.current.revision) return;
    if (
      next.revision === current.current.revision &&
      next.usage_pinned !== current.current.usage_pinned
    )
      throw new Error("Contradictory navigation-preference revision");
    current.current = next;
    setSnapshot(next);
  };
  const failed = (problem: NavigationProblem) => {
    const next = { ...current.current, problem };
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
          current.current.problem !== NavigationProblem.Unavailable)
      )
        return;
      try {
        accept(value);
      } catch {
        failed(NavigationProblem.OutcomeUnknown);
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
    setOperation(NavigationOperation.Reading);
    void (async () => {
      try {
        // Listen before reading so no committed change is lost in the gap.
        if (!(await subscribe(owned))) return;
        const value = await bridge.read();
        if (generation.current === owned) accept(value);
      } catch {
        if (generation.current === owned && current.current.revision <= before)
          failed(NavigationProblem.ReadFailed);
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
  const run = async (usage_pinned?: boolean) => {
    if (
      running.current ||
      (usage_pinned !== undefined && current.current.problem)
    )
      return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(
      usage_pinned === undefined
        ? NavigationOperation.Reading
        : NavigationOperation.Saving,
    );
    try {
      if (!subscription.current && !(await subscribe(owned))) return;
      const value =
        usage_pinned === undefined
          ? await bridge.read()
          : await bridge.update(usage_pinned, current.current.revision);
      if (generation.current === owned) {
        if (usage_pinned !== undefined) {
          // A structured save failure for the original save is not an ordinary
          // stale snapshot. Even when a newer healthy event was already
          // accepted, fence on the latest presentation without regressing it.
          // Inspection failures stay ordinary stale snapshots.
          const reply = parseNavigation(value);
          if (
            reply.problem &&
            navigationSaveFailureProblems.has(reply.problem) &&
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
        (usage_pinned !== undefined || current.current.revision <= before)
      )
        failed(
          usage_pinned === undefined
            ? NavigationProblem.ReadFailed
            : NavigationProblem.OutcomeUnknown,
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
    <NavigationContext.Provider
      value={{
        snapshot,
        operation,
        select: (usage_pinned) => {
          if (running.current || current.current.problem) return false;
          void run(usage_pinned);
          return true;
        },
        reload: () => {
          void run();
        },
      }}
    >
      {children}
    </NavigationContext.Provider>
  );
}

// Browser fixtures use only process memory and confer no restart persistence.
export function memoryNavigationBridge(): NavigationBridge {
  let current: NavigationSnapshot = {
    revision: 1,
    usage_pinned: false,
    problem: null,
  };
  const listeners = new Set<(value: unknown) => void>();
  return {
    read: async () => current,
    update: async (usage_pinned, revision) => {
      if (revision !== current.revision)
        return { ...current, problem: NavigationProblem.Changed };
      current = { revision: revision + 1, usage_pinned, problem: null };
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
export function NavigationPreferenceBoundary({
  children,
}: {
  children: ReactNode;
}) {
  return useContext(NavigationContext) ? (
    children
  ) : (
    <NavigationProvider>{children}</NavigationProvider>
  );
}
export function useNavigationPreference() {
  const controller = useContext(NavigationContext);
  if (!controller) throw new Error("Missing navigation preference owner");
  return controller;
}
export function NavigationPreferenceNotice({
  beforeReload,
}: {
  beforeReload?: () => void;
}) {
  useLocale();
  const { snapshot, operation, reload } = useNavigationPreference();
  if (!snapshot.problem || operation) return null;
  return (
    <div className="navigation-preference-notice">
      <p role="alert">{navigationProblemMessages[snapshot.problem]}</p>
      <button
        type="button"
        onClick={() => {
          // The notice unmounts while the read runs; keep focus in its owner.
          beforeReload?.();
          reload();
        }}
      >
        {copy("sidebar.navigation.reload")}
      </button>
    </div>
  );
}
