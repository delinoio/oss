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
    expect(
      screen.getByText("Original safe cause.").closest("details"),
    ).toBeNull();
    expect(
      screen.getByText("Original supported guidance.").closest("details"),
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

const quotaExplanationEn =
  "All configured account sources have confirmed quota exhaustion.";
const quotaGuidanceEn =
  "Wait for observed quota recovery or explicitly configure another source.";

it("shows localized quota copy followed by the exact original message and guidance", () => {
  const message = "  <b>Exact</b> safe message & more\nsecond line ";
  const guidance = "Use `this` supported guidance: <i>as is</i>.";
  const { container } = render(
    <SessionProblemNotice
      code="missing_input"
      cause="quota_exhausted"
      message={message}
      guidance={guidance}
      correlationId="corr-1"
    />,
  );
  const paragraphs = Array.from(container.querySelectorAll("p")).map(
    (node) => node.textContent,
  );
  expect(paragraphs).toEqual([
    quotaExplanationEn,
    quotaGuidanceEn,
    message,
    guidance,
  ]);
  expect(container.querySelector("b, i")).toBeNull();
  const details = screen.getByText("Technical details").closest("details");
  expect(details?.textContent).toContain("missing_input");
  expect(details?.textContent).toContain("quota_exhausted");
  expect(details?.textContent).toContain("corr-1");
  expect(details?.textContent).not.toContain("Exact");
});

it("renders no empty or duplicate quota paragraph when guidance is missing or empty", () => {
  for (const guidance of [undefined, ""]) {
    const { container, unmount } = render(
      <SessionProblemNotice
        code="missing_input"
        cause="quota_exhausted"
        message="Only the message."
        guidance={guidance}
      />,
    );
    const paragraphs = Array.from(container.querySelectorAll("p")).map(
      (node) => node.textContent,
    );
    expect(paragraphs).toEqual([
      quotaExplanationEn,
      quotaGuidanceEn,
      "Only the message.",
    ]);
    unmount();
  }
  const { container } = render(
    <SessionProblemNotice
      code="missing_input"
      cause="quota_exhausted"
      message=""
      guidance={quotaGuidanceEn}
    />,
  );
  expect(
    Array.from(container.querySelectorAll("p")).map((node) => node.textContent),
  ).toEqual([quotaExplanationEn, quotaGuidanceEn]);
});

it("keeps nonquota cards on their original message and guidance", () => {
  const { container } = render(
    <SessionProblemNotice
      code="unauthenticated"
      message="Sign in again."
      guidance="Open Settings."
      cause="some_cause"
      correlationId="corr-2"
    >
      <button>Original action</button>
    </SessionProblemNotice>,
  );
  expect(
    Array.from(container.querySelectorAll("p")).map((node) => node.textContent),
  ).toEqual(["Sign in again.", "Open Settings."]);
  expect(screen.getByText("Authentication required")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Original action" })).toBeTruthy();
  const details = screen.getByText("Technical details").closest("details");
  expect(details?.textContent).toContain("some_cause");
  expect(details?.textContent).toContain("corr-2");
});

it("changes only quota catalog copy on locale change and keeps the mounted card", async () => {
  const message = "Original safe cause.";
  const guidance = "Original supported guidance.";
  const { container } = render(
    <SessionProblemNotice
      code="missing_input"
      cause="quota_exhausted"
      message={message}
      guidance={guidance}
    >
      <button>Original action</button>
    </SessionProblemNotice>,
  );
  const alert = screen.getByRole("alert");
  const action = screen.getByRole("button", { name: "Original action" });
  action.focus();
  const before = Array.from(container.querySelectorAll("p")).map(
    (node) => node.textContent,
  );
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  try {
    expect(screen.getByRole("alert")).toBe(alert);
    expect(screen.getByRole("button", { name: "Original action" })).toBe(
      action,
    );
    expect(document.activeElement).toBe(action);
    const after = Array.from(container.querySelectorAll("p")).map(
      (node) => node.textContent,
    );
    expect(after).toHaveLength(4);
    expect(after[0]).not.toBe(before[0]);
    expect(after[1]).not.toBe(before[1]);
    expect(after[2]).toBe(message);
    expect(after[3]).toBe(guidance);
  } finally {
    await act(async () => {
      await i18n.changeLanguage("en");
    });
  }
});
