import {
  RefreshClientKind,
  RefreshOrigin,
} from "@delinoio/devhud-deck-connect";

export const DECK_REFRESH_INTERVAL_MS = 5 * 60 * 1_000;
export const DECK_AUTOMATIC_VIEW_WINDOW_MS = 30 * 24 * 60 * 60 * 1_000;

export interface DeckRefreshCandidate {
  viewId: string;
  lastOpenedAt?: Date;
  notificationAttached: boolean;
  shortcutAttached: boolean;
  widgetAttached: boolean;
}

export interface DeckRefreshIdentity {
  viewId: string;
  requestId: string;
  origin: RefreshOrigin;
  clientKind: RefreshClientKind;
}

export interface DeckRefreshPreflight {
  priceUsdMicros: bigint;
  token: string;
}

export interface DeckRefreshTransport {
  isAmbiguousRefreshError(error: unknown): boolean;
  getPreflight(
    request: DeckRefreshIdentity,
    signal: AbortSignal,
  ): Promise<DeckRefreshPreflight>;
  refresh(
    request: DeckRefreshIdentity & { preflightToken: string },
    signal: AbortSignal,
  ): Promise<void>;
}

export interface DeckRefreshControllerOptions {
  clientKind:
    | RefreshClientKind.DESKTOP
    | RefreshClientKind.MOBILE
    | RefreshClientKind.OS_BACKGROUND_TASK;
  transport: DeckRefreshTransport;
  createRequestId: () => string;
  listCandidates: (signal: AbortSignal) => Promise<readonly DeckRefreshCandidate[]>;
  canPoll: () => boolean;
  automaticAttempts: DeckRefreshAttemptStore;
  manualAttempts: DeckRefreshAttemptStore;
  now?: () => Date;
  onError?: (error: unknown) => void;
}

export interface ManualRefreshWarning {
  kind: "billed-provider-call";
  priceUsdMicros: bigint;
  text: string;
}

export interface DeckRefreshAttempt {
  request: DeckRefreshIdentity;
  preflightToken: string;
}

export interface DeckRefreshAttemptStore {
  get(
    viewId: string,
  ): DeckRefreshAttempt | undefined | Promise<DeckRefreshAttempt | undefined>;
  set(
    viewId: string,
    attempt: DeckRefreshAttempt,
  ): void | Promise<void>;
  // Implementations must compare and delete atomically so delayed cleanup
  // cannot remove a newer logical attempt for the same view.
  deleteIfMatches(
    viewId: string,
    attempt: DeckRefreshAttempt,
  ): void | Promise<void>;
}

export function isAutomaticRefreshEligible(
  candidate: DeckRefreshCandidate,
  now: Date,
): boolean {
  if (
    candidate.notificationAttached ||
    candidate.shortcutAttached ||
    candidate.widgetAttached
  ) {
    return true;
  }
  const openedAt = candidate.lastOpenedAt?.getTime();
  return (
    openedAt !== undefined &&
    openedAt <= now.getTime() &&
    openedAt >= now.getTime() - DECK_AUTOMATIC_VIEW_WINDOW_MS
  );
}

export function manualRefreshWarning(priceUsdMicros: bigint): ManualRefreshWarning {
  const whole = priceUsdMicros / 1_000_000n;
  const fraction = (priceUsdMicros % 1_000_000n)
    .toString()
    .padStart(6, "0");
  return {
    kind: "billed-provider-call",
    priceUsdMicros,
    text:
      `Refresh now bypasses the cache and may make a billed GitHub provider ` +
      `request at $${whole}.${fraction} USD per request.`,
  };
}

export class DeckRefreshController {
  readonly #options: DeckRefreshControllerOptions;
  readonly #now: () => Date;
  #timer: ReturnType<typeof setTimeout> | undefined;
  #automaticAbort: AbortController | undefined;
  #activeRequests = new Set<AbortController>();
  #generation = 0;
  #running = false;

  constructor(options: DeckRefreshControllerOptions) {
    this.#options = options;
    this.#now = options.now ?? (() => new Date());
  }

  start(): void {
    if (this.#running) {
      return;
    }
    this.#running = true;
    this.#generation += 1;
    this.#schedule(0, this.#generation);
  }

  stop(): void {
    this.#running = false;
    this.#generation += 1;
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    this.#automaticAbort?.abort();
    this.#automaticAbort = undefined;
    for (const request of this.#activeRequests) {
      request.abort();
    }
    this.#activeRequests.clear();
  }

  async refreshManually(
    viewId: string,
    confirm: (warning: ManualRefreshWarning) => boolean | Promise<boolean>,
  ): Promise<boolean> {
    const controller = new AbortController();
    this.#activeRequests.add(controller);
    try {
      let pending = await this.#options.manualAttempts.get(viewId);
      if (controller.signal.aborted) {
        return false;
      }
      if (pending === undefined) {
        const request = this.#identity(viewId, RefreshOrigin.MANUAL);
        const preflight = await this.#options.transport.getPreflight(
          request,
          controller.signal,
        );
        if (controller.signal.aborted) {
          return false;
        }
        const confirmed = await confirm(
          manualRefreshWarning(preflight.priceUsdMicros),
        );
        if (controller.signal.aborted || !confirmed) {
          return false;
        }
        pending = { request, preflightToken: preflight.token };
        await this.#options.manualAttempts.set(viewId, pending);
        if (controller.signal.aborted) {
          return false;
        }
      }
      try {
        await this.#options.transport.refresh(
          {
            ...pending.request,
            preflightToken: pending.preflightToken,
          },
          controller.signal,
        );
      } catch (error) {
        if (!this.#options.transport.isAmbiguousRefreshError(error)) {
          await this.#options.manualAttempts.deleteIfMatches(viewId, pending);
        }
        throw error;
      }
      await this.#options.manualAttempts.deleteIfMatches(viewId, pending);
      return true;
    } finally {
      this.#activeRequests.delete(controller);
    }
  }

  #identity(viewId: string, origin: RefreshOrigin): DeckRefreshIdentity {
    return {
      viewId,
      requestId: this.#options.createRequestId(),
      origin,
      clientKind: this.#options.clientKind,
    };
  }

  #schedule(delay: number, generation: number): void {
    if (!this.#running || generation !== this.#generation) {
      return;
    }
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      void this.#poll(generation);
    }, delay);
  }

  async #poll(generation: number): Promise<void> {
    if (!this.#running || generation !== this.#generation) {
      return;
    }
    const cycleStartedAt = this.#now().getTime();
    const controller = new AbortController();
    this.#automaticAbort = controller;
    try {
      if (!this.#options.canPoll()) {
        return;
      }
      const candidates = await this.#options.listCandidates(controller.signal);
      for (const candidate of candidates) {
        if (
          controller.signal.aborted ||
          !this.#running ||
          generation !== this.#generation ||
          !this.#options.canPoll()
        ) {
          return;
        }
        let pending = await this.#options.automaticAttempts.get(candidate.viewId);
        if (
          controller.signal.aborted ||
          !this.#running ||
          generation !== this.#generation ||
          !this.#options.canPoll()
        ) {
          return;
        }
        if (pending === undefined) {
          if (!isAutomaticRefreshEligible(candidate, this.#now())) {
            continue;
          }
          const request = this.#identity(
            candidate.viewId,
            RefreshOrigin.AUTOMATIC,
          );
          let preflight: DeckRefreshPreflight;
          try {
            preflight = await this.#options.transport.getPreflight(
              request,
              controller.signal,
            );
          } catch (error) {
            if (controller.signal.aborted) {
              return;
            }
            this.#options.onError?.(error);
            continue;
          }
          if (
            controller.signal.aborted ||
            !this.#running ||
            generation !== this.#generation ||
            !this.#options.canPoll()
          ) {
            return;
          }
          pending = { request, preflightToken: preflight.token };
          await this.#options.automaticAttempts.set(candidate.viewId, pending);
        }
        if (
          controller.signal.aborted ||
          !this.#running ||
          generation !== this.#generation ||
          !this.#options.canPoll()
        ) {
          return;
        }
        try {
          await this.#options.transport.refresh(
            {
              ...pending.request,
              preflightToken: pending.preflightToken,
            },
            controller.signal,
          );
        } catch (error) {
          if (!this.#options.transport.isAmbiguousRefreshError(error)) {
            await this.#options.automaticAttempts.deleteIfMatches(
              candidate.viewId,
              pending,
            );
          }
          if (controller.signal.aborted) {
            return;
          }
          this.#options.onError?.(error);
          continue;
        }
        await this.#options.automaticAttempts.deleteIfMatches(
          candidate.viewId,
          pending,
        );
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        this.#options.onError?.(error);
      }
    } finally {
      if (this.#automaticAbort === controller) {
        this.#automaticAbort = undefined;
      }
      if (this.#running && generation === this.#generation) {
        const elapsed = Math.max(0, this.#now().getTime() - cycleStartedAt);
        this.#schedule(
          Math.max(0, DECK_REFRESH_INTERVAL_MS - elapsed),
          generation,
        );
      }
    }
  }
}

export class DeckWidgetRefreshController {
  readonly #transport: DeckRefreshTransport;
  readonly #createRequestId: () => string;
  readonly #attempts: DeckRefreshAttemptStore;

  constructor(
    transport: DeckRefreshTransport,
    createRequestId: () => string,
    attempts: DeckRefreshAttemptStore,
  ) {
    this.#transport = transport;
    this.#createRequestId = createRequestId;
    this.#attempts = attempts;
  }

  async refresh(viewId: string, signal: AbortSignal): Promise<void> {
    if (signal.aborted) {
      return;
    }
    let pending = await this.#attempts.get(viewId);
    if (signal.aborted) {
      return;
    }
    if (pending === undefined) {
      const request: DeckRefreshIdentity = {
        viewId,
        requestId: this.#createRequestId(),
        origin: RefreshOrigin.WIDGET,
        clientKind: RefreshClientKind.WIDGET,
      };
      const preflight = await this.#transport.getPreflight(request, signal);
      if (signal.aborted) {
        return;
      }
      pending = { request, preflightToken: preflight.token };
      await this.#attempts.set(viewId, pending);
      if (signal.aborted) {
        await this.#attempts.deleteIfMatches(viewId, pending);
        return;
      }
    }
    try {
      await this.#transport.refresh(
        {
          ...pending.request,
          preflightToken: pending.preflightToken,
        },
        signal,
      );
    } catch (error) {
      if (!this.#transport.isAmbiguousRefreshError(error)) {
        await this.#attempts.deleteIfMatches(viewId, pending);
      }
      throw error;
    }
    await this.#attempts.deleteIfMatches(viewId, pending);
  }
}
