import type { ReactNode } from "react";

export function LoadingState({ label = "Loading" }: { label?: string }) {
  return (
    <div className="state-card" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <p>{label}…</p>
    </div>
  );
}

export function EmptyState({
  action,
  description,
  title,
}: {
  action?: ReactNode;
  description: string;
  title: string;
}) {
  return (
    <section className="state-card empty-state">
      <span className="empty-mark" aria-hidden="true">
        D
      </span>
      <h2>{title}</h2>
      <p>{description}</p>
      {action}
    </section>
  );
}

export function ErrorState({
  error,
  onRetry,
  title = "Something went wrong",
}: {
  error?: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  const detail =
    error instanceof Error
      ? error.message
      : "The request could not be completed. Please try again.";
  return (
    <section className="state-card error-state" role="alert">
      <span className="error-icon" aria-hidden="true">
        !
      </span>
      <h2>{title}</h2>
      <p>{detail}</p>
      {onRetry ? (
        <button className="button secondary" type="button" onClick={onRetry}>
          Try again
        </button>
      ) : null}
    </section>
  );
}

export function OfflineActionHint() {
  return (
    <p className="field-hint" role="status">
      Reconnect to use this action.
    </p>
  );
}
