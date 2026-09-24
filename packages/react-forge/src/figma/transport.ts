import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { setTimeout as delay } from "node:timers/promises";
import { ForgeError, checkSignal, abortable } from "../errors.js";
import { ErrorCode } from "../types.js";
import { CredentialReader, FIGMA_ENDPOINT } from "./credentials.js";

export enum CallSafety {
  Read = "read",
  Write = "write",
}
export interface CallStats {
  calls: number;
  retries: number;
  waitMs: number;
}
export interface FigmaConnection {
  readonly identity: string;
  readonly codeLimit: number;
  readonly stats: CallStats;
  connect(signal?: AbortSignal): Promise<void>;
  call(
    name: string,
    args: Record<string, unknown>,
    safety: CallSafety,
    signal?: AbortSignal,
  ): Promise<any>;
  close(): Promise<void>;
}
const remoteBrand = Symbol.for("react-forge.figma.remote-error.v1");
export class RemoteError extends ForgeError {
  readonly [remoteBrand] = true;
  static override [Symbol.hasInstance](value: unknown): value is RemoteError {
    return !!value && typeof value === "object" && remoteBrand in value;
  }
  constructor(
    code: ErrorCode,
    readonly safeToRetry: boolean,
    readonly retryAfterMs?: number,
  ) {
    super(code, `Figma MCP operation failed (${code}).`);
  }
}
export const retryAfter = (
  value: string | null,
  now = Date.now(),
): number | undefined => {
  if (!value) return undefined;
  const ms = /^\d+(\.\d+)?$/.test(value)
    ? Number(value) * 1000
    : Date.parse(value) - now;
  return Number.isFinite(ms) ? Math.max(0, ms) : undefined;
};
export interface SchedulerClock {
  now(): number;
  random(): number;
  sleep(ms: number, signal?: AbortSignal): Promise<void>;
}
const systemClock: SchedulerClock = {
  now: Date.now,
  random: Math.random,
  sleep: async (ms, signal) => {
    await delay(ms, undefined, { signal });
  },
};
export enum FigmaTier {
  Starter = "starter",
  Pro = "pro",
  Organization = "organization",
  Enterprise = "enterprise",
  Education = "education",
}
export function ratePolicy(tier: string, seat: string) {
  if (tier === FigmaTier.Starter)
    return { interval: 6000, quota: 20, windowMs: 31 * 86400000 };
  if (!["Full", "Dev"].includes(seat))
    return { interval: 6000, quota: 6, windowMs: 31 * 86400000 };
  if (tier === FigmaTier.Enterprise)
    return { interval: 3000, quota: 600, windowMs: 86400000 };
  if (tier === FigmaTier.Organization)
    return { interval: 4000, quota: 200, windowMs: 86400000 };
  return { interval: 6000, quota: 200, windowMs: 86400000 };
}
interface Budget {
  next: number;
  interval: number;
  queue: Promise<unknown>;
  used: number;
  start: number;
  quota: number;
  windowMs: number;
  exhaustedUntil: number;
}
const budgets = new Map<string, Budget>();
const fileQueues = new Map<string, Promise<unknown>>();
export async function withFigmaFile<T>(
  file: string,
  signal: AbortSignal,
  work: () => Promise<T>,
): Promise<T> {
  const previous = fileQueues.get(file) ?? Promise.resolve();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const current = previous.catch(() => {}).then(() => gate);
  fileQueues.set(file, current);
  try {
    await abortable(
      previous.catch(() => {}),
      signal,
    );
    checkSignal(signal);
    return await work();
  } finally {
    release();
    void current.then(() => {
      if (fileQueues.get(file) === current) fileQueues.delete(file);
    });
  }
}

export class OfficialFigmaConnection implements FigmaConnection {
  private client?: Client;
  private connecting?: Promise<void>;
  private readonly disposal = new AbortController();
  private tools = new Set<string>();
  private lastHttp?: { status: number; after?: number };
  codeLimit = 50_000;
  stats: CallStats = { calls: 0, retries: 0, waitMs: 0 };
  get identity() {
    return this.credentials.identity;
  }
  private queue: Promise<unknown> = Promise.resolve();
  constructor(
    private readonly credentials: CredentialReader,
    private readonly fetcher: typeof fetch = fetch,
    private readonly clock: SchedulerClock = systemClock,
    private readonly planKey?: string,
  ) {}
  connect(signal?: AbortSignal): Promise<void> {
    if (!this.connecting)
      this.connecting = this.initialize(signal).catch((error) => {
        this.connecting = undefined;
        throw error;
      });
    return this.connecting;
  }
  private async initialize(signal?: AbortSignal) {
    let token = await this.credentials.load(signal);
    const transport = new StreamableHTTPClientTransport(
      new URL(FIGMA_ENDPOINT),
      {
        // No OAuth provider: the SDK must not rotate another app's refresh token.
        reconnectionOptions: {
          maxRetries: 0,
          initialReconnectionDelay: 1000,
          maxReconnectionDelay: 1000,
          reconnectionDelayGrowFactor: 1,
        },
        fetch: async (input, init) => {
          if (String(input) !== FIGMA_ENDPOINT)
            throw new RemoteError(ErrorCode.Authentication, true);
          const send = () => {
            const headers = new Headers(init?.headers);
            headers.set("Authorization", `Bearer ${token}`);
            return this.fetcher(input, {
              ...init,
              headers,
              redirect: "error",
              signal: AbortSignal.any([
                this.disposal.signal,
                ...(init?.signal ? [init.signal] : []),
              ]),
            });
          };
          let response = await send();
          if (response.status === 401) {
            await response.body?.cancel();
            token = await this.credentials.load(signal);
            response = await send();
          }
          this.lastHttp = response.ok
            ? undefined
            : {
                status: response.status,
                after: retryAfter(
                  response.headers.get("Retry-After"),
                  this.clock.now(),
                ),
              };
          return response;
        },
      },
    );
    const client = new Client({ name: "react-forge", version: "0.0.0" });
    try {
      await client.connect(transport, { signal });
      let cursor: string | undefined;
      let pages = 0;
      do {
        const response = await client.listTools(cursor ? { cursor } : {}, {
          signal,
        });
        for (const tool of response.tools) {
          this.tools.add(tool.name);
          if (tool.name === "use_figma") {
            const code = tool.inputSchema.properties?.code as
              | { maxLength?: number }
              | undefined;
            if (code?.maxLength && Number.isSafeInteger(code.maxLength))
              this.codeLimit = Math.min(this.codeLimit, code.maxLength);
          }
        }
        cursor = response.nextCursor;
        if (++pages > 20) throw new RemoteError(ErrorCode.ResourceLimit, true);
      } while (cursor);
      if (
        !["whoami", "use_figma", "create_new_file", "upload_assets"].every(
          (name) => this.tools.has(name),
        )
      )
        throw new ForgeError(
          ErrorCode.UnsupportedEdit,
          "The connected official Figma MCP server does not expose the required write tools.",
        );
      this.client = client;
      if (!budgets.has(this.identity))
        budgets.set(this.identity, {
          next: 0,
          interval: 6000,
          queue: Promise.resolve(),
          used: 0,
          start: this.clock.now(),
          quota: Infinity,
          windowMs: 86400000,
          exhaustedUntil: 0,
        });
    } catch (error) {
      await client.close().catch(() => {});
      if (error instanceof ForgeError) throw error;
      throw new ForgeError(
        ErrorCode.Authentication,
        "Unable to connect to official Figma MCP. Reconnect Figma in the selected app.",
      );
    }
  }
  private async admit(signal?: AbortSignal) {
    const budget = budgets.get(this.identity)!;
    const work = budget.queue
      .catch(() => {})
      .then(async () => {
        checkSignal(signal);
        if (this.clock.now() < budget.exhaustedUntil)
          throw new RemoteError(ErrorCode.QuotaExceeded, true);
        if (this.clock.now() - budget.start >= budget.windowMs) {
          budget.start = this.clock.now();
          budget.used = 0;
        }
        if (budget.used >= budget.quota)
          throw new RemoteError(ErrorCode.QuotaExceeded, true);
        const wait = Math.max(0, budget.next - this.clock.now());
        if (wait) {
          this.stats.waitMs += wait;
          await this.clock.sleep(wait, signal);
        }
        budget.next = this.clock.now() + budget.interval;
        budget.used++;
      });
    budget.queue = work;
    await work;
  }
  call(
    name: string,
    args: Record<string, unknown>,
    safety: CallSafety,
    signal?: AbortSignal,
  ): Promise<any> {
    const run = this.queue
      .catch(() => {})
      .then(() => this.perform(name, args, safety, signal));
    this.queue = run;
    return run;
  }
  private async perform(
    name: string,
    args: Record<string, unknown>,
    safety: CallSafety,
    signal?: AbortSignal,
  ): Promise<any> {
    await this.connect(signal);
    if (!this.tools.has(name))
      throw new RemoteError(ErrorCode.UnsupportedEdit, true);
    if (
      name === "use_figma" &&
      (typeof args.code !== "string" || args.code.length > this.codeLimit)
    )
      throw new RemoteError(ErrorCode.ResourceLimit, true);
    for (let attempt = 0; ; attempt++) {
      checkSignal(signal);
      if (!["whoami", "create_new_file", "add_code_connect_map"].includes(name))
        await this.admit(signal);
      this.stats.calls++;
      let problem: RemoteError;
      try {
        this.lastHttp = undefined;
        const result = await this.client!.callTool(
          { name, arguments: args },
          undefined,
          { signal },
        );
        if (result.isError) {
          const raw = JSON.stringify(result);
          const safe =
            /"safeToRetryWithoutCanvasRead"\s*:\s*true/.test(raw) ||
            /safeToRetryWithoutCanvasRead[\\"\s:]+true/.test(raw);
          const quota = /monthly|daily|per month|per day|quota.?exhaust/i.test(
            raw,
          );
          const rate = /rate.?limit|too many requests|429/i.test(raw);
          problem = new RemoteError(
            quota
              ? ErrorCode.QuotaExceeded
              : rate
                ? ErrorCode.RateLimited
                : ErrorCode.Remote,
            safe,
          );
        } else {
          let value: any = result.structuredContent;
          if (value === undefined) {
            const texts = (result.content as any[])
              .filter((part) => part.type === "text")
              .map((part) => part.text);
            for (const text of texts) {
              try {
                value = JSON.parse(text);
                break;
              } catch {
                /* Some tools include an explanatory text block. */
              }
            }
          }
          if (value === undefined)
            throw new RemoteError(ErrorCode.UnknownOutcome, false);
          // use_figma returns the script's return value inside a result wrapper.
          if (name === "use_figma" && value.result !== undefined)
            value = value.result;
          if (name === "whoami" && this.planKey) {
            const plan = value.plans?.find((p: any) => p.key === this.planKey);
            if (plan)
              Object.assign(
                budgets.get(this.identity)!,
                ratePolicy(plan.tier, plan.seat),
              );
          }
          return value;
        }
      } catch (error) {
        checkSignal(signal);
        if (error instanceof RemoteError) problem = error;
        else {
          const http = this.lastHttp;
          const code =
            http?.status === 401
              ? ErrorCode.Authentication
              : http?.status === 403
                ? ErrorCode.PermissionDenied
                : http?.status === 429
                  ? ErrorCode.RateLimited
                  : http?.status === 404
                    ? ErrorCode.InvalidTarget
                    : http?.status === 409
                      ? ErrorCode.Conflict
                      : http?.status === 413
                        ? ErrorCode.ResourceLimit
                        : http && http.status >= 400 && http.status < 500 && http.status !== 408
                          ? ErrorCode.MalformedInput
                          : ErrorCode.Remote;
          problem = new RemoteError(
            code,
            !!http && [401, 403, 429].includes(http.status),
            http?.after,
          );
        }
      }
      if (
        problem.code === ErrorCode.RateLimited &&
        problem.retryAfterMs !== undefined
      ) {
        const budget = budgets.get(this.identity)!;
        budget.next = Math.max(
          budget.next,
          this.clock.now() + problem.retryAfterMs,
        );
        if (problem.retryAfterMs >= 60 * 60 * 1000) {
          budget.exhaustedUntil = budget.next;
          throw new RemoteError(
            ErrorCode.QuotaExceeded,
            problem.safeToRetry,
            problem.retryAfterMs,
          );
        }
      }
      if (problem.code === ErrorCode.QuotaExceeded)
        budgets.get(this.identity)!.exhaustedUntil =
          this.clock.now() + 86400000;
      const transient = [ErrorCode.RateLimited, ErrorCode.Remote].includes(
        problem.code,
      );
      if (
        !transient ||
        attempt >= 3 ||
        (safety === CallSafety.Write && !problem.safeToRetry)
      )
        throw problem;
      const wait =
        problem.retryAfterMs ??
        Math.min(30_000, 1000 * 2 ** attempt) *
          (0.75 + this.clock.random() * 0.5);
      this.stats.retries++;
      this.stats.waitMs += wait;
      await this.clock.sleep(wait, signal);
    }
  }
  async close() {
    this.disposal.abort();
    await this.client?.close().catch(() => {});
    this.credentials.clear();
  }
}
