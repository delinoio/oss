// SPDX-License-Identifier: Apache-2.0
import { createRef, useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FlatDisclosureScope, Disclosure, DisclosureButton, DisclosureContent, DisclosureDensity, DisclosureSummary } from "./disclosure";

describe("shared inline disclosures", () => {
  it("retains real native elements, original refs, programmatic reveal and toggle events", async () => {
    const ref = createRef<HTMLDetailsElement>(), trigger = createRef<HTMLElement>(), toggle = vi.fn();
    render(<Disclosure ref={ref} onToggle={toggle} density={DisclosureDensity.Settings}><DisclosureSummary ref={trigger}>Original evidence</DisclosureSummary><input aria-label="Native field" /></Disclosure>);
    expect(ref.current).toBeInstanceOf(HTMLDetailsElement);
    expect(trigger.current?.tagName).toBe("SUMMARY");
    expect(ref.current?.open).toBe(false);
    expect(trigger.current?.getAttribute("aria-controls")).toBe(ref.current?.querySelector(".disclosure-native-content")?.id);
    ref.current!.open = true;
    await waitFor(() => expect(trigger.current?.getAttribute("aria-expanded")).toBe("true"));
    expect(toggle).toHaveBeenCalledTimes(1);
    screen.getByLabelText("Native field").focus();
    ref.current!.open = false;
    expect(document.activeElement).toBe(trigger.current);
    await waitFor(() => expect(toggle).toHaveBeenCalledTimes(2));
  });

  it("keeps native siblings and nested disclosures independent and content mounted", async () => {
    const outer = createRef<HTMLDetailsElement>(), inner = createRef<HTMLDetailsElement>(), sibling = createRef<HTMLDetailsElement>();
    render(<><Disclosure ref={outer} open><DisclosureSummary>Parent</DisclosureSummary><Disclosure ref={inner}><DisclosureSummary>Child</DisclosureSummary><input aria-label="Retained draft" defaultValue="original" /></Disclosure></Disclosure><Disclosure ref={sibling}><DisclosureSummary>Sibling</DisclosureSummary>Other</Disclosure></>);
    fireEvent.click(screen.getByText("Child"));
    await waitFor(() => expect(inner.current!.open).toBe(true));
    expect(outer.current!.open).toBe(true); expect(sibling.current!.open).toBe(false);
    const input = screen.getByLabelText("Retained draft") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "unsent" } });
    outer.current!.open = false; outer.current!.open = true;
    expect(screen.getByLabelText("Retained draft")).toBe(input); expect(input.value).toBe("unsent");
  });

  it("restores descendant focus before a controlled click disposes its original content", () => {
    const transition = vi.fn();
    function Owner() { const [open, setOpen] = useState(true); return <><DisclosureButton aria-expanded={open} aria-controls="owned-content" onClick={() => { transition(document.activeElement?.textContent); setOpen(!open); }}>Show observations</DisclosureButton><DisclosureContent id="owned-content" hidden={!open}>{open ? <input aria-label="Owned field" /> : null}</DisclosureContent></>; }
    render(<Owner />); screen.getByLabelText("Owned field").focus();
    fireEvent.click(screen.getByRole("button", { name: "Show observations" }));
    expect(transition).toHaveBeenCalledTimes(1);
    expect(transition.mock.calls[0][0]).toBe("Show observations");
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Show observations" }));
    expect(screen.queryByLabelText("Owned field")).toBeNull();
  });

  it("retains hidden-mounted drafts and leaves unrelated focus unchanged", () => {
    function Owner() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(false)}>External close</button><DisclosureButton aria-expanded={open} aria-controls="mounted-content" onClick={() => setOpen(!open)}>Details</DisclosureButton><DisclosureContent id="mounted-content" hidden={!open}><input aria-label="Mounted draft" defaultValue="retained" /></DisclosureContent><button>Unrelated</button></>; }
    render(<Owner />); const input = screen.getByLabelText("Mounted draft");
    const unrelated = screen.getByRole("button", { name: "Unrelated" }); unrelated.focus();
    fireEvent.click(screen.getByRole("button", { name: "External close" }));
    expect(document.activeElement).toBe(unrelated); expect(input.closest("[hidden]")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Details" })); expect(screen.getByLabelText("Mounted draft")).toBe(input);
  });

  it("does not activate disabled controlled triggers or wrap sibling actions", () => {
    const toggle = vi.fn(), action = vi.fn();
    render(<><DisclosureButton disabled aria-expanded={false} onClick={toggle} density={DisclosureDensity.Compact}>Project</DisclosureButton><button onClick={action}>New session</button></>);
    fireEvent.click(screen.getByRole("button", { name: "Project" })); fireEvent.click(screen.getByRole("button", { name: "New session" }));
    expect(toggle).not.toHaveBeenCalled(); expect(action).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Project" }).querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });
});

it("restores focused mounted content during an owner-driven collapse", () => {
  function Owner() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(false)}>Owner close</button><DisclosureButton aria-expanded={open} aria-controls="owner-hidden" onClick={() => setOpen(!open)}>Owner details</DisclosureButton><DisclosureContent id="owner-hidden" hidden={!open}><input aria-label="Owner draft" /></DisclosureContent></>; }
  render(<Owner />); screen.getByLabelText("Owner draft").focus();
  fireEvent.click(screen.getByRole("button", { name: "Owner close" }));
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Owner details" }));
});

it("restores native descendant focus before attribute-based closes", () => {
  const owner = createRef<HTMLDetailsElement>();
  render(<Disclosure ref={owner} open><DisclosureSummary>Attribute details</DisclosureSummary><input aria-label="Attribute draft" /></Disclosure>);
  const field = screen.getByLabelText("Attribute draft"), trigger = owner.current!.querySelector("summary");
  field.focus(); owner.current!.removeAttribute("open"); expect(document.activeElement).toBe(trigger);
  owner.current!.setAttribute("open", ""); field.focus(); owner.current!.toggleAttribute("open", false); expect(document.activeElement).toBe(trigger);
});

it("renders Info technical groups as flat headings without hiding mounted content",()=>{const view=render(<FlatDisclosureScope><Disclosure><DisclosureSummary>Original source</DisclosureSummary><p>Retained evidence</p></Disclosure></FlatDisclosureScope>);expect(screen.getByRole("heading",{name:"Original source"})).toBeTruthy();expect(screen.getByText("Retained evidence")).toBeTruthy();expect(view.container.querySelector("details,summary")).toBeNull();});
