// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import {
  NavigationPreferenceNotice,
  useNavigationPreference,
} from "./navigation-preference";

// Outline glyphs share the rail icon geometry. They are local so this module
// does not import the sidebar module that mounts it.
enum MoreGlyph {
  More = "more",
  Usage = "usage",
  Pin = "pin",
}

function Glyph({
  name,
  filled = false,
}: {
  name: MoreGlyph;
  filled?: boolean;
}) {
  const common = {
    "aria-hidden": true as const,
    className: "sidebar-icon",
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.8,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };
  switch (name) {
    case MoreGlyph.More:
      return (
        <svg {...common}>
          <circle cx="5" cy="12" r="1.2" fill="currentColor" />
          <circle cx="12" cy="12" r="1.2" fill="currentColor" />
          <circle cx="19" cy="12" r="1.2" fill="currentColor" />
        </svg>
      );
    case MoreGlyph.Usage:
      return (
        <svg {...common}>
          <path d="M4 20V12M10 20V5M16 20v-9M22 20V8" />
        </svg>
      );
    case MoreGlyph.Pin:
      return (
        <svg {...common} fill={filled ? "currentColor" : "none"}>
          <path d="M9 4h6l-1 6 3 3v2H7v-2l3-3z" />
          <path d="M12 15v5" />
        </svg>
      );
  }
}

/**
 * The rail's More destination list. It owns only the popover presentation: the
 * Usage row reuses the shared navigation callback, and the pin button changes
 * the device-local rail visibility without navigating.
 */
export function SidebarMore({
  usageActive,
  openUsage,
  drawerOpen = false,
}: {
  usageActive: boolean;
  openUsage: () => void;
  drawerOpen?: boolean;
}) {
  useLocale();
  const navigation = useNavigationPreference();
  const pinned = navigation.snapshot.usage_pinned;
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const pin = useRef<HTMLButtonElement>(null);
  // The mounted popover's placement routine, shared by every size change.
  const placement = useRef<(() => void) | undefined>(undefined);
  // True after a Tab key moved focus inside the list and until the next pointer
  // press. It tells keyboard focus movement from a press on the opener, which
  // also blurs the list but must keep toggling it.
  const keyboardFocus = useRef(false);
  const close = (restoreFocus: boolean) => {
    setOpen(false);
    if (restoreFocus) trigger.current?.focus();
  };
  // The modal compact drawer makes the rail inert, so an open list is dismissed.
  useEffect(() => {
    if (drawerOpen) setOpen(false);
  }, [drawerOpen]);
  useLayoutEffect(() => {
    const element = popup.current;
    const opener = trigger.current;
    if (!open || !element || !opener) return;
    const place = () => {
      const bounds = opener.getBoundingClientRect();
      element.style.left = `${Math.max(4, Math.min(bounds.right + 8, window.innerWidth - element.offsetWidth - 4))}px`;
      element.style.top = `${Math.max(4, Math.min(bounds.top, window.innerHeight - element.offsetHeight - 4))}px`;
    };
    placement.current = place;
    element.showPopover?.();
    place();
    element.querySelector<HTMLButtonElement>("button")?.focus();
    const dismiss = (event: PointerEvent) => {
      keyboardFocus.current = false;
      const target = event.target as Node;
      if (element.contains(target) || opener.contains(target)) return;
      // A control that receives the click keeps the focus it is about to take.
      close(element.contains(document.activeElement));
    };
    // Content that appears or wraps while the list is open changes its height
    // (for example the preference notice), so placement follows the size too.
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(place);
    observer?.observe(element);
    window.addEventListener("resize", place);
    document.addEventListener("pointerdown", dismiss);
    return () => {
      placement.current = undefined;
      observer?.disconnect();
      window.removeEventListener("resize", place);
      document.removeEventListener("pointerdown", dismiss);
    };
  }, [open]);
  // Re-place synchronously when the notice or an operation changes the height
  // of an already open list, so its alert and Reload action stay in view.
  useLayoutEffect(() => {
    if (open) placement.current?.();
  }, [open, navigation.snapshot.problem, navigation.operation, pinned]);
  const blocked =
    navigation.operation !== undefined || navigation.snapshot.problem !== null;
  const label = pinned ? copy("sidebar.unpinUsage") : copy("sidebar.pinUsage");
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="sidebar-rail-button sidebar-more-trigger"
        aria-label={copy("sidebar.more")}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-current={usageActive && !pinned ? "page" : undefined}
        onClick={() => setOpen((value) => !value)}
      >
        <Glyph name={MoreGlyph.More} />
        <span className="sidebar-rail-tooltip" aria-hidden="true">
          {copy("sidebar.more")}
        </span>
      </button>
      {open ? (
        <div
          ref={popup}
          popover="manual"
          role="dialog"
          aria-label={copy("sidebar.more")}
          className="sidebar-more-popover"
          onKeyDown={(event) => {
            if (event.key === "Tab") keyboardFocus.current = true;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              close(true);
              return;
            }
            const buttons = [
              ...(popup.current?.querySelectorAll<HTMLButtonElement>(
                "button",
              ) ?? []),
            ];
            const index = buttons.indexOf(
              document.activeElement as HTMLButtonElement,
            );
            if (index < 0) return;
            const step =
              event.key === "ArrowDown" || event.key === "ArrowRight"
                ? 1
                : event.key === "ArrowUp" || event.key === "ArrowLeft"
                  ? -1
                  : 0;
            if (step) {
              event.preventDefault();
              buttons[(index + step + buttons.length) % buttons.length].focus();
            } else if (event.key === "Home" || event.key === "End") {
              event.preventDefault();
              buttons[event.key === "Home" ? 0 : buttons.length - 1].focus();
            }
          }}
          onBlur={(event) => {
            const next = event.relatedTarget as Node | null;
            // Focus that leaves for another control dismisses the list without
            // taking focus back. Window blur and removed buttons (null) do not.
            // The opener counts only when a key moved focus to it (Shift+Tab
            // from the first row): a press on it blurs the list too, and its
            // own click then toggles the list.
            if (
              next &&
              !popup.current?.contains(next) &&
              (!trigger.current?.contains(next) || keyboardFocus.current)
            )
              setOpen(false);
          }}
        >
          <div className="sidebar-more-row">
            <button
              type="button"
              className="sidebar-more-item"
              aria-current={usageActive ? "page" : undefined}
              onClick={() => {
                close(true);
                openUsage();
              }}
            >
              <Glyph name={MoreGlyph.Usage} />
              <span>{copy("sidebar.usage_8d5982")}</span>
            </button>
            <button
              ref={pin}
              type="button"
              className="sidebar-more-pin"
              aria-label={label}
              title={label}
              aria-disabled={blocked ? true : undefined}
              onClick={() => {
                if (!blocked) navigation.select(!pinned);
              }}
            >
              <Glyph name={MoreGlyph.Pin} filled={pinned} />
            </button>
          </div>
          <NavigationPreferenceNotice
            beforeReload={() => pin.current?.focus()}
          />
        </div>
      ) : null}
    </>
  );
}
