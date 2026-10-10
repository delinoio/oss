// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { CodexReasoning } from "./codex-reasoning";
import { readCodexReasoning } from "./codex-reasoning-observation";
import { TranscriptItem } from "./session";
import { transcriptResource } from "./transcript-role-fixtures";
import { statusLabel } from "./product-status";
import { i18n } from "./localization";
import { AppearanceProvider, Theme } from "./appearance";
import { defaultPreferences, DisclosureDefault } from "./appearance-preferences";
const snapshot = (summary: string[] = [], content: string[] = []) => ({ kind: "reasoning", text: "", summary, content });
const empty = { started: snapshot(), completed: snapshot() };
afterEach(async () => { await i18n.changeLanguage("en"); });
it("removes the outer artifact card and routine completion decoration, retaining an empty collapsed disclosure", () => {
  const view = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: empty })} />);
  expect(view.container.querySelector(".message-codex-reasoning")).toBeTruthy();
  const trigger = screen.getByText("Thinking");
  expect(trigger.closest("details")?.open).toBe(false);
  expect(screen.queryByText("Artifact")).toBeNull(); expect(screen.queryByText("complete")).toBeNull(); expect(screen.queryByText("Completed artifact")).toBeNull();
  expect(view.container.querySelector(".message-codex-reasoning > header")).toBeNull();
  fireEvent.click(trigger);
  expect(screen.getByText("No reasoning content was supplied.")).toBeTruthy();
  expect(view.container.querySelectorAll("details")).toHaveLength(1);
});
it("keeps exact initial, indexed streamed and final text inert and does not deduplicate observations", () => {
  const original = "  原本🙂\n<script>native()</script>  ";
  const artifact = { started: snapshot(["", original], ["initial content"]), completed: snapshot([original], ["final content"]), deltas: [
    { sequence: 2, delta: { kind: "reasoning-summary-added", index: 2, text: "" } },
    { sequence: 4, delta: { kind: "reasoning-summary", index: 2, text: original } },
    { sequence: 7, delta: { kind: "reasoning-content", index: 1, text: "stream content" } },
  ] };
  const view = render(<CodexReasoning observation={readCodexReasoning(artifact, "complete")!} />);
  const trigger = view.container.querySelector("summary")!;
  expect(trigger.getAttribute("aria-label")).toBe(original);
  expect(trigger.querySelector("span")?.textContent).toBe(original);
  fireEvent.click(trigger);
  const sections = view.container.querySelectorAll(".codex-reasoning-body > section");
  expect([...sections].map(section => section.getAttribute("aria-label"))).toEqual(["Initial observation", "Streamed observations", "Final observation"]);
  expect([...view.container.querySelectorAll("pre")].map(pre => pre.textContent)).toEqual([original, "initial content", original, "stream content", original, "final content"]);
  expect(screen.getByText("Summary part 1")).toBeTruthy();
  expect(screen.getByText("reasoning-summary-added · part 2 · sequence 2")).toBeTruthy();
  expect(view.container.querySelector("script")).toBeNull();
});
it("retains manual expansion and the same focused summary through streaming completion and locale updates", async () => {
  const started = snapshot(["Original summary"]);
  const view = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "streaming", artifact: { started } }, 2)} />);
  const trigger = view.container.querySelector("summary")!;
  act(() => trigger.focus()); fireEvent.click(trigger);
  await waitFor(() => expect(trigger.closest("details")?.open).toBe(true));
  view.rerender(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started, completed: snapshot(["Final summary"]) } }, 2, 2n)} />);
  expect(view.container.querySelector("summary")).toBe(trigger); expect(document.activeElement).toBe(trigger); expect(trigger.closest("details")?.open).toBe(true);
  await act(() => i18n.changeLanguage("ko"));
  expect(trigger.closest("details")?.open).toBe(true); expect(document.activeElement).toBe(trigger); expect(trigger.getAttribute("aria-label")).toBe("Final summary");
});
it.each([DisclosureDefault.Original, DisclosureDefault.Expanded, DisclosureDefault.Collapsed])("uses %s only as the initial preference", async preference => {
  const preferences = { ...defaultPreferences(), reasoning_disclosure: preference };
  const view = render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme: Theme.Light, problem: null, preferences }), update: async () => ({}), subscribe: async () => () => {} }}><CodexReasoning observation={readCodexReasoning(empty, "complete")!} /></AppearanceProvider>);
  const trigger = view.container.querySelector("summary")!, details = trigger.closest("details")!;
  await waitFor(() => expect(details.open).toBe(preference === DisclosureDefault.Expanded));
  fireEvent.click(trigger);
  await waitFor(() => expect(details.open).toBe(preference !== DisclosureDefault.Expanded));
  await act(() => i18n.changeLanguage("ko"));
  expect(details.open).toBe(preference !== DisclosureDefault.Expanded);
});
it.each(["streaming", "interrupted", "failed", "unavailable"])("retains the original %s state", state => {
  const view = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state, artifact: { started: snapshot() } })} />);
  expect(view.container.querySelector(".message-codex-reasoning > header")?.textContent).toBe(statusLabel(state));
});
it("leaves malformed Codex and other artifacts in their existing fallback presentation", () => {
  const view = render(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started: { ...snapshot(), summary: [null] }, completed: snapshot() } })} />);
  expect(view.container.querySelector(".message-codex-reasoning")).toBeNull();
  expect(screen.getByText("Completed artifact")).toBeTruthy();
  view.rerender(<TranscriptItem resource={transcriptResource({ role: "artifact", text: "", state: "complete", artifact: { started: { kind: "plan", text: "Plan source" }, completed: { kind: "plan", text: "Final plan" } } }, 2)} />);
  expect(view.container.querySelector(".message-codex-reasoning")).toBeNull(); expect(screen.getByText("Plan source")).toBeTruthy(); expect(screen.getByText("Final plan")).toBeTruthy();
});
