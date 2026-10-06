// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { Failure } from "./ui";
import { FailureCode } from "@delinoio/delidev-api-client";
import { LocalizedText, copy, formatDecimal, formatNumber, formatTimestamp, i18n, ownedMessage, useProductMessage, type MessageKey } from "./localization";
import { statusLabel } from "./product-status";

test("rich catalog slots retain zero, inert original text and focused child identity when reordered", async () => {
  const key = "fixture.richSlots" as MessageKey;
  i18n.addResource("en", "translation", key, "Before <s0/> · <s1/> · <s2/>");
  i18n.addResource("ko", "translation", key, "<s2/> · <s0/> · <s1/> 이후");
  function View() { return <LocalizedText id={key} components={{ s0: <>0</>, s1: <>{"<img src=x onerror=alert(1)>"}</>, s2: <input aria-label="Original input" defaultValue="Unsubmitted draft" /> }} />; }
  const { container } = render(<View />);
  const input = screen.getByRole("textbox"); input.focus(); fireEvent.change(input, { target: { value: "Pending draft" } });
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("textbox")).toBe(input);
  expect(document.activeElement).toBe(input);
  expect(input).toHaveProperty("value", "Pending draft");
  expect(container.textContent).toContain("0");
  expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
  expect(container.querySelector("img")).toBeNull();
});

test("notice translation does not repeat its action or discard a draft", async () => {
  const action = vi.fn();
  function View() {
    const [message, setMessage] = useProductMessage("");
    const [draft, setDraft] = useState("Original draft");
    return <><input aria-label="Draft" value={draft} onChange={event => setDraft(event.target.value)} /><button onClick={() => { action("stable-request-id"); setMessage(ownedMessage("language.saved")); }}>Run</button><p role="status">{message}</p></>;
  }
  render(<View />); fireEvent.click(screen.getByRole("button"));
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("status").textContent).toContain("언어를 저장했습니다.");
  expect(screen.getByRole("textbox")).toHaveProperty("value", "Original draft");
  expect(action).toHaveBeenCalledExactlyOnceWith("stable-request-id");
});

test("numbers retain BigInt precision and plural grammar belongs to each language", async () => {
  for (const language of ["en", "ko"]) {
    await i18n.changeLanguage(language);
    expect(formatNumber(90071992547409930001n)).toBe("90,071,992,547,409,930,001");
  }
  const key = "fixture.count" as MessageKey;
  i18n.addResource("en", "translation", `${key}_one`, "{{count}} session");
  i18n.addResource("en", "translation", `${key}_other`, "{{count}} sessions");
  i18n.addResource("ko", "translation", `${key}_other`, "세션 {{count}}개");
  await i18n.changeLanguage("en"); expect(copy(key, { count: 1 })).toBe("1 session"); expect(copy(key, { count: 2 })).toBe("2 sessions");
  await i18n.changeLanguage("ko"); expect(copy(key, { count: 0 })).toBe("세션 0개"); expect(copy(key, { count: 1 })).toBe("세션 1개");
});

test("typed guidance translates while original server details remain in a disclosure", async () => {
  await i18n.changeLanguage("ko");
  render(<Failure failure={{ code: FailureCode.Conflict, message: "ORIGINAL_SERVICE_MESSAGE", guidance: "ORIGINAL_SERVICE_GUIDANCE", correlationId: "technical-id" }} />);
  const summary = screen.getByText("기술 상세");
  expect(summary.closest("details")!.hasAttribute("open")).toBe(false);
  expect(summary.closest("details")!.textContent).toContain("ORIGINAL_SERVICE_MESSAGE");
  expect(summary.closest("details")!.textContent).toContain("ORIGINAL_SERVICE_GUIDANCE");
  expect(screen.getByRole("alert").textContent).toContain("DeliDev가 이 요청을 완료하지 못했습니다.");
});

test("exact decimal display preserves every fractional digit and independent currency basis", async () => {
  for (const language of ["en", "ko"]) {
    await i18n.changeLanguage(language);
    expect(formatDecimal("90071992547409930001.123456789012345")).toBe("90,071,992,547,409,930,001.123456789012345");
    expect(formatDecimal("0.000000000000001")).toBe("0.000000000000001");
    expect(formatDecimal("-0.0000")).toBe("-0.0000");
    expect(formatDecimal("unavailable")).toBe("unavailable");
    expect(copy("agent-configuration.templateCount", { count: 1 })).toBe(language === "en" ? "1 template" : "템플릿 1개");
  }
});

test("presentation retains source offsets, submillisecond precision and unknown identifiers", async () => {
  const original = "2026-10-06T23:59:59.123456789+09:00";
  await i18n.changeLanguage("en");
  expect(formatTimestamp(original)).toBe(original);
  expect(statusLabel("WAITING_FOR_IDLE")).toBe("WAITING_FOR_IDLE");
  await i18n.changeLanguage("ko");
  expect(formatTimestamp(original)).toBe("2026년 10월 6일 23:59:59.123456789 UTC+09:00");
  expect(formatTimestamp("2026-02-30T23:59:59Z")).toBe("2026-02-30T23:59:59Z");
  expect(statusLabel("WAITING_FOR_IDLE")).toBe("유휴 상태 대기 중");
  expect(statusLabel("external-future-state")).toBe("external-future-state");
});
