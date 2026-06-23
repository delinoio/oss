import { useEffect } from "react";
import {
  DocFooter as BasicDocFooter,
  Layout as BasicLayout,
  type LayoutProps,
} from "@rspress/core/theme-original";

import "./accessibility.css";
import "./repository-footer.css";

const SEARCH_LABEL = "Search documentation";
const MOBILE_SEARCH_LABEL = "Open documentation search";
const REPOSITORY_LABEL = "Open Delino OSS repository on GitHub";
const SIDEBAR_DRAWER_QUERY = "(max-width: 768px)";
const OUTLINE_DRAWER_QUERY = "(max-width: 1279px)";
const NAVIGATION_DRAWER_QUERY = "(max-width: 768px)";

function setButtonName(element: Element | null, label: string) {
  if (!(element instanceof HTMLElement)) {
    return;
  }

  if (element.getAttribute("aria-label") !== label) {
    element.setAttribute("aria-label", label);
  }
  if (element.getAttribute("title") !== label) {
    element.setAttribute("title", label);
  }
}

function setTextContent(element: HTMLElement, text: string) {
  if (element.textContent !== text) {
    element.textContent = text;
  }
}

function setLocalHref(anchor: HTMLAnchorElement, href: string) {
  if (anchor.getAttribute("href") !== href) {
    anchor.setAttribute("href", href);
  }
}

function setInteractiveDiv(element: Element | null, label: string) {
  if (!(element instanceof HTMLElement)) {
    return;
  }

  if (element.getAttribute("role") !== "button") {
    element.setAttribute("role", "button");
  }
  if (element.getAttribute("tabindex") !== "0") {
    element.setAttribute("tabindex", "0");
  }
  setButtonName(element, label);
}

function isDrawerOpen(
  drawer: Element | null,
  openClassName: string,
): boolean {
  return drawer?.classList.contains(openClassName) ?? false;
}

function getHeadingText(heading: HTMLHeadingElement) {
  const clone = heading.cloneNode(true);

  if (!(clone instanceof HTMLElement)) {
    return heading.textContent?.trim() ?? "section";
  }

  for (const anchor of clone.querySelectorAll(".rp-header-anchor")) {
    anchor.remove();
  }

  return clone.textContent?.replace(/\s+/g, " ").trim() || "section";
}

function setDrawerVisibility(
  drawer: Element | null,
  isDrawerBreakpoint: boolean,
  isOpen: boolean,
) {
  if (!(drawer instanceof HTMLElement)) {
    return;
  }

  const shouldHideFromFocus = isDrawerBreakpoint && !isOpen;

  if (drawer.inert !== shouldHideFromFocus) {
    drawer.inert = shouldHideFromFocus;
  }

  const ariaHidden = shouldHideFromFocus ? "true" : "false";
  if (drawer.getAttribute("aria-hidden") !== ariaHidden) {
    drawer.setAttribute("aria-hidden", ariaHidden);
  }
}

function setMobileDrawerState() {
  const sidebar = document.querySelector(".rp-doc-layout__sidebar");
  const outline = document.querySelector(".rp-doc-layout__outline");

  setDrawerVisibility(
    sidebar,
    window.matchMedia(SIDEBAR_DRAWER_QUERY).matches,
    isDrawerOpen(sidebar, "rp-doc-layout__sidebar--open"),
  );
  setDrawerVisibility(
    outline,
    window.matchMedia(OUTLINE_DRAWER_QUERY).matches,
    isDrawerOpen(outline, "rp-doc-layout__outline--open"),
  );
}

function syncHeadingPermalinks() {
  for (const heading of document.querySelectorAll<HTMLHeadingElement>(
    ".rp-doc :is(h1, h2, h3, h4, h5, h6)[id]",
  )) {
    const headingId = heading.id;
    const headingText = getHeadingText(heading);
    const anchor = heading.querySelector<HTMLAnchorElement>("a.rp-header-anchor");

    if (!anchor) {
      continue;
    }

    const headingHref = `#${headingId}`;
    const keyboardLabel = `Permalink to ${headingText}`;

    setLocalHref(anchor, headingHref);
    setTextContent(anchor, "#");
    if (anchor.getAttribute("aria-hidden") !== "true") {
      anchor.setAttribute("aria-hidden", "true");
    }
    if (anchor.tabIndex !== -1) {
      anchor.tabIndex = -1;
    }

    let keyboardAnchor = heading.nextElementSibling;
    if (
      !(keyboardAnchor instanceof HTMLAnchorElement) ||
      !keyboardAnchor.classList.contains("delino-heading-permalink-keyboard")
    ) {
      keyboardAnchor = document.createElement("a");
      keyboardAnchor.className = "delino-heading-permalink-keyboard";
      heading.after(keyboardAnchor);
    }

    setLocalHref(keyboardAnchor, headingHref);
    setTextContent(keyboardAnchor, keyboardLabel);
    setButtonName(keyboardAnchor, keyboardLabel);
  }
}

function syncAccessibleControls() {
  const searchButton = document.querySelector(".rp-search-button");
  if (
    searchButton instanceof HTMLButtonElement &&
    searchButton.getAttribute("type") !== "button"
  ) {
    searchButton.type = "button";
  }
  setButtonName(searchButton, SEARCH_LABEL);
  setInteractiveDiv(
    document.querySelector(".rp-search-button--mobile"),
    MOBILE_SEARCH_LABEL,
  );

  for (const input of document.querySelectorAll(".rp-search-panel__input")) {
    if (input.getAttribute("aria-label") !== SEARCH_LABEL) {
      input.setAttribute("aria-label", SEARCH_LABEL);
    }
  }

  for (const button of document.querySelectorAll(".rp-code-copy-button")) {
    setButtonName(button, "Copy code block");
  }

  const nextThemeLabel = document.documentElement.classList.contains("rp-dark")
    ? "Switch to light theme"
    : "Switch to dark theme";
  for (const themeSwitch of document.querySelectorAll(
    ".rp-switch-appearance",
  )) {
    setInteractiveDiv(themeSwitch, nextThemeLabel);
  }

  for (const link of document.querySelectorAll<HTMLAnchorElement>(
    '.rp-social-links__item[href="https://github.com/delinoio/oss"]',
  )) {
    setButtonName(link, REPOSITORY_LABEL);
  }

  const sidebar = document.querySelector(".rp-doc-layout__sidebar");
  const outline = document.querySelector(".rp-doc-layout__outline");
  setButtonName(
    document.querySelector(".rp-sidebar-menu__left"),
    isDrawerOpen(sidebar, "rp-doc-layout__sidebar--open")
      ? "Close documentation pages"
      : "Open documentation pages",
  );
  const sidebarMenuText = document.querySelector(".rp-sidebar-menu__left span");
  if (
    sidebarMenuText instanceof HTMLElement &&
    sidebarMenuText.textContent !== "Docs"
  ) {
    sidebarMenuText.textContent = "Docs";
  }
  setButtonName(
    document.querySelector(".rp-sidebar-menu__right"),
    isDrawerOpen(outline, "rp-doc-layout__outline--open")
      ? "Close page outline"
      : "Open page outline",
  );

  for (const button of document.querySelectorAll(".rp-nav-hamburger")) {
    const isOpen = button.classList.contains("rp-nav-hamburger--active");
    const isNavigationDrawer = window.matchMedia(NAVIGATION_DRAWER_QUERY).matches;
    setButtonName(
      button,
      isNavigationDrawer
        ? isOpen
          ? "Close site navigation"
          : "Open site navigation"
        : "Open site controls",
    );
    button.setAttribute("aria-expanded", String(isNavigationDrawer && isOpen));
  }

  syncHeadingPermalinks();
  setMobileDrawerState();
}

function closeMobileDrawers() {
  const navScreen = document.querySelector<HTMLElement>(".rp-nav-screen--open");
  if (navScreen) {
    navScreen.click();
    document.querySelector<HTMLElement>(".rp-nav-hamburger")?.focus();
  }

  const sidebar = document.querySelector(".rp-doc-layout__sidebar");
  const outline = document.querySelector(".rp-doc-layout__outline");
  const isSidebarOpen = isDrawerOpen(sidebar, "rp-doc-layout__sidebar--open");
  const isOutlineOpen = isDrawerOpen(outline, "rp-doc-layout__outline--open");

  if (!isSidebarOpen && !isOutlineOpen) {
    return;
  }

  document.body.dispatchEvent(
    new MouseEvent("mousedown", { bubbles: true, cancelable: true }),
  );
  document
    .querySelector<HTMLElement>(
      isOutlineOpen ? ".rp-sidebar-menu__right" : ".rp-sidebar-menu__left",
    )
    ?.focus();
}

function AccessibilitySync() {
  useEffect(() => {
    syncAccessibleControls();

    const observer = new MutationObserver(syncAccessibleControls);
    observer.observe(document.body, {
      attributes: true,
      childList: true,
      subtree: true,
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });

    const handleKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      if (event.key === "Enter" || event.key === " ") {
        if (
          target instanceof HTMLElement &&
          (target.matches(".rp-search-button--mobile") ||
            target.matches(".rp-switch-appearance"))
        ) {
          event.preventDefault();
          target.click();
        }
      }

      if (event.key === "Escape") {
        closeMobileDrawers();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    window.addEventListener("resize", setMobileDrawerState);

    return () => {
      observer.disconnect();
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("resize", setMobileDrawerState);
    };
  }, []);

  return null;
}

function MainContentAnchor() {
  return (
    <span
      className="delino-main-content-anchor"
      id="main-content"
      tabIndex={-1}
    />
  );
}

function SkipToContent() {
  return (
    <a className="delino-skip-link" href="#main-content">
      Skip to content
    </a>
  );
}

function Layout(props: LayoutProps) {
  return (
    <BasicLayout
      {...props}
      top={
        <>
          <SkipToContent />
          {props.top}
        </>
      }
      beforeDocContent={
        <>
          <MainContentAnchor />
          {props.beforeDocContent}
        </>
      }
      bottom={
        <>
          {props.bottom}
          <AccessibilitySync />
        </>
      }
    />
  );
}

function DocFooter() {
  return (
    <>
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

export { Layout };
export { DocFooter };
export * from "@rspress/core/theme-original";
