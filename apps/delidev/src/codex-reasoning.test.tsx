// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CodexReasoning, codexReasoningObservation } from "./codex-reasoning";
import { AppearanceProvider, Theme, type AppearanceBridge } from "./appearance";
import { defaultPreferences, DisclosureDefault } from "./appearance-preferences";
import { i18n, SupportedLanguage } from "./localization";
const snapshot = (summary: string[] = [], content: string[] = []) => ({ kind: "reasoning", text: "", summary, content });
const observation = (value: unknown) => { const result = codexReasoningObservation(value); if (!result) throw new Error("Invalid test observation"); return result; };
it("keeps empty reasoning collapsed with an explicit empty-content explanation", () => {
  const { container } = render(<CodexReasoning observation={observation({ started: snapshot(), completed: snapshot() })} />);
  expect(container.querySelector("details")?.open).toBe(false);
  fireEvent.click(screen.getByText("Thinking"));
  expect(screen.getByText("No reasoning content was supplied.")).toBeTruthy();
  expect(container.querySelector("section,h3,h4,pre")).toBeNull();
  expect(container.textContent).not.toMatch(/Artifact|Complete/);
});
it("selects the unchanged first non-whitespace final summary before initial summary", () => {
  const started = snapshot(["Initial summary"]), completed = snapshot([" \n", "  Final 한글🙂\nNext line.  "]);
  const { container, rerender } = render(<CodexReasoning observation={observation({ started, completed })} />);
  const trigger = container.querySelector("summary")!;
  expect(trigger.textContent).toBe("  Final 한글🙂\nNext line.  ");
  rerender(<CodexReasoning observation={observation({ started, completed: snapshot() })} />);
  expect(trigger.textContent).toBe("Initial summary");
  rerender(<CodexReasoning observation={observation({ started: snapshot(["\n "]) })} />);
  expect(trigger.textContent).toBe("Thinking");
});
it("preserves indexed families, sequence order, empty parts and inert original text", () => {
  const started = snapshot(["Initial summary", ""], ["  original\n"]), completed = snapshot(["Final summary"], ["<script>run()</script>\n命令"]);
  const deltas = [
    { sequence: 3, delta: { kind: "reasoning-summary-added", index: 2, text: "" } },
    { sequence: 4, delta: { kind: "reasoning-summary", index: 2, text: " delta " } },
    { sequence: 7, delta: { kind: "reasoning-content", index: 0, text: "\n  retained" } },
  ];
  const { container } = render(<CodexReasoning observation={observation({ started, deltas, completed })} />);
  fireEvent.click(container.querySelector("summary")!);
  const sections = container.querySelectorAll("section");
  expect([...sections].map(s => s.getAttribute("aria-label"))).toEqual(["Initial observation", "Streamed observations", "Final observation"]);
  expect([...container.querySelectorAll("pre")].map(p => p.textContent)).toEqual(["Initial summary", "", "  original\n", "", " delta ", "\n  retained", "Final summary", "<script>run()</script>\n命令"]);
  expect(within(sections[0] as HTMLElement).getAllByText("Part index 0")).toHaveLength(2);
  expect(sections[1].textContent).toContain("reasoning-summary-added [2] · Sequence 3");
  expect(container.querySelector("script,a,button,input")).toBeNull();
});
it("retains explicit expansion and focused native trigger across completion and locale changes", async () => {
  const started = snapshot(["Initial summary"]);
  const { container, rerender } = render(<CodexReasoning observation={observation({ started })} />);
  const trigger = container.querySelector("summary")!;
  trigger.focus(); fireEvent.click(trigger);
  await waitFor(() => expect(container.querySelector("details")?.open).toBe(true));
  rerender(<CodexReasoning observation={observation({ started, completed: snapshot(["Final summary"]) })} />);
  await act(() => i18n.changeLanguage(SupportedLanguage.Korean));
  expect(container.querySelector("details")?.open).toBe(true);
  expect(document.activeElement).toBe(trigger);
  expect(screen.getByText("최종 관찰")).toBeTruthy();
});
it.each([DisclosureDefault.Original, DisclosureDefault.Expanded, DisclosureDefault.Collapsed])("honors %s initial preference and subsequent manual choices", async preference => {
  const preferences = { ...defaultPreferences(), reasoning_disclosure: preference };
  const bridge: AppearanceBridge = { read: vi.fn(async () => ({ revision: 1, theme: Theme.System, problem: null, preferences })), update: vi.fn(), subscribe: vi.fn(async () => () => {}) };
  const view = (final = false) => <AppearanceProvider bridge={bridge}><CodexReasoning observation={observation({ started: snapshot(["summary"]), ...(final ? { completed: snapshot(["final"]) } : {}) })} /></AppearanceProvider>;
  const { container, rerender } = render(view());
  await waitFor(() => expect(bridge.read).toHaveBeenCalled());
  await waitFor(() => expect(container.querySelector("details")?.open).toBe(preference === DisclosureDefault.Expanded));
  fireEvent.click(container.querySelector("summary")!);
  await waitFor(() => expect(container.querySelector("details")?.open).toBe(preference !== DisclosureDefault.Expanded));
  rerender(view(true));
  expect(container.querySelector("details")?.open).toBe(preference !== DisclosureDefault.Expanded);
});
it.each([
  { started: snapshot([null as unknown as string]) },
  { started: { ...snapshot(), text: "foreign text" } },
  { started: { ...snapshot(), kind: "reasoning-text" } },
  { started: snapshot(), completed: { ...snapshot(), kind: "plan" } },
  { started: snapshot(), deltas: [{ sequence: 1, delta: { kind: "reasoning-content", index: null, text: "foreign" } }] },
  { started: snapshot(), deltas: [{ sequence: 1, delta: { kind: "reasoning-summary-added", index: 0, text: "invented" } }] },
  { started: snapshot(["\uD800"]) },
  { started: snapshot(["한".repeat(90000)]) },
])("leaves malformed and foreign artifacts to the original fallback", value => expect(codexReasoningObservation(value)).toBeUndefined());
it("localizes empty reasoning without reconstructing hidden content", async () => {
  await act(() => i18n.changeLanguage(SupportedLanguage.Korean));
  render(<CodexReasoning observation={observation({ started: snapshot() })} />);
  fireEvent.click(screen.getByText("생각 과정"));
  expect(screen.getByText("제공된 생각 과정이 없습니다.")).toBeTruthy();
});
