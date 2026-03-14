/**
 * React error boundary component for catching render errors in the component tree.
 */

import { Component, type ErrorInfo, type ReactNode } from "react";
import type { CSSProperties } from "react";

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    console.error("[ErrorBoundary] Uncaught error:", error, errorInfo.componentStack);
  }

  handleRetry = () => {
    this.setState({ hasError: false, error: null });
  };

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback;
      }
      return <ErrorFallback error={this.state.error} onRetry={this.handleRetry} />;
    }
    return this.props.children;
  }
}

function ErrorFallback({ error, onRetry }: { error: Error | null; onRetry: () => void }) {
  const containerStyle: CSSProperties = {
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    padding: "var(--space-8)",
    gap: "var(--space-4)",
  };

  const titleStyle: CSSProperties = {
    fontSize: "var(--font-size-lg)",
    fontWeight: 600,
    color: "var(--color-text-primary)",
  };

  const messageStyle: CSSProperties = {
    fontSize: "var(--font-size-sm)",
    color: "var(--color-text-secondary)",
    textAlign: "center",
    maxWidth: "400px",
  };

  const buttonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-4)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: "pointer",
  };

  return (
    <div style={containerStyle} data-testid="error-fallback">
      <div style={titleStyle}>Something went wrong</div>
      <div style={messageStyle}>
        {error?.message ?? "An unexpected error occurred."}
      </div>
      <button style={buttonStyle} onClick={onRetry} data-testid="error-retry-button">
        Try Again
      </button>
    </div>
  );
}
