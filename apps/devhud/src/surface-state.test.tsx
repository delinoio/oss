// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { messages } from "./localization";
import { ContentStateKind, ContentStateView } from "./surface-state";

afterEach(cleanup);

describe.each(["en", "ko"] as const)("localized content states in %s", (language) => {
  const copy = messages[language];

  it.each([
    [{ kind: ContentStateKind.Loading } as const, copy.loadingTitle, "status"],
    [{ kind: ContentStateKind.Empty } as const, copy.emptyTitle, "status"],
    [{ kind: ContentStateKind.Offline } as const, copy.offlineTitle, "status"],
    [{ kind: ContentStateKind.Blocked, scope: "official-api" } as const, copy.blockedTitle, "alert"],
    [{ kind: ContentStateKind.Error, retryable: false } as const, copy.errorTitle, "alert"],
  ])("renders %s with a named semantic region", (state, title, role) => {
    render(<ContentStateView state={state} copy={copy} />);
    expect(screen.getByRole("heading", { name: title })).toBeTruthy();
    expect(screen.getByRole(role, { name: title })).toBeTruthy();
  });

  it("provides a localized accessible retry action", () => {
    render(<ContentStateView state={{ kind: ContentStateKind.Error, retryable: true, correlationId: "safe-123" }} copy={copy} onRetry={() => {}} />);
    expect(screen.getByRole("button", { name: copy.retry })).toBeTruthy();
    expect(screen.getByText(/safe-123/u)).toBeTruthy();
  });

  it("shows a supplied last-successful refresh without inventing one", () => {
    const timestamp = "2026-08-16T12:34:56Z";
    const { container, rerender } = render(<ContentStateView state={{ kind: ContentStateKind.Offline, lastSuccessfulAt: timestamp }} copy={copy} />);
    expect(container.textContent).toContain(copy.lastSuccessfulRefresh);
    expect(screen.getByText(timestamp).getAttribute("datetime")).toBe(timestamp);

    rerender(<ContentStateView state={{ kind: ContentStateKind.Offline }} copy={copy} />);
    expect(container.textContent).not.toContain(copy.lastSuccessfulRefresh);
  });
});
