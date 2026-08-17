import type { ReactNode } from "react";
import type { Copy } from "./localization";

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

function StateFrame({ eyebrow, title, children, role = "status" }: { readonly eyebrow: string; readonly title: string; readonly children: ReactNode; readonly role?: "status" | "alert" }) {
  return <section className="state-panel" role={role} aria-labelledby={`state-${role}-title`}>
    <p className="eyebrow">{eyebrow}</p>
    <h2 id={`state-${role}-title`} tabIndex={-1}>{title}</h2>
    {children}
  </section>;
}

export function LoadingState({ copy }: StateProps) {
  return <StateFrame eyebrow={copy.loading} title={copy.loadingTitle}><p aria-live="polite">{copy.loadingSummary}</p><span className="progress" aria-hidden="true" /></StateFrame>;
}

export function EmptyState({ copy }: StateProps) {
  return <StateFrame eyebrow={copy.empty} title={copy.emptyTitle}><p>{copy.emptySummary}</p></StateFrame>;
}

export function OfflineState({ copy, lastSuccessfulAt }: StateProps & { readonly lastSuccessfulAt?: string }) {
  return <StateFrame eyebrow={copy.offline} title={copy.offlineTitle}><p>{copy.offlineSummary}</p>{lastSuccessfulAt && <p className="notice">{copy.lastSuccessfulRefresh}: <time dateTime={lastSuccessfulAt}>{lastSuccessfulAt}</time></p>}</StateFrame>;
}

export function BlockedState({ copy }: StateProps) {
  return <StateFrame eyebrow={copy.blocked} title={copy.blockedTitle} role="alert"><p>{copy.blockedSummary}</p><p className="notice">{copy.blockedLocalHint}</p></StateFrame>;
}

export function ErrorState({ copy, onRetry, retryable = true, correlationId }: StateProps & { readonly retryable?: boolean; readonly correlationId?: string }) {
  return <StateFrame eyebrow={copy.error} title={copy.errorTitle} role="alert"><p>{copy.errorSummary}</p>{correlationId && <p className="correlation">{copy.correlationId}: <code>{correlationId}</code></p>}{retryable && onRetry && <button className="primary" onClick={onRetry}>{copy.retry}</button>}</StateFrame>;
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
