// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeWebSearch, nativeWebObservation } from "./native-web-search";
import { i18n } from "./localization";
function fixture(
  action: Record<string, unknown> | null = {
    type: "search",
    query: "Exact query",
    queries: ["First", "Second"],
  },
) {
  const web = {
    query: "Original query",
    action,
    results: '[{"original":9007199254740993,"title":"원본"}]',
    mode: "cached",
    source: "openai",
  };
  return {
    started: {
      kind: "web-search",
      status: "running",
      web_search: { ...web, results: null },
    },
    completed: { kind: "web-search", status: "completed", web_search: web },
  };
}
afterEach(async () => {
  await i18n.changeLanguage("en");
});
test.each([
  [{ type: "search", query: "Exact query" }, "Web search"],
  [{ type: "openPage", url: "https://fixture.invalid" }, "Open page"],
  [
    { type: "findInPage", url: "https://fixture.invalid", pattern: "Exact" },
    "Find text",
  ],
] as const)(
  "starts collapsed and shows only original native evidence for %s",
  (action, label) => {
    const { container } = render(<NativeWebSearch tool={fixture(action)} />);
    const disclosure = container.querySelector("details")!;
    expect(disclosure.open).toBe(false);
    fireEvent.click(screen.getByText(label));
    expect(disclosure.open).toBe(true);
    expect(screen.getAllByText("Original query").length).toBeGreaterThan(0);
    expect(
      screen.getByText('[{"original":9007199254740993,"title":"원본"}]'),
    ).toBeTruthy();
    expect(container.textContent).not.toMatch(
      /Completed|Success|Done|Result observed|Mode not reported|cached|live/i,
    );
    expect(container.querySelectorAll("button,a")).toHaveLength(0);
  },
);
test("preserves missing native results without claiming completion or offering retransmission", () => {
  const tool = fixture();
  tool.completed.web_search.results = null as unknown as string;
  render(<NativeWebSearch tool={tool} />);
  expect(screen.getByText(/did not provide a result/)).toBeTruthy();
  expect(screen.queryByText(/completed/i)).toBeNull();
});
test("rejects malformed unions and source drift without rendering their result", () => {
  const tool = fixture({ type: "other", url: null });
  expect(nativeWebObservation(tool)).toBeUndefined();
  const replacement = fixture();
  replacement.completed.web_search.mode = "live";
  const { container } = render(<NativeWebSearch tool={replacement} />);
  expect(screen.getByRole("status").textContent).toContain("unavailable");
  expect(container.textContent).not.toContain("9007199254740993");
});
test("keeps the disclosure owner while English changes to Korean", async () => {
  const { container } = render(<NativeWebSearch tool={fixture()} />);
  const disclosure = container.querySelector("details")!;
  fireEvent.click(screen.getByText("Web search"));
  await i18n.changeLanguage("ko");
  expect(container.querySelector("details")).toBe(disclosure);
  expect(disclosure.open).toBe(true);
  expect(screen.getByText("웹 검색")).toBeTruthy();
});
