import type { Copy } from "./localization";
import { Button, StatePanel } from "./ui-foundation";

export const ContentStateKind = {
  Ready: "ready",
  Loading: "loading",
  Empty: "empty",
  Offline: "offline",
  Blocked: "blocked",
  Error: "error",
} as const;
export type ContentStateKind = (typeof ContentStateKind)[keyof typeof ContentStateKind];

export type ContentState =
  | { readonly kind: "ready" }
  | { readonly kind: "loading" }
  | { readonly kind: "empty" }
  | { readonly kind: "offline"; readonly lastSuccessfulAt?: string }
  | { readonly kind: "blocked"; readonly scope: "official-api" }
  | { readonly kind: "error"; readonly retryable: boolean; readonly correlationId?: string };

interface StateProps {
  readonly copy: Copy;
  readonly onRetry?: () => void;
}

export function LoadingState({ copy }: StateProps) {
  return <StatePanel eyebrow={copy.loading} title={copy.loadingTitle} summary={<span aria-live="polite">{copy.loadingSummary}</span>} progress />;
}

export function EmptyState({ copy }: StateProps) {
  return <StatePanel eyebrow={copy.empty} title={copy.emptyTitle} summary={copy.emptySummary} tone="neutral" />;
}

export function OfflineState({ copy, lastSuccessfulAt }: StateProps & { readonly lastSuccessfulAt?: string }) {
  return <StatePanel eyebrow={copy.offline} title={copy.offlineTitle} summary={copy.offlineSummary} tone="warning" details={lastSuccessfulAt && <p className="notice">{copy.lastSuccessfulRefresh}: <time dateTime={lastSuccessfulAt}>{lastSuccessfulAt}</time></p>} />;
}

export function BlockedState({ copy }: StateProps) {
  return <StatePanel eyebrow={copy.blocked} title={copy.blockedTitle} summary={copy.blockedSummary} role="alert" tone="danger" details={<p className="notice">{copy.blockedLocalHint}</p>} />;
}

export function ErrorState({ copy, onRetry, retryable = true, correlationId }: StateProps & { readonly retryable?: boolean; readonly correlationId?: string }) {
  return <StatePanel eyebrow={copy.error} title={copy.errorTitle} summary={copy.errorSummary} role="alert" tone="danger" details={correlationId && <p className="correlation">{copy.correlationId}: <code>{correlationId}</code></p>} actions={retryable && onRetry && <Button variant="primary" onClick={onRetry}>{copy.retry}</Button>} />;
}

export function ContentStateView({ state, copy, onRetry }: { readonly state: ContentState; readonly copy: Copy; readonly onRetry?: () => void }) {
  switch (state.kind) {
    case ContentStateKind.Loading: return <LoadingState copy={copy} />;
    case ContentStateKind.Empty: return <EmptyState copy={copy} />;
    case ContentStateKind.Offline: return <OfflineState copy={copy} lastSuccessfulAt={state.lastSuccessfulAt} />;
    case ContentStateKind.Blocked: return <BlockedState copy={copy} />;
    case ContentStateKind.Error: return <ErrorState copy={copy} retryable={state.retryable} correlationId={state.correlationId} onRetry={onRetry} />;
    case ContentStateKind.Ready: return null;
  }
}
