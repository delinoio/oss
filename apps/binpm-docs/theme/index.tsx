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

function syncAccessibleControls() {
  const searchButton = document.querySelector(".rp-search-button");
  if (searchButton instanceof HTMLButtonElement) {
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
  setButtonName(document.querySelector(".rp-sidebar-menu__left"), "Open menu");
  setButtonName(
    document.querySelector(".rp-sidebar-menu__right"),
    isDrawerOpen(outline, "rp-doc-layout__outline--open")
      ? "Close page outline"
      : "Open page outline",
  );

  for (const button of document.querySelectorAll(".rp-nav-hamburger")) {
    const isOpen = button.classList.contains("rp-nav-hamburger--active");
    setButtonName(
      button,
      isOpen ? "Close navigation menu" : "Open navigation menu",
    );
  }

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
