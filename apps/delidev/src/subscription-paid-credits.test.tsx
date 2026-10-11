// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import {
  PaidCredits,
  PaidCreditState,
  paidCreditProjection,
  formatPaidCreditBalance,
} from "./subscription-paid-credits";
const at = "2026-10-09T00:00:00Z",
  now = Date.parse(at),
  bucket = {
    id: "codex",
    hasCredits: true,
    unlimited: false,
    balance: "1250.50000000000000001",
    observedAt: at,
  };
describe("paid-credit projection", () => {
  it("preserves decimal strings, null and bucket identity", () => {
    const parsed = paidCreditProjection({
      paid_credits: [
        {
          id: "codex",
          has_credits: true,
          unlimited: false,
          balance: bucket.balance,
          observed_at: at,
        },
        {
          id: "another",
          has_credits: false,
          unlimited: true,
          balance: null,
          observed_at: at,
        },
      ],
    });
    expect(parsed.map((v) => v.balance)).toEqual([bucket.balance, null]);
    expect(parsed.map((v) => v.id)).toEqual(["codex", "another"]);
  });
  it.each(["-1", "1e4", "private", "1.", "1".repeat(65), undefined])(
    "refuses malformed values %s",
    (balance) => {
      expect(
        paidCreditProjection({
          paid_credits: [
            {
              id: "codex",
              has_credits: true,
              unlimited: false,
              balance,
              observed_at: at,
            },
          ],
        }),
      ).toEqual([]);
    },
  );
  it("shows rounded balances with unchanged exact disclosure", () => {
    render(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Observed}
        now={now}
      />,
    );
    expect(screen.getByText("1,250.50")).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "Show exact paid-credit balance" }),
    );
    expect(screen.getByRole("tooltip").textContent).toBe(
      `Exact balance: ${bucket.balance}`,
    );
    expect(screen.getByText("codex")).toBeTruthy();
  });
  it("keeps zero, null and unlimited distinct and never sums buckets", () => {
    render(
      <PaidCredits
        buckets={[
          { ...bucket, id: "zero", balance: "0" },
          { ...bucket, id: "null", balance: null },
          { ...bucket, id: "unlimited", unlimited: true, balance: "15" },
        ]}
        state={PaidCreditState.Observed}
        now={now}
      />,
    );
    expect(screen.getByText("0.00")).toBeTruthy();
    expect(screen.getByText("Balance unknown")).toBeTruthy();
    expect(screen.getByText("Unlimited credits")).toBeTruthy();
    expect(screen.queryByText("15.00")).toBeNull();
  });
  it("retains values with truthful failure and stale labels", () => {
    const v = render(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Failed}
        now={now + 600000}
      />,
    );
    expect(screen.getByText("1,250.50")).toBeTruthy();
    expect(
      screen.getByText(/Refresh failed; retained observation/),
    ).toBeTruthy();
    v.rerender(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Observed}
        now={now + 600000}
      />,
    );
    expect(screen.getByText(/Stale observation/)).toBeTruthy();
  });
  it("shows a count for multiple compact balances and no aggregate", () => {
    render(
      <PaidCredits
        buckets={[bucket, { ...bucket, id: "another", balance: "2" }]}
        state={PaidCreditState.Observed}
        compact
        now={now}
      />,
    );
    expect(screen.getByText(/2 credit balances/)).toBeTruthy();
    expect(screen.queryByText(bucket.balance + " credits")).toBeNull();
  });
  it("support states cannot fabricate zero or send a request", () => {
    const v = render(<PaidCredits state={PaidCreditState.Unsupported} />);
    expect(screen.getByRole("status").textContent).toContain(
      "Update the server",
    );
    v.rerender(<PaidCredits state={PaidCreditState.Loading} />);
    expect(screen.getByRole("status").textContent).toContain("Loading");
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });
});

it.each([false, true])(
  "shows initial refresh failure without inventing retained evidence (compact %s)",
  async (compact) => {
    const v = render(
      <PaidCredits state={PaidCreditState.Failed} compact={compact} />,
    );
    expect(
      screen.getByText(/Refresh failed; no balance observed/),
    ).toBeTruthy();
    expect(
      screen.queryByText(/Balance unknown|retained observation/),
    ).toBeNull();
    await i18n.changeLanguage(SupportedLanguage.Korean);
    expect(screen.getByText(/새로고침 실패; 관측된 잔액 없음/)).toBeTruthy();
    expect(screen.queryByText(/잔액 알 수 없음|기존 관측 유지/)).toBeNull();
    await i18n.changeLanguage(SupportedLanguage.English);
    v.rerender(
      <PaidCredits state={PaidCreditState.Unknown} compact={compact} />,
    );
    expect(screen.getByText(/Balance unknown/)).toBeTruthy();
    expect(screen.queryByText(/Refresh failed/)).toBeNull();
  },
);

describe("paid-credit decimal display", () => {
  it.each([
    ["60961.1135370000", "60,961.11"],
    ["1.995", "2.00"],
    ["0", "0.00"],
    ["0.004", "<0.01"],
    ["0.00999", "<0.01"],
    ["0.010", "0.01"],
    ["999.999", "1,000.00"],
    ["00012.125", "12.13"],
    ["000.000", "0.00"],
    ["9".repeat(61) + ".99", "9" + ",999".repeat(20) + ".99"],
  ])("formats %s as %s with exact decimal arithmetic", (source, expected) => {
    expect(formatPaidCreditBalance(source)).toBe(expected);
  });
  it.each(["", "-1", "1e9", "1.", "1".repeat(65)])(
    "refuses malformed display evidence %s",
    (source) => {
      expect(formatPaidCreditBalance(source)).toBeNull();
    },
  );
  it("opens exact disclosure through hover, focus and click, and consumes Escape", () => {
    render(
      <PaidCredits
        buckets={[{ ...bucket, balance: "60961.1135370000" }]}
        state={PaidCreditState.Observed}
        now={now}
        compact
      />,
    );
    const trigger = screen.getByRole("button", {
      name: "Show exact paid-credit balance",
    });
    fireEvent.mouseEnter(screen.getByText("60,961.11"));
    expect(screen.getByRole("tooltip").textContent).toBe(
      "Exact balance: 60961.1135370000",
    );
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.queryByRole("tooltip")).toBeNull();
    expect(document.activeElement).not.toBe(trigger);
    fireEvent.mouseEnter(screen.getByText("60,961.11"));
    fireEvent.mouseLeave(trigger.parentElement!);
    expect(screen.queryByRole("tooltip")).toBeNull();
    act(() => trigger.focus());
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(trigger, { key: "Escape" });
    expect(screen.queryByRole("tooltip")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    fireEvent.click(trigger);
    expect(screen.getByRole("tooltip")).toBeTruthy();
    fireEvent.click(trigger);
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
  it("keeps exact 64-character values and long bucket labels in source order", () => {
    const balance = "9".repeat(61) + ".99";
    const id = "codex:" + "a".repeat(100);
    render(
      <PaidCredits
        buckets={[
          { ...bucket, id, balance },
          { ...bucket, id: "second", balance: "1.995" },
        ]}
        state={PaidCreditState.Observed}
        now={now}
      />,
    );
    expect(
      screen
        .getAllByRole("listitem")
        .map((item) => item.querySelector("strong")?.textContent),
    ).toEqual([id, "second"]);
    fireEvent.click(screen.getAllByRole("button")[0]);
    expect(screen.getByRole("tooltip").textContent).toBe(
      `Exact balance: ${balance}`,
    );
    expect(screen.getByText("2.00")).toBeTruthy();
  });
  it("uses original timestamps at the freshness boundary and rejects future freshness", () => {
    const view = render(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Observed}
        now={now + 300000}
        compact
      />,
    );
    expect(screen.queryByText(/Stale observation/)).toBeNull();
    expect(view.container.querySelector("time")?.dateTime).toBe(at);
    view.rerender(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Observed}
        now={now + 300001}
        compact
      />,
    );
    expect(screen.getByText(/Stale observation/)).toBeTruthy();
    view.rerender(
      <PaidCredits
        buckets={[bucket]}
        state={PaidCreditState.Observed}
        now={now - 1}
        compact
      />,
    );
    expect(screen.getByText(/Stale observation/)).toBeTruthy();
    view.rerender(<PaidCredits state={PaidCreditState.Failed} compact />);
    expect(screen.getByText("Balance unavailable")).toBeTruthy();
    expect(view.container.querySelector("time")).toBeNull();
    expect(screen.queryByText("0.00")).toBeNull();
  });
  it("localizes balance grouping, units, exact disclosure and multiple-bucket summaries", async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
    try {
      const view = render(
        <PaidCredits
          buckets={[{ ...bucket, balance: "60961.1135370000" }]}
          state={PaidCreditState.Observed}
          now={now}
          compact
        />,
      );
      expect(screen.getByText("60,961.11")).toBeTruthy();
      expect(screen.getByText("크레딧")).toBeTruthy();
      fireEvent.click(
        screen.getByRole("button", { name: "정확한 유료 크레딧 잔액 보기" }),
      );
      expect(screen.getByRole("tooltip").textContent).toBe(
        "정확한 잔액: 60961.1135370000",
      );
      view.rerender(
        <PaidCredits
          buckets={[bucket, { ...bucket, id: "second" }]}
          state={PaidCreditState.Observed}
          now={now}
          compact
        />,
      );
      expect(screen.getByText("크레딧 잔액 2개")).toBeTruthy();
      expect(screen.queryByRole("button")).toBeNull();
    } finally {
      await i18n.changeLanguage(SupportedLanguage.English);
    }
  });
});
