import { ErrorCode, type Format, type Stage } from "./types.js";

export class ForgeError extends Error {
  readonly name = "ForgeError";
  constructor(
    readonly code: ErrorCode,
    message: string,
    readonly context: { stage?: Stage; format?: Format; revision?: number; location?: string; published?: boolean } = {},
  ) { super(message); }

  toJSON() { return { code: this.code, message: this.message, ...this.context }; }
}

export function cancelled(): ForgeError {
  return new ForgeError(ErrorCode.Cancelled, "The operation was cancelled.");
}

export function checkSignal(signal?: AbortSignal): void {
  if (signal?.aborted) throw cancelled();
}

/** Wait without retaining listeners after completion. Never exposes signal.reason. */
export function abortable<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  if (signal.aborted) { void promise.catch(() => {}); return Promise.reject(cancelled()); }
  return new Promise<T>((resolve, reject) => {
    const abort = () => { cleanup(); reject(cancelled()); };
    const cleanup = () => signal.removeEventListener("abort", abort);
    signal.addEventListener("abort", abort, { once: true });
    promise.then(value => { cleanup(); resolve(value); }, error => { cleanup(); reject(error); });
  });
}
