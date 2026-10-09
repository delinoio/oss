// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ProviderPresetId, providerPresetNames } from "@delinoio/delidev-api-client";
import { ProviderMark, providerMarkFamilies } from "./provider-mark";

it("covers every existing preset with a local decorative family mark", () => {
 const names = [...providerPresetNames];
 expect(names).toHaveLength(35); expect(providerMarkFamilies.size).toBe(35);
 const view = render(<>{names.map(([preset, name]) => <h4 key={preset}><ProviderMark preset={preset}/><span>{name}</span></h4>)}</>);
 expect(view.container.querySelectorAll("img")).toHaveLength(35);
 for (const [preset, name] of names) {
  const heading = screen.getByRole("heading", { name }); const mark = heading.querySelector(".api-provider-mark")!; const image = mark.querySelector("img")!;
  expect(mark.getAttribute("aria-hidden")).toBe("true"); expect(image.alt).toBe(""); expect(image.getAttribute("src")).toBe(`./provider-marks/${providerMarkFamilies.get(preset)}.svg`);
 }
 expect(screen.queryAllByRole("img")).toHaveLength(0);
 expect(providerMarkFamilies.get(ProviderPresetId.MOONSHOT_CN)).toBe(providerMarkFamilies.get(ProviderPresetId.MOONSHOT));
 expect(providerMarkFamilies.get(ProviderPresetId.MINIMAX_CN)).toBe(providerMarkFamilies.get(ProviderPresetId.MINIMAX));
 expect(providerMarkFamilies.get(ProviderPresetId.SILICONFLOW_CN)).toBe("siliconcloud");
 expect(providerMarkFamilies.get(ProviderPresetId.QIANFAN)).toBe("baiducloud");
 expect(providerMarkFamilies.get(ProviderPresetId.SCALEWAY)).toBe("scaleway");
});

it("keeps custom names and unknown identities neutral and preserves the name", () => {
 const view = render(<><h4><ProviderMark preset={ProviderPresetId.UNSPECIFIED}/><span>OpenAI</span></h4><h4><ProviderMark preset={999 as ProviderPresetId}/><span>Unknown provider</span></h4></>);
 expect(view.container.querySelectorAll("img")).toHaveLength(0); expect(view.container.querySelectorAll(".api-provider-mark svg")).toHaveLength(2);
 expect(screen.getByRole("heading", { name: "OpenAI" })).toBeDefined(); expect(screen.getByRole("heading", { name: "Unknown provider" })).toBeDefined();
});

it("replaces a failed image in its existing slot and resets only for a different asset", () => {
 const view = render(<h4><ProviderMark preset={ProviderPresetId.OPENAI}/><span>Provider</span></h4>); const slot = view.container.querySelector(".api-provider-mark");
 fireEvent.error(view.container.querySelector("img")!); expect(view.container.querySelector("img")).toBeNull(); expect(view.container.querySelector(".api-provider-mark")).toBe(slot); expect(slot?.querySelector("svg")).toBeDefined();
 view.rerender(<h4><ProviderMark preset={ProviderPresetId.OPENAI}/><span>Renamed provider</span></h4>); expect(view.container.querySelector("img")).toBeNull();
 view.rerender(<h4><ProviderMark preset={ProviderPresetId.ANTHROPIC}/><span>Renamed provider</span></h4>); expect(view.container.querySelector("img")?.getAttribute("src")).toBe("./provider-marks/anthropic.svg");
 view.rerender(<h4><ProviderMark preset={ProviderPresetId.UNSPECIFIED}/><span>Renamed provider</span></h4>); expect(view.container.querySelector("img")).toBeNull();
});
