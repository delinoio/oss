// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  SessionNotice,
  SessionNoticeSeverity,
  SessionProblemNotice,
} from "./session-presentation";
import { i18n } from "./localization";
it.each([
  ["missing_input", "quota_exhausted", "Usage limit reached"],
  ["unauthenticated", undefined, "Authentication required"],
  ["server_unavailable", undefined, "Connection unavailable"],
  ["budget_reached", undefined, "Session budget reached"],
  ["recovery_required", undefined, "Recovery required"],
  ["missing_input", undefined, "This session needs attention"],
  ["missing_input", "unknown", "This session needs attention"],
  ["resource_exhausted", "quota_exhausted", "This session needs attention"],
  [
    "missing_input",
    { cause: "quota_exhausted" },
    "This session needs attention",
  ],
])("projects %s and %s without parsing cause text", (code, cause, title) => {
  render(
    <SessionProblemNotice
      code={code}
      cause={cause}
      message="Original safe cause."
      guidance="Original supported guidance."
    />,
  );
  expect(screen.getByText(title)).toBeTruthy();
  expect(screen.getByText("Technical details").closest("details")?.open).toBe(
    false,
  );
  if (cause !== "quota_exhausted" || code !== "missing_input") {
    expect(
      screen.getByText("Original safe cause.").closest("details"),
    ).toBeNull();
    expect(
      screen.getByText("Original supported guidance.").closest("details"),
    ).toBeNull();
  } else {
    expect(
      screen
        .getByText(
          "All configured account sources have confirmed quota exhaustion.",
        )
        .closest("details"),
    ).toBeNull();
    expect(
      screen
        .getByText(
          "Wait for observed quota recovery or explicitly configure another source.",
        )
        .closest("details"),
    ).toBeNull();
  }
});
it("retains separate cards, original actions and localized quota without automatic actions", async () => {
  const details = vi.fn(),
    retry = vi.fn();
  const mounted = render(
    <>
      <SessionProblemNotice
        code="missing_input"
        cause="quota_exhausted"
        message="Original quota cause."
        details={details}
      />
      <SessionNotice
        title="Execution could not start"
        severity={SessionNoticeSeverity.Error}
      >
        <p>Install on the original Runner.</p>
        <button onClick={retry}>Original request</button>
      </SessionNotice>
    </>,
  );
  expect(screen.getAllByRole("alert")).toHaveLength(2);
  expect(details).not.toHaveBeenCalled();
  expect(retry).not.toHaveBeenCalled();
  const opener = screen.getByRole("button", { name: "Show details" });
  fireEvent.click(opener);
  expect(details).toHaveBeenCalledWith(opener);
  fireEvent.click(screen.getByRole("button", { name: "Original request" }));
  expect(retry).toHaveBeenCalledTimes(1);
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(screen.getByText("사용량 한도에 도달했습니다")).toBeTruthy();
  expect(details).toHaveBeenCalledTimes(1);
  expect(retry).toHaveBeenCalledTimes(1);
  mounted.unmount();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});
