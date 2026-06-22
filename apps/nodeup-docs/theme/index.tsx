import { NoSSR, useI18n } from "@rspress/core/runtime";
import {
  DocFooter as BasicDocFooter,
  IconSearch,
  SearchPanel,
  SvgWrapper,
} from "@rspress/core/theme-original";
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

function updateSidebarAccessibility(shouldRestoreFocus = false) {
  const trigger = document.querySelector(".rp-sidebar-menu__left");
  const sidebar = document.querySelector(".rp-doc-layout__sidebar");

  if (!isHTMLElement(trigger) || !isHTMLElement(sidebar)) {
    return;
  }

  const isOpen = sidebar.classList.contains("rp-doc-layout__sidebar--open");
  const sidebarId = sidebar.id || "nodeup-docs-sidebar";

  if (!sidebar.id) {
    sidebar.id = sidebarId;
  }
  setAttributeIfChanged(
    trigger,
    "aria-label",
    isOpen ? "Close documentation navigation" : "Open documentation navigation",
  );
  setAttributeIfChanged(trigger, "aria-controls", sidebarId);
  setAttributeIfChanged(trigger, "aria-expanded", String(isOpen));

  if (isOpen) {
    removeAttributeIfPresent(sidebar, "aria-hidden");
    removeAttributeIfPresent(sidebar, "inert");
  } else {
    setAttributeIfChanged(sidebar, "aria-hidden", "true");
    setAttributeIfChanged(sidebar, "inert", "");
    if (shouldRestoreFocus) {
      trigger.focus();
    }
  }
}

function updateGeneratedControlAccessibility() {
  document.querySelectorAll<HTMLAnchorElement>("a.rp-header-anchor").forEach((anchor) => {
    if (anchor.tabIndex !== -1) {
      anchor.tabIndex = -1;
    }
    setAttributeIfChanged(anchor, "aria-hidden", "true");
  });

  document.querySelectorAll<HTMLElement>(".rp-nav-hamburger__sm").forEach((button) => {
    setAttributeIfChanged(button, "aria-label", "Open site navigation");
  });

  document.querySelectorAll<HTMLElement>(".rp-nav-hamburger__md").forEach((button) => {
    setAttributeIfChanged(button, "aria-label", "Open site controls");
  });

  updateSidebarAccessibility();
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

    document.addEventListener("keydown", onKeyDown);

    return () => {
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
