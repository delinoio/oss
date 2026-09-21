// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import {
  DOCUMENTATION_SITES,
  DocumentationSiteId,
  DocsSiteSwitcher,
  getDocumentationSiteForPathname,
} from "../src/index";

afterEach(cleanup);

function renderSwitcher(currentSite = DocumentationSiteId.Runmoor) {
  return render(<DocsSiteSwitcher currentSite={currentSite} />);
}

describe("DocsSiteSwitcher", () => {
  it("renders all sites with the current site marked as the page", () => {
    renderSwitcher();

    const trigger = screen.getByRole("button", { name: "Runmoor" });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(trigger.getAttribute("aria-controls")).toMatch(/^delino-docs-site-menu-/);

    fireEvent.click(trigger);

    const menu = screen.getByRole("menu");
    const items = screen.getAllByRole("menuitem");
    expect(items).toHaveLength(DOCUMENTATION_SITES.length);
    expect(items.map((item) => item.textContent?.trim())).toEqual([
      "Delino OSS",
      "Runmoor✓",
      "Nodeup",
      "binpm",
      "async-commit-hook",
    ]);
    expect(menu.getAttribute("id")).toBe(trigger.getAttribute("aria-controls"));
    expect(items[1]?.getAttribute("aria-current")).toBe("page");
    expect(items.map((item) => item.getAttribute("href"))).toEqual([
      "/",
      "/runmoor/",
      "/nodeup/",
      "/binpm/",
      "/async-commit-hook/",
    ]);
  });

  it("supports roving keyboard navigation and restores focus on Escape", () => {
    renderSwitcher();

    const trigger = screen.getByRole("button", { name: "Runmoor" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown" });

    const items = screen.getAllByRole("menuitem");
    expect(document.activeElement).toBe(items[0]);

    fireEvent.keyDown(items[0], { key: "End" });
    expect(document.activeElement).toBe(items[items.length - 1]);

    fireEvent.keyDown(items[items.length - 1], { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[0]);

    fireEvent.keyDown(items[0], { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("closes when the user clicks outside the menu", () => {
    renderSwitcher();

    fireEvent.click(screen.getByRole("button", { name: "Runmoor" }));
    expect(screen.getByRole("menu")).toBeTruthy();

    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("maps nested integrated paths to the correct site", () => {
    expect(getDocumentationSiteForPathname("/")).toBe(DocumentationSiteId.PublicDocs);
    expect(getDocumentationSiteForPathname("/nodeup/reference")).toBe(
      DocumentationSiteId.Nodeup,
    );
    expect(getDocumentationSiteForPathname("/async-commit-hook/validation/")).toBe(
      DocumentationSiteId.AsyncCommitHook,
    );
    expect(getDocumentationSiteForPathname("/unrelated-route")).toBe(
      DocumentationSiteId.PublicDocs,
    );
  });
});
