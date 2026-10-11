// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { repositoryDetailsPlacement, useRepositoryDetailsPopup } from "./repository-details-popup";
import { DisclosureButton, DisclosureContent } from "./disclosure";

it("places to the right when room exists and constrains below/above fallbacks", () => {
  expect(repositoryDetailsPlacement({ left: 20, right: 220, top: 40, bottom: 80 }, 320, 160, 1000, 700)).toEqual({ left: 228, top: 40, maxHeight: 684 });
  expect(repositoryDetailsPlacement({ left: 20, right: 220, top: 40, bottom: 80 }, 320, 160, 400, 700)).toEqual({ left: 20, top: 88, maxHeight: 604 });
  expect(repositoryDetailsPlacement({ left: 20, right: 220, top: 500, bottom: 540 }, 320, 160, 400, 700)).toEqual({ left: 20, top: 332, maxHeight: 484 });
  const short = repositoryDetailsPlacement({ left: 280, right: 460, top: 200, bottom: 240 }, 320, 1200, 480, 320);
  expect(short.left).toBe(152); expect(short.top).toBe(8); expect(short.maxHeight).toBe(184);
});

function Fixture({ active = true, hidden = false }: { active?: boolean; hidden?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const opener = useRef<HTMLButtonElement>(null), popup = useRef<HTMLDivElement>(null);
  const { visible, reveal } = useRepositoryDetailsPopup(expanded, active, opener, popup, () => setExpanded(false));
  return <><section aria-label="Original sidebar" hidden={hidden}><div><DisclosureButton ref={opener} aria-expanded={visible} aria-controls="popup" onClick={() => expanded && !visible ? reveal() : setExpanded(!expanded)}>Details</DisclosureButton></div><DisclosureContent ref={popup} id="popup" popover="manual" restoreFocusOnHide={false} hidden={!visible} role="region" aria-label="Repository details" tabIndex={0}>owner/repository</DisclosureContent><button>Sibling</button></section><button>Outside</button></>;
}

it("keeps a local manual popup, dismisses without stealing focus and restores the opener only on Escape", () => {
  const show = vi.fn(), hide = vi.fn();
  const view = render(<Fixture />);
  const content = view.container.querySelector<HTMLDivElement>("#popup")!;
  content.showPopover = show; content.hidePopover = hide;
  const trigger = screen.getByRole("button", { name: "Details" });
  fireEvent.click(trigger);
  expect(show).toHaveBeenCalledTimes(1);
  expect(content.closest("section")?.getAttribute("aria-label")).toBe("Original sidebar");
  expect(content.getAttribute("popover")).toBe("manual");
  expect(trigger.getAttribute("aria-expanded")).toBe("true");
  const outside = screen.getByRole("button", { name: "Outside" }); outside.focus();
  fireEvent.pointerDown(outside); fireEvent.click(outside);
  expect(content.hidden).toBe(true); expect(document.activeElement).toBe(outside);
  expect(hide).toHaveBeenCalledTimes(1);
  fireEvent.click(trigger); content.focus();
  const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
  act(() => document.dispatchEvent(event));
  expect(event.defaultPrevented).toBe(true); expect(content.hidden).toBe(true);
  expect(document.activeElement).toBe(trigger); expect(hide).toHaveBeenCalledTimes(2);
});

it("hides inactive or concealed openers while retaining the original expansion across reactivation", async () => {
  const view = render(<Fixture />);
  fireEvent.click(screen.getByRole("button", { name: "Details" }));
  view.rerender(<Fixture active={false} />);
  expect(screen.queryByRole("region", { name: "Repository details" })).toBeNull();
  view.rerender(<Fixture />);
  await screen.findByRole("region", { name: "Repository details" });
  view.rerender(<Fixture hidden />);
  await waitFor(() => expect(view.container.querySelector<HTMLDivElement>("#popup")!.hidden).toBe(true));
  view.rerender(<Fixture />);
  await screen.findByRole("region", { name: "Repository details" });
  const trigger = screen.getByRole("button", { name: "Details" });
  const heading = trigger.parentElement!;
  act(() => trigger.remove());
  await waitFor(() => expect(view.container.querySelector<HTMLDivElement>("#popup")!.hidden).toBe(true));
  act(() => heading.append(trigger));
  await screen.findByRole("region", { name: "Repository details" });
  view.unmount();
});
