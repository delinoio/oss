// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://127.0.0.1:46302/"}

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
      "clibox",
      "pnport",
      "React Forge",
    ]);
    expect(menu.getAttribute("id")).toBe(trigger.getAttribute("aria-controls"));
    expect(items[1]?.getAttribute("aria-current")).toBe("page");
    expect(items.map((item) => item.getAttribute("href"))).toEqual([
      "/",
      "/runmoor/",
      "/nodeup/",
      "/binpm/",
      "/async-commit-hook/",
      "/clibox/",
      "/pnport/",
      "/react-forge/",
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

  it("moves focus into the current item when opened by trigger activation", () => {
    renderSwitcher();

    const trigger = screen.getByRole("button", { name: "Runmoor" });
    fireEvent.keyDown(trigger, { key: "Enter" });
    fireEvent.click(trigger);

    expect(document.activeElement).toBe(screen.getAllByRole("menuitem")[1]);
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

  it.each(["/clibox", "/clibox/", "/clibox/configuration", "/clibox/wait/"])(
    "selects clibox for %s",
    (pathname) => {
      renderSwitcher(getDocumentationSiteForPathname(pathname));
      fireEvent.click(screen.getByRole("button", { name: "clibox" }));
      const selected = screen.getByRole("menuitem", { name: "clibox" });
      expect(selected.getAttribute("aria-current")).toBe("page");
      expect(document.activeElement).toBe(selected);
    },
  );

  it.each(["/clibox-extra", "/cliboxish/wait"])(
    "does not select clibox for unrelated path %s",
    (pathname) => expect(getDocumentationSiteForPathname(pathname)).toBe(DocumentationSiteId.PublicDocs),
  );

  it.each(["/pnport", "/pnport/", "/pnport/editors", "/pnport/diagnostics/"])(
    "selects pnport for %s",
    (pathname) => {
      renderSwitcher(getDocumentationSiteForPathname(pathname));
      fireEvent.click(screen.getByRole("button", { name: "pnport" }));
      const selected = screen.getByRole("menuitem", { name: "pnport" });
      expect(selected.getAttribute("aria-current")).toBe("page");
      expect(document.activeElement).toBe(selected);
    },
  );

  it.each(["/pnport-extra", "/pnportish/editors"])(
    "does not select pnport for unrelated path %s",
    (pathname) => expect(getDocumentationSiteForPathname(pathname)).toBe(DocumentationSiteId.PublicDocs),
  );

  it.each(["/react-forge", "/react-forge/", "/react-forge/figma", "/react-forge/mcp/"])(
    "selects React Forge for %s",
    (pathname) => {
      renderSwitcher(getDocumentationSiteForPathname(pathname));
      fireEvent.click(screen.getByRole("button", { name: "React Forge" }));
      const selected = screen.getByRole("menuitem", { name: "React Forge" });
      expect(selected.getAttribute("aria-current")).toBe("page");
      expect(document.activeElement).toBe(selected);
    },
  );

  it.each(["/react-forge-extra", "/react-forgeish/mcp"])(
    "does not select React Forge for unrelated path %s",
    (pathname) => expect(getDocumentationSiteForPathname(pathname)).toBe(DocumentationSiteId.PublicDocs),
  );

  it("retains same-origin destinations on the consolidated development server", () => {
    expect(window.location.port).toBe("46302");
    renderSwitcher(DocumentationSiteId.Clibox);
    fireEvent.click(screen.getByRole("button", { name: "clibox" }));
    for (const [index, item] of screen.getAllByRole("menuitem").entries()) {
      const destination = new URL(item.getAttribute("href")!, window.location.href);
      expect(destination.origin).toBe(window.location.origin);
      expect(destination.pathname).toBe(DOCUMENTATION_SITES[index].href);
    }
  });

  it("reaches React Forge at the end and restores its trigger on Escape", () => {
    renderSwitcher(DocumentationSiteId.ReactForge);
    const trigger = screen.getByRole("button", { name: "React Forge" });
    fireEvent.keyDown(trigger, { key: "ArrowUp" });
    const item = screen.getByRole("menuitem", { name: "React Forge" });
    expect(document.activeElement).toBe(item);
    fireEvent.keyDown(item, { key: "Home" });
    expect(document.activeElement).toBe(screen.getAllByRole("menuitem")[0]);
    fireEvent.keyDown(document.activeElement!, { key: "End" });
    expect(document.activeElement).toBe(item);
    fireEvent.keyDown(item, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
