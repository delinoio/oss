// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  FailureCode,
  GrokDiagnosticPhase,
  GrokDiagnosticSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  GrokDiagnosticNotice,
  grokDiagnosticFromDocument,
  safeGrokDiagnostic,
} from "./grok-diagnostic";
import { i18n } from "./localization";
import {
  SubscriptionOnboarding,
  SubscriptionOnboardingStage as Stage,
} from "./subscription-onboarding";

afterEach(async () => {
  cleanup();
  await act(() => i18n.changeLanguage("en"));
});

const supported = "1.0.46";
// Raw native text, paths, URLs and tokens that must never be displayed.
const hostile =
  "raw-native-sentinel /Users/private/.grok/auth.json https://auth.x.ai/oauth2/device?user_code=ABCD-EFGH refresh_token person@example.com";
const sensitive =
  /sentinel|\/Users|https?:|user_code|refresh_token|@example\.com/;

function diagnostic(
  phase: GrokDiagnosticPhase,
  code = FailureCode.Unsupported,
  version = "1.0.52",
  correlationId = newRequestId(),
) {
  return create(GrokDiagnosticSchema, {
    detectedVersion: version,
    supportedVersion: supported,
    phase,
    code,
    message: hostile,
    guidance: hostile,
    correlationId,
  });
}

const phases = [
  [GrokDiagnosticPhase.DISCOVERY, "Executable discovery", "실행 파일 검색"],
  [GrokDiagnosticPhase.VERSION, "Version validation", "버전 검증"],
  [GrokDiagnosticPhase.PROFILE, "Profile validation", "프로필 검증"],
  [GrokDiagnosticPhase.RUNTIME, "Runtime preparation", "런타임 준비"],
  [GrokDiagnosticPhase.LOGIN, "Sign-in", "로그인"],
  [GrokDiagnosticPhase.REFRESH, "Credential refresh", "자격 증명 갱신"],
  [GrokDiagnosticPhase.LOGOUT, "Sign-out", "로그아웃"],
  [GrokDiagnosticPhase.MODELS, "Model discovery", "모델 검색"],
  [GrokDiagnosticPhase.EXECUTION, "Execution", "실행"],
  [GrokDiagnosticPhase.HISTORY, "History verification", "기록 확인"],
  [GrokDiagnosticPhase.CLEANUP, "Cleanup", "정리"],
] as const;

it.each(["en", "ko"] as const)(
  "renders every phase with version, code and guidance in %s without server text",
  async (locale) => {
    await act(() => i18n.changeLanguage(locale));
    for (const [phase, english, korean] of phases) {
      const value = diagnostic(phase);
      const { unmount } = render(<GrokDiagnosticNotice diagnostic={value} />);
      const alert = screen.getByRole("alert").textContent!;
      expect(alert).toContain(locale === "en" ? english : korean);
      expect(alert).toContain("1.0.52");
      expect(alert).toContain(supported);
      expect(alert).toContain(FailureCode.Unsupported);
      expect(alert).toContain(value.correlationId);
      expect(alert).not.toMatch(sensitive);
      unmount();
    }
  },
);

it("shows detected and supported versions for an unsupported release", async () => {
  const value = diagnostic(
    GrokDiagnosticPhase.VERSION,
    FailureCode.Unsupported,
    "1.0.43",
  );
  render(<GrokDiagnosticNotice diagnostic={value} />);
  const alert = screen.getByRole("alert").textContent!;
  expect(alert).toContain(
    "Grok Build 1.0.43 did not complete version validation. Grok Build requires valid SemVer at or above 1.0.46.",
  );
  expect(alert).toContain("Detected version:1.0.43");
  expect(alert).toContain("Supported version:1.0.46");
  expect(alert).toContain(
    "Install the supported Grok Build release shown below on the selected Runner Device",
  );
  await act(() => i18n.changeLanguage("ko"));
  const korean = screen.getByRole("alert").textContent!;
  expect(korean).toContain(
    "Grok Build 1.0.43이(가) 버전 검증 단계를 완료하지 못했습니다.",
  );
  expect(korean).toContain("1.0.46 이상의 올바른 SemVer");
  expect(korean).toContain("감지된 버전:1.0.43");
  expect(korean).toContain("지원되는 버전:1.0.46");
});

it("describes undetected, invalid and timed out failures from closed metadata", () => {
  const undetected = create(GrokDiagnosticSchema, {
    supportedVersion: supported,
    phase: GrokDiagnosticPhase.VERSION,
    code: FailureCode.Unsupported,
  });
  expect(safeGrokDiagnostic(undetected)!.message).toBe(
    "Grok Build not detected did not complete version validation. The native version metadata is invalid.",
  );
  const timeout = create(GrokDiagnosticSchema, {
    detectedVersion: "1.0.52",
    supportedVersion: supported,
    phase: GrokDiagnosticPhase.LOGIN,
    code: FailureCode.Unavailable,
    message:
      "Grok Build 1.0.52 did not complete sign-in. The native operation timed out.",
  });
  expect(safeGrokDiagnostic(timeout)!.message).toContain(
    "The native operation timed out.",
  );
  for (const bad of [
    diagnostic(GrokDiagnosticPhase.LOGIN, "bogus" as FailureCode),
    diagnostic(GrokDiagnosticPhase.UNSPECIFIED),
    diagnostic(
      GrokDiagnosticPhase.LOGIN,
      FailureCode.Unsupported,
      "/Users/private",
    ),
  ])
    expect(safeGrokDiagnostic(bad)).toBeUndefined();
  expect(safeGrokDiagnostic(undefined)).toBeUndefined();
  const rejected = render(
    <GrokDiagnosticNotice
      diagnostic={diagnostic(GrokDiagnosticPhase.UNSPECIFIED)}
    />,
  );
  expect(rejected.container.textContent).toBe("");
});

it("copies only the correlation id and reports a failed copy", async () => {
  const value = diagnostic(GrokDiagnosticPhase.PROFILE);
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  });
  render(<GrokDiagnosticNotice diagnostic={value} />);
  fireEvent.click(screen.getByRole("button", { name: "Copy reference" }));
  expect(writeText).toHaveBeenCalledExactlyOnceWith(value.correlationId);
  expect(await screen.findByText("Reference copied.")).toBeTruthy();
  writeText.mockRejectedValueOnce(new Error("denied"));
  fireEvent.click(screen.getByRole("button", { name: "Copy reference" }));
  expect(
    await screen.findByText("The reference could not be copied."),
  ).toBeTruthy();
  // A malformed reference is neither displayed nor copyable.
  cleanup();
  render(
    <GrokDiagnosticNotice
      diagnostic={diagnostic(
        GrokDiagnosticPhase.PROFILE,
        FailureCode.Unsupported,
        "1.0.52",
        "not-an-id",
      )}
    />,
  );
  expect(screen.queryByRole("button", { name: "Copy reference" })).toBeNull();
  expect(screen.getByRole("alert").textContent).toContain(
    "Reference:Not reported",
  );
});

it("reads the stored installation diagnostic and drops unknown phases", () => {
  const stored = grokDiagnosticFromDocument({
    detected_version: "1.0.52",
    supported_version: supported,
    phase: "profile",
    code: "unsupported",
    message: hostile,
    guidance: hostile,
    correlation_id: newRequestId(),
  });
  expect(stored?.phase).toBe(GrokDiagnosticPhase.PROFILE);
  expect(safeGrokDiagnostic(stored)!.message).toBe(
    "Grok Build 1.0.52 did not complete profile validation. The native protocol or version is incompatible.",
  );
  expect(grokDiagnosticFromDocument({ phase: "launch" })).toBeUndefined();
  expect(grokDiagnosticFromDocument(null)).toBeUndefined();
});

it("reuses the diagnostic in Grok onboarding while the service stays unsupported", async () => {
  const inspect = vi.fn();
  const value = diagnostic(
    GrokDiagnosticPhase.VERSION,
    FailureCode.Unsupported,
    "1.0.43",
  );
  render(
    <SubscriptionOnboarding
      serviceName="Grok"
      stage={Stage.Unsupported}
      active
      name="Grok"
      suggested={false}
      busy={false}
      canReopen={false}
      canCancel={false}
      changeName={vi.fn()}
      saveName={vi.fn()}
      reopen={vi.fn()}
      cancel={vi.fn()}
      leave={vi.fn()}
      grokDiagnostic={value}
      inspect={inspect}
    />,
  );
  expect(screen.getByRole("status").textContent).toBe(
    "Grok sign-in is not supported yet",
  );
  const alert = screen.getByRole("alert").textContent!;
  expect(alert).toContain(
    "Grok Build 1.0.43 did not complete version validation.",
  );
  expect(alert).toContain("Supported version:1.0.46");
  expect(alert).not.toMatch(sensitive);
  expect(
    screen.queryByRole("button", { name: "Open browser again" }),
  ).toBeNull();
  expect(inspect).not.toHaveBeenCalled();
});
