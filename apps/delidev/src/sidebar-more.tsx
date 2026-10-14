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
    element.showPopover?.();
    place();
    element.querySelector<HTMLButtonElement>("button")?.focus();
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Node;
      if (element.contains(target) || opener.contains(target)) return;
      // A control that receives the click keeps the focus it is about to take.
      close(element.contains(document.activeElement));
    };
    window.addEventListener("resize", place);
    document.addEventListener("pointerdown", dismiss);
    return () => {
      window.removeEventListener("resize", place);
      document.removeEventListener("pointerdown", dismiss);
    };
  }, [open]);
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
            if (
              next &&
              !popup.current?.contains(next) &&
              !trigger.current?.contains(next)
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
