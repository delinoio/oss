import { NoSSR, useI18n } from "@rspress/core/runtime";
import {
  DocFooter as BasicDocFooter,
  IconSearch,
  SearchPanel,
  SvgWrapper,
} from "@rspress/core/theme-original";
import "@rspress/core/dist/theme/components/Search/SearchButton.css";
import { useEffect, useState } from "react";

import "./accessibility.css";
import "./repository-footer.css";

function isHTMLElement(value: Element | null): value is HTMLElement {
  return value instanceof HTMLElement;
}

function setAttributeIfChanged(element: HTMLElement, name: string, value: string) {
  if (element.getAttribute(name) !== value) {
    element.setAttribute(name, value);
  }
}

function removeAttributeIfPresent(element: HTMLElement, name: string) {
  if (element.hasAttribute(name)) {
    element.removeAttribute(name);
  }
}

function getHashTargetId() {
  if (!window.location.hash) {
    return null;
  }

  try {
    return decodeURIComponent(window.location.hash.slice(1));
  } catch {
    return window.location.hash.slice(1);
  }
}

function getHeadingAnchorTopOffset() {
  const navHeight =
    document.querySelector<HTMLElement>(".rp-nav")?.getBoundingClientRect().height ?? 0;
  const menuHeight =
    document.querySelector<HTMLElement>(".rp-doc-layout__menu")?.getBoundingClientRect().height ?? 0;

  return navHeight + menuHeight + 12;
}

function getVisibleHeadingId() {
  const headings = Array.from(
    document.querySelectorAll<HTMLElement>(".rp-doc :where(.rp-toc-include)[id]"),
  );

  if (headings.length === 0) {
    return null;
  }

  const hashTargetId = getHashTargetId();
  const hashTarget = hashTargetId ? document.getElementById(hashTargetId) : null;
  if (hashTarget instanceof HTMLElement) {
    const rect = hashTarget.getBoundingClientRect();
    const topOffset = getHeadingAnchorTopOffset();
    if (rect.top >= 0 && rect.top <= Math.max(window.innerHeight * 0.45, topOffset + 80)) {
      return hashTarget.id;
    }
  }

  const activationLine = getHeadingAnchorTopOffset();
  let activeHeading = headings[0];
  for (const heading of headings) {
    if (heading.getBoundingClientRect().top <= activationLine) {
      activeHeading = heading;
      continue;
    }
    break;
  }

  return activeHeading.id;
}

function getTocLinkForHeading(headingId: string | null) {
  if (!headingId) {
    return null;
  }

  return (
    Array.from(document.querySelectorAll<HTMLAnchorElement>(".rp-outline__toc .rp-toc-item")).find(
      (link) => {
        try {
          return new URL(link.href, window.location.href).hash === `#${headingId}`;
        } catch {
          return link.getAttribute("href") === `#${headingId}`;
        }
      },
    ) ?? null
  );
}

function syncOutlineActiveHeading() {
  const activeLink = getTocLinkForHeading(getVisibleHeadingId());
  const activeLabel =
    activeLink?.querySelector<HTMLElement>(".rp-toc-item__text")?.textContent?.trim() ||
    activeLink?.getAttribute("title")?.trim() ||
    "";

  document.querySelectorAll<HTMLAnchorElement>(".rp-outline__toc .rp-toc-item").forEach((link) => {
    const isActive = link === activeLink;
    link.classList.toggle("rp-toc-item--active", isActive);
    if (isActive) {
      setAttributeIfChanged(link, "aria-current", "location");
    } else {
      removeAttributeIfPresent(link, "aria-current");
    }
  });

  const outlineButton = document.querySelector<HTMLElement>(".rp-sidebar-menu__right");
  const outlineButtonText = outlineButton?.querySelector<HTMLElement>(".rp-sidebar-menu__right__text");
  if (!outlineButton || !outlineButtonText) {
    return;
  }

  const nextLabel = activeLabel || "ON THIS PAGE";
  if (outlineButtonText.textContent !== nextLabel) {
    outlineButtonText.textContent = nextLabel;
  }
  outlineButtonText.classList.toggle("rp-doc", Boolean(activeLabel));

  const isOpen = document
    .querySelector(".rp-doc-layout__outline")
    ?.classList.contains("rp-doc-layout__outline--open");
  setAttributeIfChanged(
    outlineButton,
    "aria-label",
    `${isOpen ? "Close" : "Open"} page outline${activeLabel ? `, current section: ${activeLabel}` : ""}`,
  );
  setAttributeIfChanged(outlineButton, "aria-expanded", String(Boolean(isOpen)));
}

function scheduleOutlineActiveHeadingSync() {
  requestAnimationFrame(() => {
    syncOutlineActiveHeading();
  });
}

let wasMobileDrawerOpen = false;

function updateSidebarAccessibility(shouldRestoreFocus = false) {
  const trigger = document.querySelector(".rp-sidebar-menu__left");
  const sidebar = document.querySelector(".rp-doc-layout__sidebar");

  if (!isHTMLElement(trigger) || !isHTMLElement(sidebar)) {
    wasMobileDrawerOpen = false;
    return;
  }

  const isOpen = sidebar.classList.contains("rp-doc-layout__sidebar--open");
  const sidebarId = sidebar.id || "nodeup-docs-sidebar";

  if (!sidebar.id) {
    sidebar.id = sidebarId;
  }

  const isMobileDrawerControlVisible =
    trigger.getClientRects().length > 0 && getComputedStyle(trigger).visibility !== "hidden";
  const isMobileDrawerViewport = window.matchMedia("(max-width: 768px)").matches;
  const isMobileDrawer = isMobileDrawerControlVisible && isMobileDrawerViewport;
  const shouldMoveFocusFromSidebar =
    shouldRestoreFocus || (wasMobileDrawerOpen && sidebar.contains(document.activeElement));

  setAttributeIfChanged(
    trigger,
    "aria-label",
    isOpen ? "Close documentation navigation" : "Open documentation navigation",
  );
  setAttributeIfChanged(trigger, "aria-controls", sidebarId);
  setAttributeIfChanged(trigger, "aria-expanded", String(isMobileDrawer && isOpen));

  if (!isMobileDrawer) {
    removeAttributeIfPresent(sidebar, "aria-hidden");
    removeAttributeIfPresent(sidebar, "inert");
    wasMobileDrawerOpen = false;
    return;
  }

  if (isOpen) {
    removeAttributeIfPresent(sidebar, "aria-hidden");
    removeAttributeIfPresent(sidebar, "inert");
    wasMobileDrawerOpen = true;
  } else {
    setAttributeIfChanged(sidebar, "aria-hidden", "true");
    setAttributeIfChanged(sidebar, "inert", "");
    if (shouldMoveFocusFromSidebar) {
      trigger.focus();
    }
    wasMobileDrawerOpen = false;
  }
}

function updateGeneratedControlAccessibility() {
  document.querySelectorAll<HTMLAnchorElement>("a.rp-header-anchor").forEach((anchor) => {
    if (anchor.tabIndex !== -1) {
      anchor.tabIndex = -1;
    }
    setAttributeIfChanged(anchor, "aria-hidden", "true");
  });

  const isSiteNavigationOpen = Boolean(document.querySelector(".rp-nav-screen--open"));
  document.querySelectorAll<HTMLElement>(".rp-nav-hamburger__sm").forEach((button) => {
    const isOpen = button.classList.contains("rp-nav-hamburger--active") || isSiteNavigationOpen;
    setAttributeIfChanged(button, "aria-label", isOpen ? "Close site navigation" : "Open site navigation");
    setAttributeIfChanged(button, "aria-expanded", String(isOpen));
  });

  document.querySelectorAll<HTMLElement>(".rp-nav-hamburger__md").forEach((button) => {
    setAttributeIfChanged(button, "aria-label", "Open site controls");
  });

  updateSidebarAccessibility();
  syncOutlineActiveHeading();
}

function NodeupDocsAccessibility() {
  useEffect(() => {
    updateGeneratedControlAccessibility();

    const observer = new MutationObserver(() => {
      updateGeneratedControlAccessibility();
    });

    observer.observe(document.body, {
      attributes: true,
      childList: true,
      subtree: true,
      attributeFilter: ["class", "href", "id"],
    });

    function onResize() {
      updateGeneratedControlAccessibility();
    }

    function onScroll() {
      scheduleOutlineActiveHeadingSync();
    }

    function onHashChange() {
      scheduleOutlineActiveHeadingSync();
      window.setTimeout(syncOutlineActiveHeading, 120);
      window.setTimeout(syncOutlineActiveHeading, 320);
    }

    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") {
        return;
      }

      const sidebar = document.querySelector(".rp-doc-layout__sidebar");
      const mask = document.querySelector(".rp-sidebar-menu__mask");
      if (isHTMLElement(sidebar) && sidebar.classList.contains("rp-doc-layout__sidebar--open")) {
        event.stopPropagation();
        if (isHTMLElement(mask)) {
          mask.click();
        }
        requestAnimationFrame(() => updateSidebarAccessibility(true));
      }
    }

    window.addEventListener("resize", onResize);
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("hashchange", onHashChange);
    window.addEventListener("popstate", onHashChange);
    document.addEventListener("keydown", onKeyDown);
    scheduleOutlineActiveHeadingSync();

    return () => {
      window.removeEventListener("resize", onResize);
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("hashchange", onHashChange);
      window.removeEventListener("popstate", onHashChange);
      document.removeEventListener("keydown", onKeyDown);
      observer.disconnect();
    };
  }, []);

  return null;
}

function Search() {
  const [focused, setFocused] = useState(false);
  const [metaKey, setMetaKey] = useState<string | null>(null);
  const t = useI18n();

  useEffect(() => {
    setMetaKey(/(Mac|iPhone|iPod|iPad)/i.test(navigator.platform) ? "⌘" : "Ctrl");
  }, []);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key.toLowerCase() !== "k" || (!event.ctrlKey && !event.metaKey)) {
        return;
      }

      event.preventDefault();
      setFocused(true);
    }

    document.addEventListener("keydown", onKeyDown);

    return () => {
      document.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  return (
    <>
      <button
        className="rp-search-button"
        onClick={() => setFocused(true)}
        type="button"
      >
        <div className="rp-search-button__content">
          <SvgWrapper className="rp-search-button__icon" icon={IconSearch} />
          <span className="rp-search-button__word">{t("searchPlaceholderText")}</span>
        </div>
        <div
          className="rp-search-button__hotkey"
          style={{
            opacity: metaKey ? 1 : 0,
          }}
        >
          <span>{metaKey}</span>
          <span>K</span>
        </div>
      </button>
      <button
        aria-label="Search documentation"
        className="rp-search-button--mobile"
        onClick={() => setFocused(true)}
        type="button"
      >
        <SvgWrapper icon={IconSearch} />
      </button>
      <NoSSR>
        <SearchPanel focused={focused} setFocused={setFocused} />
      </NoSSR>
    </>
  );
}

function DocFooter() {
  return (
    <>
      <NodeupDocsAccessibility />
      <BasicDocFooter />
      <footer className="delino-repository-footer">
        <a
          className="delino-repository-footer__link"
          href="https://github.com/delinoio/oss"
          rel="noreferrer"
          target="_blank"
        >
          Delino OSS repository
        </a>
      </footer>
    </>
  );
}

export { DocFooter, Search };
export * from "@rspress/core/theme-original";
