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
  now?: () => Date;
  onError?: (error: unknown) => void;
}

export interface ManualRefreshWarning {
  kind: "billed-provider-call";
  priceUsdMicros: bigint;
  text: string;
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
    this.#schedule(0);
  }

  stop(): void {
    this.#running = false;
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
      const request = this.#identity(viewId, RefreshOrigin.MANUAL);
      const preflight = await this.#options.transport.getPreflight(
        request,
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        !(await confirm(manualRefreshWarning(preflight.priceUsdMicros)))
      ) {
        return false;
      }
      await this.#options.transport.refresh(
        { ...request, preflightToken: preflight.token },
        controller.signal,
      );
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

  #schedule(delay: number): void {
    if (!this.#running) {
      return;
    }
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      void this.#poll();
    }, delay);
  }

  async #poll(): Promise<void> {
    if (!this.#running) {
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
          !this.#options.canPoll()
        ) {
          return;
        }
        if (!isAutomaticRefreshEligible(candidate, this.#now())) {
          continue;
        }
        const request = this.#identity(candidate.viewId, RefreshOrigin.AUTOMATIC);
        const preflight = await this.#options.transport.getPreflight(
          request,
          controller.signal,
        );
        if (
          controller.signal.aborted ||
          !this.#running ||
          !this.#options.canPoll()
        ) {
          return;
        }
        await this.#options.transport.refresh(
          { ...request, preflightToken: preflight.token },
          controller.signal,
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
      if (this.#running) {
        const elapsed = Math.max(0, this.#now().getTime() - cycleStartedAt);
        this.#schedule(Math.max(0, DECK_REFRESH_INTERVAL_MS - elapsed));
      }
    }
  }
}

export async function refreshFromWidget(
  viewId: string,
  transport: DeckRefreshTransport,
  createRequestId: () => string,
  signal: AbortSignal,
): Promise<void> {
  const request: DeckRefreshIdentity = {
    viewId,
    requestId: createRequestId(),
    origin: RefreshOrigin.WIDGET,
    clientKind: RefreshClientKind.WIDGET,
  };
  const preflight = await transport.getPreflight(request, signal);
  if (signal.aborted) {
    return;
  }
  await transport.refresh(
    { ...request, preflightToken: preflight.token },
    signal,
  );
}
