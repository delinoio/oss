/**
 * Skeleton loading components for DexDex pages.
 */

import type { CSSProperties } from "react";

const pulseKeyframes = `
@keyframes skeleton-pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}
`;

// Inject keyframes once
if (typeof document !== "undefined") {
  const style = document.getElementById("skeleton-keyframes");
  if (!style) {
    const el = document.createElement("style");
    el.id = "skeleton-keyframes";
    el.textContent = pulseKeyframes;
    document.head.appendChild(el);
  }
}

function SkeletonLine({ width = "100%", height = "14px" }: { width?: string; height?: string }) {
  const style: CSSProperties = {
    width,
    height,
    borderRadius: "var(--radius-sm)",
    backgroundColor: "var(--color-border)",
    animation: "skeleton-pulse 1.5s ease-in-out infinite",
  };
  return <div style={style} />;
}

function SkeletonRow() {
  const rowStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "var(--space-3)",
    padding: "var(--space-3) var(--space-6)",
    borderBottom: "1px solid var(--color-border-subtle)",
  };

  return (
    <div style={rowStyle}>
      <SkeletonLine width="60px" height="20px" />
      <SkeletonLine width="60%" />
      <div style={{ flex: 1 }} />
      <SkeletonLine width="80px" />
    </div>
  );
}

export function TaskListSkeleton() {
  return (
    <div data-testid="task-list-skeleton">
      {Array.from({ length: 6 }, (_, i) => (
        <SkeletonRow key={i} />
      ))}
    </div>
  );
}

export function InboxSkeleton() {
  return (
    <div data-testid="inbox-skeleton">
      {Array.from({ length: 4 }, (_, i) => (
        <div
          key={i}
          style={{
            display: "flex",
            alignItems: "flex-start",
            gap: "var(--space-3)",
            padding: "var(--space-3) var(--space-6)",
            borderBottom: "1px solid var(--color-border-subtle)",
          }}
        >
          <SkeletonLine width="24px" height="24px" />
          <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
            <SkeletonLine width="50%" height="16px" />
            <SkeletonLine width="80%" />
          </div>
        </div>
      ))}
    </div>
  );
}

export function PrListSkeleton() {
  return (
    <div data-testid="pr-list-skeleton">
      {Array.from({ length: 4 }, (_, i) => (
        <div
          key={i}
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-3)",
            padding: "var(--space-3) var(--space-4)",
            borderBottom: "1px solid var(--color-border)",
          }}
        >
          <SkeletonLine width="80px" height="22px" />
          <SkeletonLine width="40%" />
        </div>
      ))}
    </div>
  );
}
