import { useLocale } from "./localization";
import { createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Code, ConnectError, type Transport } from "@connectrpc/connect";
import { addStaticKeyToTransport, TransportProvider, useTransport } from "@connectrpc/connect-query";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { newRequestId } from "@delinoio/delidev-api-client";

const Context = createContext<SettingsOpening | undefined>(undefined);
export const useSettingsOpening = () => useContext(Context);

function canceled() { return new ConnectError("This Settings opening has closed.", Code.Canceled); }

// Nested guards can reject before an abort-ignoring original RPC settles. Keep
// that deepest completion separate from the visible cancellation outcome.
const unaryCompletions = new WeakMap<Promise<unknown>, Promise<void>>();
function unaryCompletion(pending: Promise<unknown>) {
  return unaryCompletions.get(pending) ?? pending.then(() => undefined, () => undefined);
}

// Read-pool owners may retain a permit until original work settles. This wrapper
// still invokes the same guarded transport and preserves its cancellation gates.
export function observeSettingsUnaryCompletion(transport: Transport, observe: (completion: Promise<void>) => void): Transport {
  return { ...transport, unary: (method, signal, timeout, headers, input, context) => {
    const pending = transport.unary(method, signal, timeout, headers, input, context);
    observe(unaryCompletion(pending));
    return pending;
  } };
}

// Reject client waits even when an accepted server/native operation ignores
// abort. Its authoritative effects are observed by fresh reads, never replayed.
function guarded<T>(pending: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = () => { signal.removeEventListener("abort", abort); reject(canceled()); };
    signal.addEventListener("abort", abort, { once: true });
    pending.then((value) => { signal.removeEventListener("abort", abort); if (signal.aborted) abort(); else resolve(value); }, (error) => { signal.removeEventListener("abort", abort); reject(error); });
    if (signal.aborted) abort();
  });
}

// One active category or external project creation opening owns this scope. A
// task dialog may create a nested scope; departure fences every nested task
// transport while independent connection controllers stay mounted.
export class SettingsOpening {
  readonly id = newRequestId();
  readonly categoryOwner: object;
  readonly controller = new AbortController();
  readonly queryKey = ["settings-opening", this.id] as const;
  readonly mutationMeta = { settingsOpening: this.id };
  readonly transport: Transport;

  constructor(upstream: () => Transport, categoryOwner?: object) {
    this.categoryOwner = categoryOwner ?? this;
    this.transport = addStaticKeyToTransport({
      unary: (method, signal, timeout, headers, input, context) => {
        if (this.disposed || signal?.aborted) return Promise.reject(canceled());
        const linked = this.link(signal);
        try {
          const pending = upstream().unary(method, linked.signal, timeout, headers, input, context);
          const visible = guarded(pending, linked.signal).finally(linked.release);
          unaryCompletions.set(visible, unaryCompletion(pending));
          return visible;
        } catch (error) { linked.release(); return Promise.reject(error); }
      },
      stream: async (method, signal, timeout, headers, input, context) => {
        const linked = this.link(signal);
        const check = () => { if (linked.signal.aborted) throw canceled(); };
        try {
          const request = (async function* () { for await (const message of input) { check(); yield message; } })();
          const response = await guarded(upstream().stream(method, linked.signal, timeout, headers, request, context), linked.signal);
          return { ...response, message: (async function* () {
            const iterator = response.message[Symbol.asyncIterator]();
            try {
              while (true) {
                check();
                const next = await guarded(iterator.next(), linked.signal);
                if (next.done) return;
                yield next.value;
              }
            } finally { linked.release(); void iterator.return?.().catch(() => undefined); }
          })() };
        } catch (error) { linked.release(); throw error; }
      },
    }, this.id);
  }

  get disposed() { return this.controller.signal.aborted; }
  private link(signal?: AbortSignal) {
    if (this.disposed || signal?.aborted) throw canceled();
    const controller = new AbortController();
    const abort = () => controller.abort();
    this.controller.signal.addEventListener("abort", abort, { once: true });
    signal?.addEventListener("abort", abort, { once: true });
    return { signal: controller.signal, release: () => {
      this.controller.signal.removeEventListener("abort", abort);
      signal?.removeEventListener("abort", abort);
    } };
  }

  async native<T>(operation: () => Promise<T>): Promise<T> {
    if (this.disposed) throw canceled();
    return guarded(operation(), this.controller.signal);
  }

  dispose(client: QueryClient) {
    if (this.disposed) return;
    this.controller.abort();
    const filters = { predicate: (query: { queryKey: readonly unknown[] }) => {
      const [kind, identity] = query.queryKey;
      return (kind === "settings-opening" && identity === this.id) || (kind === "connect-query" && typeof identity === "object" && identity !== null && "transport" in identity && identity.transport === this.id);
    } };
    void client.cancelQueries(filters);
    client.removeQueries(filters);
    for (const mutation of client.getMutationCache().getAll()) {
      if (mutation.meta?.settingsOpening === this.id) client.getMutationCache().remove(mutation);
    }
  }
}

export function SettingsLifetime({ children }: { children: (opening: SettingsOpening) => ReactNode }) {
  useLocale();
  const transport = useTransport();
  const parent = useContext(Context);
  const upstream = useRef(transport);
  upstream.current = transport;
  const client = useQueryClient();
  const [opening, setOpening] = useState<SettingsOpening>();
  useLayoutEffect(() => {
    // Strict Mode replays setup/cleanup. Each setup publishes a new generation
    // before mounting readers, so no child can reuse an aborted transport.
    const current = new SettingsOpening(() => upstream.current, parent?.categoryOwner);
    setOpening(current);
    return () => current.dispose(client);
  }, [client, parent?.categoryOwner]);
  return opening && !opening.disposed ? <Context.Provider key={opening.id} value={opening}><TransportProvider transport={opening.transport}>{children(opening)}</TransportProvider></Context.Provider> : null;
}
