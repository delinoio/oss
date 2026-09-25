import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";

import "./styles.css";

export enum DocumentationSiteId {
  PublicDocs = "public-docs",
  Runmoor = "runmoor",
  Nodeup = "nodeup",
  Binpm = "binpm",
  AsyncCommitHook = "async-commit-hook",
  Clibox = "clibox",
  Pnport = "pnport",
  ReactForge = "react-forge",
}

export interface DocumentationSite {
  readonly id: DocumentationSiteId;
  readonly label: string;
  readonly href: string;
}

export const DOCUMENTATION_SITES = [
  {
    id: DocumentationSiteId.PublicDocs,
    label: "Delino OSS",
    href: "/",
  },
  {
    id: DocumentationSiteId.Runmoor,
    label: "Runmoor",
    href: "/runmoor/",
  },
  {
    id: DocumentationSiteId.Nodeup,
    label: "Nodeup",
    href: "/nodeup/",
  },
  {
    id: DocumentationSiteId.Binpm,
    label: "binpm",
    href: "/binpm/",
  },
  {
    id: DocumentationSiteId.AsyncCommitHook,
    label: "async-commit-hook",
    href: "/async-commit-hook/",
  },
  {
    id: DocumentationSiteId.Clibox,
    label: "clibox",
    href: "/clibox/",
  },
  {
    id: DocumentationSiteId.Pnport,
    label: "pnport",
    href: "/pnport/",
  },
  {
    id: DocumentationSiteId.ReactForge,
    label: "React Forge",
    href: "/react-forge/",
  },
] as const satisfies readonly DocumentationSite[];

const PUBLIC_DOCS_SITE = DOCUMENTATION_SITES[0];

function normalizePathname(pathname: string) {
  if (pathname === "/") {
    return pathname;
  }

  const withLeadingSlash = pathname.startsWith("/") ? pathname : `/${pathname}`;
  return withLeadingSlash.replace(/\/+$/, "");
}

export function getDocumentationSiteForPathname(pathname: string) {
  const normalizedPathname = normalizePathname(pathname);
  return (
    DOCUMENTATION_SITES.find((site) => {
      if (site.id === DocumentationSiteId.PublicDocs) {
        return normalizedPathname === "/";
      }

      const sitePath = site.href.replace(/\/$/, "");
      return normalizedPathname === sitePath || normalizedPathname.startsWith(`${sitePath}/`);
    })?.id ?? PUBLIC_DOCS_SITE.id
  );
}

export interface DocsSiteSwitcherProps {
  currentSite: DocumentationSiteId;
  className?: string;
}

function getNextIndex(index: number, offset: number, count: number) {
  return (index + offset + count) % count;
}

export function DocsSiteSwitcher({ currentSite, className }: DocsSiteSwitcherProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuItemRefs = useRef<Array<HTMLAnchorElement | null>>([]);
  const [isOpen, setIsOpen] = useState(false);
  const [focusedIndex, setFocusedIndex] = useState<number | null>(null);
  const menuId = `delino-docs-site-menu-${useId().replace(/:/g, "")}`;
  const currentSiteDefinition =
    DOCUMENTATION_SITES.find((site) => site.id === currentSite) ?? PUBLIC_DOCS_SITE;
  const currentSiteIndex = Math.max(
    0,
    DOCUMENTATION_SITES.findIndex((site) => site.id === currentSiteDefinition.id),
  );

  const closeMenu = useCallback((restoreFocus: boolean) => {
    setIsOpen(false);
    setFocusedIndex(null);

    if (restoreFocus) {
      triggerRef.current?.focus();
    }
  }, []);

  const openMenu = useCallback((index: number | null = null) => {
    setIsOpen(true);
    setFocusedIndex(index);
  }, []);

  useEffect(() => {
    if (!isOpen || focusedIndex === null) {
      return;
    }

    menuItemRefs.current[focusedIndex]?.focus();
  }, [focusedIndex, isOpen]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    function handleOutsidePointerDown(event: MouseEvent) {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        closeMenu(false);
      }
    }

    document.addEventListener("mousedown", handleOutsidePointerDown);
    return () => {
      document.removeEventListener("mousedown", handleOutsidePointerDown);
    };
  }, [closeMenu, isOpen]);

  function moveFocus(index: number) {
    setFocusedIndex(index);
  }

  function handleTriggerKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
    if (event.key === "Escape" && isOpen) {
      event.preventDefault();
      closeMenu(true);
      return;
    }

    if (event.key === "ArrowDown") {
      event.preventDefault();
      openMenu(0);
      return;
    }

    if (event.key === "ArrowUp") {
      event.preventDefault();
      openMenu(DOCUMENTATION_SITES.length - 1);
      return;
    }

    if (event.key === "Home") {
      event.preventDefault();
      openMenu(0);
      return;
    }

    if (event.key === "End") {
      event.preventDefault();
      openMenu(DOCUMENTATION_SITES.length - 1);
    }
  }

  function handleMenuItemKeyDown(
    event: ReactKeyboardEvent<HTMLAnchorElement>,
    index: number,
  ) {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        moveFocus(getNextIndex(index, 1, DOCUMENTATION_SITES.length));
        break;
      case "ArrowUp":
        event.preventDefault();
        moveFocus(getNextIndex(index, -1, DOCUMENTATION_SITES.length));
        break;
      case "Home":
        event.preventDefault();
        moveFocus(0);
        break;
      case "End":
        event.preventDefault();
        moveFocus(DOCUMENTATION_SITES.length - 1);
        break;
      case "Escape":
        event.preventDefault();
        closeMenu(true);
        break;
      case "Tab":
        closeMenu(false);
        break;
      default:
        break;
    }
  }

  const rootClassName = ["delino-docs-site-switcher", className].filter(Boolean).join(" ");

  return (
    <div className={rootClassName} ref={rootRef}>
      <button
        aria-controls={menuId}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        className="delino-docs-site-switcher__trigger"
        onClick={() => (isOpen ? closeMenu(true) : openMenu(currentSiteIndex))}
        onKeyDown={handleTriggerKeyDown}
        ref={triggerRef}
        type="button"
      >
        <span className="delino-docs-site-switcher__current">
          {currentSiteDefinition.label}
        </span>
        <span aria-hidden="true" className="delino-docs-site-switcher__chevron">
          ▾
        </span>
      </button>

      <div
        aria-label="Choose documentation site"
        className="delino-docs-site-switcher__menu"
        hidden={!isOpen}
        id={menuId}
        role="menu"
      >
        {DOCUMENTATION_SITES.map((site, index) => {
          const isCurrent = site.id === currentSiteDefinition.id;

          return (
            <a
              aria-current={isCurrent ? "page" : undefined}
              className="delino-docs-site-switcher__item"
              href={site.href}
              key={site.id}
              onClick={() => closeMenu(false)}
              onKeyDown={(event) => handleMenuItemKeyDown(event, index)}
              ref={(element) => {
                menuItemRefs.current[index] = element;
              }}
              role="menuitem"
              tabIndex={focusedIndex === index ? 0 : -1}
            >
              <span>{site.label}</span>
              {isCurrent ? (
                <span aria-hidden="true" className="delino-docs-site-switcher__check">
                  ✓
                </span>
              ) : null}
            </a>
          );
        })}
      </div>
    </div>
  );
}
