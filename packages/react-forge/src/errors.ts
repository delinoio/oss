import { ErrorCode, type Format, type Stage } from "./types.js";

// tsx isolates task imports in a module namespace. A session returned by that
// namespace must retain its typed errors across the CLI's module boundary.
const errorBrand = Symbol.for("react-forge.error.v1");
export class ForgeError extends Error {
  readonly [errorBrand] = true;
  static [Symbol.hasInstance](value: unknown): value is ForgeError {
    return typeof value === "object" && value !== null && errorBrand in value
      && (value as ForgeError)[errorBrand] === true;
  }
  readonly name = "ForgeError";
  constructor(
    readonly code: ErrorCode,
    message: string,
    readonly context: { stage?: Stage; format?: Format; revision?: number; location?: string; published?: boolean } = {},
    cause?: unknown,
  ) { super(message, cause === undefined ? undefined : { cause }); }

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
