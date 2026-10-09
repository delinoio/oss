// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ServiceTierField } from "./configuration-fields";
import { i18n, SupportedLanguage } from "./localization";
import type { Document } from "./documents";
function Editor({ initial, unavailable = false }: { initial: Document; unavailable?: boolean }) {
  const [options, setOptions] = useState(initial);
  return <><ServiceTierField value={options.service_tier} unavailable={unavailable} change={service_tier => setOptions(previous => ({ ...previous, service_tier }))} clear={() => setOptions(previous => { const next = { ...previous }; delete next.service_tier; return next; })}/><output data-testid="options">{JSON.stringify(options)}</output></>;
}
const options = () => JSON.parse(screen.getByTestId("options").textContent!);
it("selects Fast explicitly and omits Native default without changing unrelated options", () => {
  render(<Editor initial={{ permission: "read-only", future: { retained: true } }}/>);
  const selector = screen.getByRole("combobox", { name: "Service tier" });
  expect((selector as HTMLSelectElement).value).toBe("default");
  fireEvent.change(selector, { target: { value: "fast" } });expect(options()).toEqual({ permission: "read-only", future: { retained: true }, service_tier: "fast" });
  fireEvent.change(selector, { target: { value: "default" } });expect(options()).toEqual({ permission: "read-only", future: { retained: true } });
});
it.each(["priority", " Future Exact Tier ", "fast"])("reopens the exact existing tier without rewriting it: %s", tier => {
  render(<Editor initial={{ service_tier: tier }}/>);
  expect((screen.getByRole("combobox", { name: "Service tier" }) as HTMLSelectElement).value).toBe(tier === "fast" ? "fast" : "custom");
  expect(options().service_tier).toBe(tier);
});
it.each([{}, { service_tier: "" }, { service_tier: null }])("leaves absent, empty and null storage untouched until explicit selection", initial => {
  render(<Editor initial={initial}/>);expect(options()).toEqual(initial);
  fireEvent.change(screen.getByRole("combobox", { name: "Service tier" }), { target: { value: "custom" } });expect(options()).toEqual(initial);
  fireEvent.change(screen.getByRole("textbox", { name: "Custom service tier" }), { target: { value: " exact-custom " } });expect(options().service_tier).toBe(" exact-custom ");
});
it("preserves unavailable saved values and uses the original explicit clearing interaction", () => {
  render(<Editor initial={{ service_tier: "future-tier", retained: true }} unavailable/>);
  expect((screen.getByRole("textbox", { name: "Service tier" }) as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Clear retained option: Service tier" }));expect(options()).toEqual({ service_tier: "", retained: true });
});
it("localizes the selector, guidance and accessible description", async () => {
  await i18n.changeLanguage(SupportedLanguage.Korean);render(<Editor initial={{}}/>);
  const selector = screen.getByRole("combobox", { name: "서비스 등급" });
  expect(screen.getByRole("option", { name: "빠른 모드" })).toBeTruthy();
  expect(document.getElementById(selector.getAttribute("aria-describedby")!)?.textContent).toContain("구독 사용량");
  expect(screen.getByRole("link", { name: "공식 빠른 모드 안내" }).getAttribute("href")).toBe("https://learn.chatgpt.com/docs/agent-configuration/speed");
});
