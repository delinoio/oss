import { useLocale } from "./localization";
import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { create, fromBinary, toBinary, type DescMessage, type DescMethodUnary, type MessageInitShape, type MessageShape } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { clientFailure, FailureCode } from "@delinoio/delidev-api-client";
import { useSettingsOpening } from "./settings-lifetime";

interface Intent { input?: object; bytes?: number; acknowledge?: (result: unknown) => boolean; busy: boolean; uncertain: boolean; error?: unknown }
const empty: Intent = Object.freeze({ busy: false, uncertain: false });
// Bind outside the hook so a retained verifier cannot keep the submitting
// hook's mutation, presentation callback or view state alive.
function bindAcknowledgement<I extends DescMessage, O extends DescMessage>(acknowledge: (result: MessageShape<O>, request: MessageShape<I>) => boolean, request: MessageShape<I>) {
  return (result: unknown) => acknowledge(result as MessageShape<O>, request);
}
class IntentRegistry {
  alive = true;
  revision = 0;
  entries = new Map<string, Intent>();
  listeners = new Set<() => void>();
  acceptedListeners = new Map<string, Set<() => void>>();
  acceptedObservers = new Set<(key: string, request: object, result: unknown) => void>();
  outcomeObservers = new Set<(key: string, request: object, phase: RetainedMutationPhase) => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  observeAccepted(key: string, listener: () => void) {
    const listeners = this.acceptedListeners.get(key) ?? new Set<() => void>();
    listeners.add(listener);
    this.acceptedListeners.set(key, listeners);
    return () => {
      listeners.delete(listener);
      if (!listeners.size) this.acceptedListeners.delete(key);
    };
  }
  notifyAcceptedResult(key: string, request: object, result: unknown) {
    for (const observer of this.acceptedObservers) {
      try { observer(key, request, result); } catch (error) { console.warn("delidev.mutation.accepted_observer_failed", { classification: clientFailure(error).code }); }
    }
  }
  notifyAccepted(key: string) {
    for (const listener of [...(this.acceptedListeners.get(key) ?? [])]) listener();
  }
  notifyOutcome(key: string, request: object, phase: RetainedMutationPhase) {
    for (const observer of this.outcomeObservers) {
      try { observer(key, request, phase); } catch (error) { console.warn("delidev.mutation.outcome_observer_failed", { classification: clientFailure(error).code }); }
    }
  }
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

export interface RetainedMutationIntent { key: string; busy: boolean; uncertain: boolean; input: object }
export function useRetainedMutationIntents(prefix: string): RetainedMutationIntent[] {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const revision = useSyncExternalStore(registry.subscribe, () => registry.revision, () => registry.revision);
  return useMemo(() => [...registry.entries].flatMap(([key, intent]) => key.startsWith(prefix) && intent.input
    ? [{ key, busy: intent.busy, uncertain: intent.uncertain, input: intent.input }]
    : []), [prefix, registry, revision]);
}
const Context = createContext<IntentRegistry | undefined>(undefined);
export function MutationIntents({ children }: { children: ReactNode }) {
  useLocale();
  const opening = useSettingsOpening();
  const [registry] = useState(() => new IntentRegistry());
  useEffect(() => {
    const dispose = () => { registry.alive = false; registry.entries.clear(); registry.acceptedListeners.clear(); registry.acceptedObservers.clear(); registry.outcomeObservers.clear(); };
    registry.alive = !opening?.disposed;
    opening?.controller.signal.addEventListener("abort", dispose, { once: true });
    return () => { opening?.controller.signal.removeEventListener("abort", dispose); dispose(); };
  }, [registry, opening]);
  return <Context.Provider value={registry}>{children}</Context.Provider>;
}

// Connection-owned attachment drafts observe the exact accepted request even
// after its submitting conversation leaves the mounted surface.
export function useRetainedMutationNotifications(accepted: (key: string, request: object, result: unknown) => void) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const callback = useRef(accepted); callback.current = accepted;
  useEffect(() => { const observer = (key: string, request: object, result: unknown) => callback.current(key, request, result); registry.acceptedObservers.add(observer); return () => { registry.acceptedObservers.delete(observer); }; }, [registry]);
}

export enum RetainedMutationPhase { Sending = "sending", Uncertain = "uncertain", Rejected = "rejected" }
/** Outcomes belong to the original connection even after the sender unmounts. */
export function useRetainedMutationOutcomes(observe: (key: string, request: object, phase: RetainedMutationPhase) => void) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const callback = useRef(observe); callback.current = observe;
  useEffect(() => {
    const observer = (key: string, request: object, phase: RetainedMutationPhase) => callback.current(key, request, phase);
    registry.outcomeObservers.add(observer);
    return () => { registry.outcomeObservers.delete(observer); };
  }, [registry]);
}

// Acceptance belongs to the retained request, not to the component that
// happened to initiate it. A Session-level recovery view can therefore
// acknowledge a deletion after its comment row has left the page.
export function useRetainedMutationAccepted(key: string, accepted: () => void) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const opening = useSettingsOpening();
  const callback = useRef(accepted);
  callback.current = accepted;
  const mounted = useRef(true);
  const [localError, setLocalError] = useState<{ key: string; error: unknown }>();
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => registry.observeAccepted(key, () => {
    if (!registry.alive || opening?.disposed || !mounted.current) return;
    try { callback.current(); } catch (error) { setLocalError({ key, error }); }
  }), [key, opening, registry]);
  return localError?.key === key ? localError.error : undefined;
}

// Exact pending requests outlive session navigation. Only switching the whole
// connection discards that registry. Settings categories, task dialogs, and
// external project creation own nested opening registries; departure discards
// their intents, and late results cannot reach a replacement.
export function useRetainedMutation<I extends DescMessage, O extends DescMessage>(key: string, method: DescMethodUnary<I, O>, accepted?: (result: MessageShape<O>, request: MessageShape<I>) => void, acknowledge?: (result: MessageShape<O>, request: MessageShape<I>) => boolean, retainOnError = false) {
  const registry = useContext(Context);
  if (!registry) throw new Error("A connection-scoped mutation registry is required.");
  const opening = useSettingsOpening();
  const mutation = useMutation(method, { retry: false, meta: opening?.mutationMeta });
  const [localError, setLocalError] = useState<{ key: string; error: unknown }>();
  const mounted = useRef(true);
  const state = useSyncExternalStore(registry.subscribe, () => registry.entries.get(key) ?? empty);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const send = async (input?: MessageInitShape<I>, retainedAcknowledgement?: (result: MessageShape<O>, request: MessageShape<I>) => boolean) => {
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
    } catch (error) { setLocalError({ key, error }); registry.notifyOutcome(key, current.input ?? input!, RetainedMutationPhase.Rejected); return; }
    // Recovery views must use the original request's validation authority even
    // when the submitting view has gone away or its current selection changed.
    const originalAcknowledgement = retainedAcknowledgement ?? acknowledge;
    const verify = current.acknowledge ?? (originalAcknowledgement ? bindAcknowledgement<I, O>(originalAcknowledgement, retained) : undefined);
    setLocalError(undefined);
    registry.put(key, { ...current, input: retained, bytes, acknowledge: verify, busy: true, error: undefined });
    registry.notifyOutcome(key, retained, RetainedMutationPhase.Sending);
    let result: MessageShape<O>;
    try {
      result = await mutation.mutateAsync(retained);
      mutation.reset();
    } catch (error) {
      mutation.reset();
      if (!registry.alive || opening?.disposed) return;
      const failure = clientFailure(error);
      // A rejected replay cannot establish whether an earlier uncertain request
      // was admitted. Keep opt-in original verification until a matching receipt.
      const uncertain = retainOnError || Boolean(current.uncertain && current.acknowledge) || [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(failure.code);
      registry.put(key, { busy: false, uncertain, input: uncertain ? retained : undefined, bytes: uncertain ? bytes : undefined, acknowledge: uncertain ? verify : undefined, error });
      registry.notifyOutcome(key, retained, uncertain ? RetainedMutationPhase.Uncertain : RetainedMutationPhase.Rejected);
      return;
    }
    if (!registry.alive || opening?.disposed) return;
    if (verify || acknowledge) {
      try {
        if (!(verify ? verify(result) : acknowledge!(result, retained))) throw new ConnectError("The accepted response could not be verified. Retry only the original request or inspect retained attempts.", Code.Internal);
      } catch (error) {
        registry.put(key, { busy: false, uncertain: true, input: retained, bytes, acknowledge: verify, error });
        registry.notifyOutcome(key, retained, RetainedMutationPhase.Uncertain);
        return;
      }
    }
    // Publish verified result ownership before clearing the original intent.
    registry.notifyAcceptedResult(key, retained, result);
    registry.put(key, empty);
    registry.notifyAccepted(key);
    // A presentation callback failure cannot turn an acknowledged RPC into an
    // uncertain mutation or authorize sending its side effect again.
    if (!mounted.current || !registry.alive || opening?.disposed) return;
    try { accepted?.(result, retained); } catch (error) { setLocalError({ key, error }); }
  };
  return { send, retry: () => send(), ...state, error: localError?.key === key ? localError.error : state.error };
}
