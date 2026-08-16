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
});
