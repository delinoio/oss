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
  GrokDiagnosticOwner,
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

// Mirrors TestGrokVersionReasonFollowsFullSemVerValidation in the Go domain
// package: every version passes the installation-version shape, and full SemVer
// validation alone decides which reason the server reconstructs.
it.each([
  "1.0.43",
  "1.0.0-rc.1",
  "1.0.0-0",
  "1.0.0-alpha-1",
  "1.0.0+build.01",
  "1.0.0-rc.1+20260101",
])("names the supported baseline for valid SemVer %s", (version) => {
  const message = safeGrokDiagnostic(
    diagnostic(GrokDiagnosticPhase.VERSION, FailureCode.Unsupported, version),
  )!.message;
  expect(message).toBe(
    `Grok Build ${version} did not complete version validation. Grok Build requires valid SemVer at or above ${supported}.`,
  );
});

it.each([
  "01.0.0",
  "1.00.0",
  "1.0.00",
  "1.0.0-01",
  "1.0.0-rc.01",
  "1.0.0-a..b",
  "1.0.0-.a",
  "1.0.0-a.",
  "1.0.0+a..b",
  "1.0.0+.a",
])("reports invalid metadata for the non-SemVer version %s", (version) => {
  const message = safeGrokDiagnostic(
    diagnostic(GrokDiagnosticPhase.VERSION, FailureCode.Unsupported, version),
  )!.message;
  expect(message).toBe(
    `Grok Build ${version} did not complete version validation. The native version metadata is invalid.`,
  );
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

it.each(["", "1.0", "01.0.46", "1.0.46-01", "1.0.46+a..b", "/Users/private"])(
  "never presents a diagnostic whose supported version is %j",
  (supportedVersion) => {
    const value = create(GrokDiagnosticSchema, {
      detectedVersion: "1.0.52",
      supportedVersion,
      phase: GrokDiagnosticPhase.PROFILE,
      code: FailureCode.Unsupported,
      correlationId: newRequestId(),
    });
    expect(safeGrokDiagnostic(value)).toBeUndefined();
    const view = render(<GrokDiagnosticNotice diagnostic={value} />);
    expect(view.container.textContent).toBe("");
  },
);

it("drops a stored installation diagnostic that omits the supported version", () => {
  const stored = grokDiagnosticFromDocument({
    detected_version: "1.0.52",
    phase: "profile",
    code: "unsupported",
  });
  expect(stored).toBeDefined();
  expect(safeGrokDiagnostic(stored)).toBeUndefined();
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

// Identical to grokReasonTable in the Go domain tests
// (TestGrokDiagnosticReasonFollowsTheSharedCodeTable): the desktop rebuilds the
// server's reason sentence from the closed code.
const reasonTable: ReadonlyArray<readonly [FailureCode, string]> = [
  [FailureCode.InvalidArgument, "The native configuration is invalid."],
  [FailureCode.NotFound, "The Grok Build executable was not found."],
  [FailureCode.Conflict, "The operation conflicts with an existing owner."],
  [FailureCode.Unauthenticated, "Native authentication was not accepted."],
  [FailureCode.PermissionDenied, "Native access was denied."],
  [FailureCode.Unavailable, "The native operation failed or timed out."],
  [FailureCode.ServerUnavailable, "The native service is unavailable."],
  [FailureCode.Unsupported, "The native protocol or version is incompatible."],
  [FailureCode.RecoveryRequired, "The native operation requires recovery."],
  [FailureCode.ResourceExhausted, "The native output exceeded its bound."],
  [FailureCode.Canceled, "The native operation was canceled."],
  [FailureCode.Internal, "The native operation failed."],
  [FailureCode.ConfirmationRequired, "The native operation did not complete."],
  [FailureCode.MissingInput, "The native operation did not complete."],
  [FailureCode.BudgetReached, "The native operation did not complete."],
  [FailureCode.CursorExpired, "The native operation did not complete."],
  [FailureCode.ProviderDisabled, "The native operation did not complete."],
];

it.each(reasonTable)("explains %s with the shared reason", (code, reason) => {
  const safe = safeGrokDiagnostic(diagnostic(GrokDiagnosticPhase.LOGIN, code));
  expect(safe?.message.endsWith(` ${reason}`)).toBe(true);
});

it("covers every failure code in the shared reason table", () => {
  expect(reasonTable.map(([code]) => code).sort()).toEqual(
    Object.values(FailureCode).sort(),
  );
});

// Guidance depends on the phase and the code. Only an access failure while
// handling the installation or its private runtime restores OS permissions;
// every other failure keeps the account and operation unchanged.
const accessGuidance = "Restore OS permission";
const installGuidance = "Install the supported Grok Build release";
const operationGuidance = "Keep the original account and operation. Check";
const installationPhaseSet = new Set([
  GrokDiagnosticPhase.DISCOVERY,
  GrokDiagnosticPhase.VERSION,
  GrokDiagnosticPhase.PROFILE,
  GrokDiagnosticPhase.RUNTIME,
  GrokDiagnosticPhase.CLEANUP,
]);

it.each(
  phases.flatMap(([phase]) =>
    Object.values(FailureCode).map((code) => [phase, code] as const),
  ),
)("chooses guidance for phase %s and code %s", (phase, code) => {
  const safe = safeGrokDiagnostic(diagnostic(phase, code));
  let want = operationGuidance;
  if (code === FailureCode.PermissionDenied && installationPhaseSet.has(phase))
    want = accessGuidance;
  else if (
    phase === GrokDiagnosticPhase.DISCOVERY ||
    phase === GrokDiagnosticPhase.VERSION ||
    phase === GrokDiagnosticPhase.PROFILE
  )
    want = installGuidance;
  expect(safe?.guidance.startsWith(want)).toBe(true);
});

it.each([
  [FailureCode.InvalidArgument, "noncanonical home or inherited environment"],
  [FailureCode.Unsupported, "unexpected profile entry or symlink"],
  [FailureCode.Unavailable, "launch or initialization failure"],
])(
  "does not ask to restore OS permissions for a runtime %s failure (%s)",
  async (code) => {
    const value = diagnostic(GrokDiagnosticPhase.RUNTIME, code);
    const { unmount } = render(<GrokDiagnosticNotice diagnostic={value} />);
    const english = screen.getByRole("alert").textContent!;
    expect(english).not.toContain(accessGuidance);
    expect(english).toContain(operationGuidance);
    await act(() => i18n.changeLanguage("ko"));
    expect(screen.getByRole("alert").textContent).not.toContain(
      "운영체제 권한",
    );
    unmount();
  },
);

it("restores OS permissions only for a runtime permission failure", async () => {
  const value = diagnostic(
    GrokDiagnosticPhase.RUNTIME,
    FailureCode.PermissionDenied,
  );
  render(<GrokDiagnosticNotice diagnostic={value} />);
  expect(screen.getByRole("alert").textContent).toContain(accessGuidance);
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("alert").textContent).toContain("운영체제 권한");
});

it("keeps server login guidance independent of Runner installation guidance", async () => {
  for (const language of ["en", "ko"]) {
    await act(() => i18n.changeLanguage(language));
    const view = render(
      <GrokDiagnosticNotice
        diagnostic={diagnostic(GrokDiagnosticPhase.LOGIN, FailureCode.NotFound)}
        owner={GrokDiagnosticOwner.Server}
      />,
    );
    expect(view.container.textContent).toContain(
      language === "en" ? "DeliDev server" : "DeliDev 서버",
    );
    expect(view.container.textContent).not.toMatch(
      /Runner Device|Worker diagnostics|실행 장치|워커 진단/,
    );
    expect(view.container.textContent).not.toMatch(sensitive);
    view.unmount();
  }
});
