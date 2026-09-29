import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { create, fromBinary, toBinary, type DescMessage, type DescMethodUnary, type MessageInitShape, type MessageShape } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { clientFailure, FailureCode } from "@delinoio/delidev-api-client";

interface Intent { input?: object; bytes?: number; busy: boolean; uncertain: boolean; error?: unknown }
const empty: Intent = Object.freeze({ busy: false, uncertain: false });
class IntentRegistry {
  alive = true;
  entries = new Map<string, Intent>();
  listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  put(key: string, value: Intent) {
    this.entries.set(key, value);
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
const Context = createContext<IntentRegistry | undefined>(undefined);
export function MutationIntents({ children }: { children: ReactNode }) {
  const [registry] = useState(() => new IntentRegistry());
  useEffect(() => { registry.alive = true; return () => { registry.alive = false; registry.entries.clear(); }; }, [registry]);
  return <Context.Provider value={registry}>{children}</Context.Provider>;
}

// Exact pending requests outlive session navigation. Only switching the whole
// connection discards this registry; late results cannot reach its replacement.
export function useRetainedMutation<I extends DescMessage, O extends DescMessage>(key: string, method: DescMethodUnary<I, O>, accepted?: (result: MessageShape<O>, request: MessageShape<I>) => void) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const mutation = useMutation(method, { retry: false });
  const [localError, setLocalError] = useState<{ key: string; error: unknown }>();
  const state = useSyncExternalStore(registry.subscribe, () => registry.entries.get(key) ?? empty);
  const send = async (input?: MessageInitShape<I>) => {
    const current = registry.entries.get(key) ?? empty;
    if (current.busy || (current.input && input) || !registry.alive) return;
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
    setLocalError(undefined);
    registry.put(key, { ...current, input: retained, bytes, busy: true, error: undefined });
    let result: MessageShape<O>;
    try {
      result = await mutation.mutateAsync(retained);
      mutation.reset();
    } catch (error) {
      mutation.reset();
      if (!registry.alive) return;
      const failure = clientFailure(error);
      const uncertain = [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(failure.code);
      registry.put(key, { busy: false, uncertain, input: uncertain ? retained : undefined, bytes: uncertain ? bytes : undefined, error });
      return;
    }
    if (!registry.alive) return;
    registry.put(key, empty);
    // A presentation callback failure cannot turn an acknowledged RPC into an
    // uncertain mutation or authorize sending its side effect again.
    try { accepted?.(result, retained); } catch (error) { setLocalError({ key, error }); }
  };
  return { send, retry: () => send(), ...state, error: localError?.key === key ? localError.error : state.error };
}
