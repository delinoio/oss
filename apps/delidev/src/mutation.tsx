import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { create, fromBinary, toBinary, type DescMessage, type DescMethodUnary, type MessageInitShape, type MessageShape } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { clientFailure, FailureCode } from "@delinoio/delidev-api-client";
import { useSettingsOpening } from "./settings-lifetime";
import { SettingsTaskStatus, useRetainSettingsTask } from "./settings-task-context";

interface Intent { input?: object; bytes?: number; acknowledge?: (result: unknown) => boolean; busy: boolean; uncertain: boolean; error?: unknown }
const empty: Intent = Object.freeze({ busy: false, uncertain: false });
class IntentRegistry {
  alive = true;
  revision = 0;
  entries = new Map<string, Intent>();
  listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  put(key: string, value: Intent) {
    this.entries.set(key, value);
    this.revision += 1;
    for (const listener of this.listeners) listener();
  }
  reserve(key: string, bytes: number) {
    const used = [...this.entries].reduce((sum, [id, value]) => sum + (id === key ? 0 : value.bytes ?? 0), 0);
    if (used + bytes > 8 << 20) throw new ConnectError("Resolve pending operations before retaining more input.", Code.ResourceExhausted);
    if (this.entries.has(key) || this.entries.size < 1000) return;
    for (const [id, value] of this.entries) if (!value.input && !value.busy) { this.entries.delete(id); return; }
    throw new ConnectError("Inspect pending operations before starting more work.", Code.ResourceExhausted);
  }
}

export interface RetainedMutationIntent { key: string; busy: boolean; uncertain: boolean }
export function useRetainedMutationIntents(prefix: string): RetainedMutationIntent[] {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const revision = useSyncExternalStore(registry.subscribe, () => registry.revision, () => registry.revision);
  return useMemo(() => [...registry.entries].flatMap(([key, intent]) => key.startsWith(prefix) && intent.input
    ? [{ key, busy: intent.busy, uncertain: intent.uncertain }]
    : []), [prefix, registry, revision]);
}
const Context = createContext<IntentRegistry | undefined>(undefined);
export function MutationIntents({ children }: { children: ReactNode }) {
  const [registry] = useState(() => new IntentRegistry());
  useEffect(() => { registry.alive = true; return () => { registry.alive = false; registry.entries.clear(); }; }, [registry]);
  return <Context.Provider value={registry}>{children}</Context.Provider>;
}

// Exact pending requests outlive session navigation. Only switching the whole
// connection discards that registry. Settings owns a nested category registry;
// leaving it discards only its intents, and late results cannot reach a replacement.
export function useRetainedMutation<I extends DescMessage, O extends DescMessage>(key: string, method: DescMethodUnary<I, O>, accepted?: (result: MessageShape<O>, request: MessageShape<I>) => void, acknowledge?: (result: MessageShape<O>, request: MessageShape<I>) => boolean) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const opening = useSettingsOpening();
  const mutation = useMutation(method, { retry: false, meta: opening?.mutationMeta });
  const [localError, setLocalError] = useState<{ key: string; error: unknown }>();
  const mounted = useRef(true);
  const state = useSyncExternalStore(registry.subscribe, () => registry.entries.get(key) ?? empty);
  useRetainSettingsTask(state.busy || state.uncertain, state.busy ? SettingsTaskStatus.Pending : SettingsTaskStatus.Uncertain);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const send = async (input?: MessageInitShape<I>) => {
    const current = registry.entries.get(key) ?? empty;
    if (current.busy || (current.input && input) || !registry.alive || opening?.disposed) return;
    if (!current.input && !input) return;
    let retained: MessageShape<I>;
    let bytes: number;
    try {
      // Clone the wire request before acceptance so later form edits cannot
      // change an uncertain request. Bound retained content across all screens.
      const wire = toBinary(method.input, create(method.input, (current.input ?? input) as MessageInitShape<I>));
      bytes = wire.byteLength;
      registry.reserve(key, bytes);
      retained = fromBinary(method.input, wire);
    } catch (error) { setLocalError({ key, error }); return; }
    // Recovery views must use the original request's validation authority even
    // when the submitting view has gone away or its current selection changed.
    const verify = current.acknowledge ?? (acknowledge ? (result: unknown) => acknowledge(result as MessageShape<O>, retained) : undefined);
    setLocalError(undefined);
    registry.put(key, { ...current, input: retained, bytes, acknowledge: verify, busy: true, error: undefined });
    let result: MessageShape<O>;
    try {
      result = await mutation.mutateAsync(retained);
      mutation.reset();
    } catch (error) {
      mutation.reset();
      if (!registry.alive || opening?.disposed) return;
      const failure = clientFailure(error);
      const uncertain = [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(failure.code);
      registry.put(key, { busy: false, uncertain, input: uncertain ? retained : undefined, bytes: uncertain ? bytes : undefined, acknowledge: uncertain ? verify : undefined, error });
      return;
    }
    if (!registry.alive || opening?.disposed) return;
    if (verify) {
      try {
        if (!verify(result)) throw new ConnectError("The accepted response could not be verified. Retry only the original request or inspect retained attempts.", Code.Internal);
      } catch (error) {
        registry.put(key, { busy: false, uncertain: true, input: retained, bytes, acknowledge: verify, error });
        return;
      }
    }
    registry.put(key, empty);
    // A presentation callback failure cannot turn an acknowledged RPC into an
    // uncertain mutation or authorize sending its side effect again.
    if (!mounted.current) return;
    try { accepted?.(result, retained); } catch (error) { setLocalError({ key, error }); }
  };
  return { send, retry: () => send(), ...state, error: localError?.key === key ? localError.error : state.error };
}
