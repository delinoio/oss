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

export enum BrowserChoiceState {
  SystemDefault = "system-default",
  Installed = "installed",
}
export type BrowserChoice =
  | { state: BrowserChoiceState.SystemDefault }
  | { state: BrowserChoiceState.Installed; identity: string };
export interface BrowserChoices {
  general: BrowserChoice;
  local: BrowserChoice;
}
export enum BrowserPreferenceProblem {
  Unavailable = "unavailable",
  ReadFailed = "read-failed",
  InvalidDocument = "invalid-document",
  UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed",
  OutcomeUnknown = "outcome-unknown",
  Changed = "changed",
}
export enum BrowserPreferenceOperation {
  Reading = "reading",
  Saving = "saving",
  Resetting = "resetting",
}
export enum BrowserSaveResult {
  Saved = "saved",
  NotInstalled = "not-installed",
  Failed = "failed",
}
export interface BrowserPreferenceSnapshot {
  revision: number;
  choices: BrowserChoices;
  problem: BrowserPreferenceProblem | null;
}
export interface BrowserInventoryEntry {
  identity: string;
  name: string;
}
export interface BrowserPreferenceBridge {
  /** False in web previews and mobile, which have no native browser opener. */
  native: boolean;
  read: () => Promise<unknown>;
  update: (choices: BrowserChoices, revision: number) => Promise<unknown>;
  reset: (revision: number) => Promise<unknown>;
  inventory: () => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}

export const defaultBrowserChoices: BrowserChoices = {
  general: { state: BrowserChoiceState.SystemDefault },
  local: { state: BrowserChoiceState.SystemDefault },
};
const defaults: BrowserPreferenceSnapshot = {
  revision: 0,
  choices: defaultBrowserChoices,
  problem: BrowserPreferenceProblem.Unavailable,
};
const nativeBridge: BrowserPreferenceBridge = {
  native: isTauri(),
  read: () =>
    isTauri() ? invoke("read_browser_preferences") : Promise.resolve(defaults),
  update: (choices, revision) =>
    invoke("update_browser_preferences", {
      choices,
      expectedRevision: revision,
    }),
  reset: (revision) =>
    invoke("reset_browser_preferences", { expectedRevision: revision }),
  inventory: () => invoke("read_browser_inventory"),
  subscribe: async (changed) =>
    isTauri()
      ? listen<unknown>("browser-preferences-changed", (event) =>
          changed(event.payload),
        )
      : () => {},
};

const identityPattern = /^[A-Za-z0-9._+()-](?:[A-Za-z0-9._+() -]{0,126}[A-Za-z0-9._+()-])?$/;
export function parseBrowserChoice(raw: unknown): BrowserChoice {
  if (!raw || typeof raw !== "object" || Array.isArray(raw))
    throw new Error("Invalid browser choice");
  const value = raw as Record<string, unknown>;
  if (
    value.state === BrowserChoiceState.SystemDefault &&
    Object.keys(value).join(",") === "state"
  )
    return { state: BrowserChoiceState.SystemDefault };
  if (
    value.state === BrowserChoiceState.Installed &&
    Object.keys(value).sort().join(",") === "identity,state" &&
    typeof value.identity === "string" &&
    identityPattern.test(value.identity)
  )
    return { state: BrowserChoiceState.Installed, identity: value.identity };
  throw new Error("Invalid browser choice");
}
export function parseBrowserSnapshot(raw: unknown): BrowserPreferenceSnapshot {
  if (!raw || typeof raw !== "object" || Array.isArray(raw))
    throw new Error("Invalid browser snapshot");
  const data = raw as Record<string, unknown>;
  const choices = data.choices as Record<string, unknown> | undefined;
  if (
    Object.keys(data).sort().join(",") !== "choices,problem,revision" ||
    !Number.isInteger(data.revision) ||
    Number(data.revision) < 0 ||
    Number(data.revision) > 0xffffffff ||
    (data.problem !== null &&
      !Object.values(BrowserPreferenceProblem).includes(
        data.problem as BrowserPreferenceProblem,
      )) ||
    !choices ||
    typeof choices !== "object" ||
    Object.keys(choices).sort().join(",") !== "general,local"
  )
    throw new Error("Invalid browser snapshot");
  return {
    revision: Number(data.revision),
    choices: {
      general: parseBrowserChoice(choices.general),
      local: parseBrowserChoice(choices.local),
    },
    problem: data.problem as BrowserPreferenceProblem | null,
  };
}
export function parseBrowserInventory(raw: unknown): BrowserInventoryEntry[] {
  if (
    !raw ||
    typeof raw !== "object" ||
    Object.keys(raw).join(",") !== "browsers" ||
    !Array.isArray((raw as { browsers: unknown }).browsers) ||
    (raw as { browsers: unknown[] }).browsers.length > 64
  )
    throw new Error("Invalid browser inventory");
  return (raw as { browsers: unknown[] }).browsers.map((entry) => {
    const value = entry as Record<string, unknown> | null;
    if (
      !value ||
      typeof value !== "object" ||
      Object.keys(value).sort().join(",") !== "identity,name" ||
      typeof value.identity !== "string" ||
      !identityPattern.test(value.identity) ||
      typeof value.name !== "string" ||
      value.name.length > 80
    )
      throw new Error("Invalid browser inventory entry");
    return { identity: value.identity, name: value.name };
  });
}

interface Controller {
  /** True only in the desktop app; web and mobile show native unavailable. */
  native: boolean;
  snapshot: BrowserPreferenceSnapshot;
  operation?: BrowserPreferenceOperation;
  save: (
    choices: BrowserChoices,
    revision: number,
  ) => Promise<BrowserSaveResult>;
  reset: (revision: number) => Promise<boolean>;
  reload: () => void;
  inventory: () => Promise<BrowserInventoryEntry[]>;
}
const Context = createContext<Controller>({
  native: false,
  snapshot: defaults,
  save: async () => BrowserSaveResult.Failed,
  reset: async () => false,
  reload: () => {},
  inventory: async () => [],
});
export const useBrowserPreferences = () => useContext(Context);

export function BrowserPreferenceProvider({
  children,
  bridge = nativeBridge,
}: {
  children: ReactNode;
  bridge?: BrowserPreferenceBridge;
}) {
  const [snapshot, setSnapshot] = useState(defaults),
    current = useRef(defaults);
  const [operation, setOperation] = useState<
    BrowserPreferenceOperation | undefined
  >(bridge.native ? BrowserPreferenceOperation.Reading : undefined);
  const running = useRef(false),
    generation = useRef(0),
    subscription = useRef<(() => void) | undefined>(undefined);
  const accept = (raw: unknown) => {
    const next = parseBrowserSnapshot(raw);
    if (next.revision < current.current.revision) return;
    if (
      next.revision === current.current.revision &&
      !next.problem &&
      JSON.stringify(next.choices) !== JSON.stringify(current.current.choices)
    )
      throw new Error("Contradictory browser revision");
    current.current = next;
    setSnapshot(next);
  };
  const failed = (problem: BrowserPreferenceProblem) => {
    current.current = { ...current.current, problem };
    setSnapshot(current.current);
  };
  const subscribe = async (owned: number) => {
    const remove = await bridge.subscribe((raw) => {
      if (
        generation.current !== owned ||
        (current.current.problem &&
          current.current.problem !== BrowserPreferenceProblem.Unavailable)
      )
        return;
      try {
        accept(raw);
      } catch {
        failed(BrowserPreferenceProblem.OutcomeUnknown);
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
    if (!bridge.native) return;
    const owned = ++generation.current;
    running.current = true;
    setOperation(BrowserPreferenceOperation.Reading);
    void (async () => {
      try {
        if (!(await subscribe(owned))) return;
        const result = await bridge.read();
        if (generation.current === owned) accept(result);
      } catch {
        if (generation.current === owned)
          failed(BrowserPreferenceProblem.ReadFailed);
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
  const run = async <T,>(
    kind: BrowserPreferenceOperation,
    action: () => Promise<unknown>,
    onError: (error: unknown) => T,
    onSuccess: () => T,
  ): Promise<T> => {
    if (running.current) return onError(undefined);
    const owned = generation.current;
    running.current = true;
    setOperation(kind);
    try {
      if (!subscription.current && !(await subscribe(owned)))
        return onError(undefined);
      const result = await action();
      if (generation.current !== owned) return onError(undefined);
      accept(result);
      return onSuccess();
    } catch (error) {
      if (generation.current === owned) {
        // A rejected unavailable-browser selection never reached storage and
        // leaves the verified snapshot untouched.
        if (error !== "browser-unavailable")
          failed(
            kind === BrowserPreferenceOperation.Reading
              ? BrowserPreferenceProblem.ReadFailed
              : BrowserPreferenceProblem.OutcomeUnknown,
          );
      }
      return onError(error);
    } finally {
      if (generation.current === owned) {
        running.current = false;
        setOperation(undefined);
      }
    }
  };
  return (
    <Context.Provider
      value={{
        native: bridge.native,
        snapshot,
        operation,
        save: (choices, revision) => {
          // The visible choice never changes before native persistence: the
          // snapshot is replaced only by the accepted native result.
          if (current.current.problem || revision !== current.current.revision)
            return Promise.resolve(BrowserSaveResult.Failed);
          return run(
            BrowserPreferenceOperation.Saving,
            () => bridge.update(structuredClone(choices), revision),
            (error) =>
              error === "browser-unavailable"
                ? BrowserSaveResult.NotInstalled
                : BrowserSaveResult.Failed,
            () =>
              current.current.problem
                ? BrowserSaveResult.Failed
                : BrowserSaveResult.Saved,
          );
        },
        reset: (revision) =>
          run(
            BrowserPreferenceOperation.Resetting,
            () => bridge.reset(revision),
            () => false,
            () => !current.current.problem,
          ),
        reload: () => {
          void run(
            BrowserPreferenceOperation.Reading,
            () => bridge.read(),
            () => false,
            () => true,
          );
        },
        inventory: async () => parseBrowserInventory(await bridge.inventory()),
      }}
    >
      {children}
    </Context.Provider>
  );
}
