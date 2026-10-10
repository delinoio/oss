// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { render, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SkillCompletionOverlay, skillCompletionGeometry } from "./skill-completion-overlay";
const surface = { left: 0, top: 0, right: 640, bottom: 480 };
it("prefers above, falls below only when it fits, and clamps constrained content", () => {
  expect(skillCompletionGeometry({ left: 40, top: 300, right: 600, bottom: 350, width: 560 }, surface, 500)).toEqual({ left: 40, top: 72, width: 560, maxHeight: 220 });
  expect(skillCompletionGeometry({ left: 40, top: 30, right: 600, bottom: 80, width: 560 }, surface, 220)).toEqual({ left: 40, top: 88, width: 560, maxHeight: 220 });
  expect(skillCompletionGeometry({ left: -20, top: 200, right: 700, bottom: 320, width: 720 }, surface, 500)).toEqual({ left: 8, top: 8, width: 624, maxHeight: 184 });
  expect(skillCompletionGeometry({ left: 20, top: 100, right: 600, bottom: 130, width: 580 }, { left: 10, top: 20, right: 610, bottom: 240 }, 220)).toEqual({ left: 20, top: 138, width: 580, maxHeight: 94 });
});
function Owner({ dismiss }: { dismiss: () => void }) {
  const anchor = useRef<HTMLTextAreaElement>(null), panel = useRef<HTMLDivElement>(null);
  return <fieldset><textarea ref={anchor} defaultValue="$draft"/><SkillCompletionOverlay anchor={anchor} panel={panel} dismiss={dismiss}><p>Fixture inventory</p></SkillCompletionOverlay></fieldset>;
}
for (const lock of ["disabled", "hidden", "inert", "removed"] as const) it(`disposes the original ${lock} composer without changing its draft or focus`, async () => {
  const show = vi.fn(), hide = vi.fn(), dismiss = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "showPopover", { configurable: true, value: show });
  Object.defineProperty(HTMLElement.prototype, "hidePopover", { configurable: true, value: hide });
  const view = render(<Owner dismiss={dismiss}/>), input = view.container.querySelector("textarea")!, fieldset = view.container.querySelector("fieldset")!;
  input.focus(); expect(show).toHaveBeenCalledOnce(); expect(view.container.querySelector("[popover=manual]")).not.toBeNull();
  if (lock === "removed") fieldset.remove(); else fieldset.setAttribute(lock, "");
  await waitFor(() => expect(dismiss).toHaveBeenCalledOnce()); expect(hide).toHaveBeenCalled(); expect(input.value).toBe("$draft");
  if (lock !== "removed") expect(document.activeElement).toBe(input);
  if (lock === "removed") view.container.append(fieldset);
  view.unmount(); delete (HTMLElement.prototype as HTMLElement & { showPopover?: unknown }).showPopover; delete (HTMLElement.prototype as HTMLElement & { hidePopover?: unknown }).hidePopover;
});
