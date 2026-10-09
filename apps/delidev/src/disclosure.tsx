// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useId, useLayoutEffect, useRef, useState, type ComponentPropsWithRef, type Ref, type ReactNode } from "react";
import "./disclosure.css";

export enum DisclosureDensity { Settings = "settings", Details = "details", Compact = "compact" }
const Density = createContext(DisclosureDensity.Details);
export function DisclosureDensityScope({ density, children }: { density: DisclosureDensity; children: ReactNode }) { return <Density.Provider value={density}>{children}</Density.Provider>; }
function assign<T>(ref: Ref<T> | undefined, value: T | null) {
  if (typeof ref === "function") ref(value);
  else if (ref) ref.current = value;
}
function restoreFocus(content: Element | null, trigger: HTMLElement | null) {
  if (content?.contains(document.activeElement)) trigger?.focus({ preventScroll: true });
}
const NativeDisclosure = createContext<{ id: string; expanded: boolean } | undefined>(undefined);
function Chevron() { return <svg className="disclosure-chevron" aria-hidden="true" focusable="false" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="m5 3 4 4-4 4" /></svg>; }

/** Native DOM is intentional: pagination, validation and explicit reveal use it. */
export function Disclosure({ ref, density, className = "", onToggle, children, ...props }: ComponentPropsWithRef<"details"> & { density?: DisclosureDensity }) {
  const inheritedDensity = useContext(Density);
  const node = useRef<HTMLDetailsElement>(null);
  useLayoutEffect(() => {
    const element = node.current;
    if (!element) return;
    const descriptor = Object.getOwnPropertyDescriptor(HTMLDetailsElement.prototype, "open")!;
    // Focus must leave descendants before an original programmatic close hides
    // them. Keep native attribute/property behavior and delete this shim on exit.
    Object.defineProperty(element, "open", { configurable: true, get() { return descriptor.get!.call(this); }, set(value: boolean) { if (!value) restoreFocus(element, element.querySelector("summary")); descriptor.set!.call(this, value); } });
    return () => { delete (element as Partial<HTMLDetailsElement>).open; };
  }, []);
  const generatedId = useId(), id = props.id ?? generatedId;
  const [expanded, setExpanded] = useState(Boolean(props.open));
  useLayoutEffect(() => { if (props.open === false) restoreFocus(node.current, node.current?.querySelector("summary") ?? null); if (node.current) setExpanded(node.current.open); }, [props.open]);
  return <NativeDisclosure.Provider value={{ id, expanded }}><details {...props} id={id} ref={value => { node.current = value; assign(ref, value); }} className={`disclosure ${className}`} data-disclosure-density={density ?? inheritedDensity} onToggle={event => { setExpanded(event.currentTarget.open); if (!event.currentTarget.open) restoreFocus(event.currentTarget, event.currentTarget.querySelector("summary")); onToggle?.(event); }}>{children}</details></NativeDisclosure.Provider>;
}
export function DisclosureSummary({ children, className = "", onClick, ...props }: ComponentPropsWithRef<"summary">) {
  const owner = useContext(NativeDisclosure);
  return <summary aria-controls={owner?.id} aria-expanded={owner?.expanded} {...props} className={`disclosure-header ${className}`} onClick={event => { const details = event.currentTarget.parentElement; if (details instanceof HTMLDetailsElement && details.open) restoreFocus(details, event.currentTarget); onClick?.(event); }}><Chevron />{children}</summary>;
}

/** The caller owns expansion and the content's original mount/disposal policy. */
export function DisclosureButton({ density, className = "", children, onClick, focusWhenCollapsing, ...props }: ComponentPropsWithRef<"button"> & { density?: DisclosureDensity; focusWhenCollapsing?: (element: Element) => boolean }) {
  const inheritedDensity = useContext(Density);
  return <button type="button" {...props} className={`disclosure-header ${className}`} data-disclosure-density={density ?? inheritedDensity} onClick={event => { if (props["aria-expanded"] === true) { const id = props["aria-controls"]; if (!focusWhenCollapsing || document.activeElement && focusWhenCollapsing(document.activeElement)) restoreFocus(id ? document.getElementById(id) : null, event.currentTarget); } onClick?.(event); }}><Chevron />{children}</button>;
}
export function DisclosureContent({ ref, children, hidden, onFocusCapture, ...props }: ComponentPropsWithRef<"div">) {
  const node = useRef<HTMLDivElement>(null), focused = useRef<Element | null>(null);
  const [concealed, setConcealed] = useState(Boolean(hidden));
  const focusTrigger = (content: HTMLDivElement | null) => {
    if (!content) return;
    const active = document.activeElement;
    // A removed descendant loses DOM focus before layout cleanup. Restore only
    // that owner's focus; a deliberate move to another control stays intact.
    if (!content.contains(active) && !(active === document.body && focused.current && !focused.current.isConnected)) return;
    const trigger = [...document.querySelectorAll<HTMLButtonElement>("button[aria-controls]")].find(button => button.getAttribute("aria-controls") === content.id);
    trigger?.focus({ preventScroll: true });
  };
  useLayoutEffect(() => {
    if (hidden) focusTrigger(node.current);
    // Close mounted content after focus restoration, before the browser paints.
    setConcealed(Boolean(hidden));
  }, [hidden]);
  useLayoutEffect(() => { const content = node.current; return () => focusTrigger(content); }, []);
  return <div {...props} hidden={concealed} ref={value => { node.current = value; assign(ref, value); }} onFocusCapture={event => { focused.current = event.target; onFocusCapture?.(event); }} data-disclosure-content>{children}</div>;
}
