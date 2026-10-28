// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, rs } from "@rstest/core";
import { createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OrganizationService,
  OperatorService,
  Role,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { App } from "../src/app";
import {
  pageHref,
  pagePaths,
  operatorPaths,
  organizationPages,
  resolveRoute,
} from "../src/routes";

const url = (path: string) => new URL(path, "http://localhost");
describe("canonical console routes", () => {
  it.each(Object.entries(pagePaths))(
    "resolves the %s deep link",
    (page, path) => {
      const route = resolveRoute(url(path));
      expect(route?.page).toBe(page);
      expect(route?.url.pathname).toBe(path);
    },
  );
  it.each([
    ["/keys?organization=", "/keys", "keys"],
    ["/device?user_code=ABCD", "/device?user_code=ABCD", "device"],
    [
      "/ownership?organization_id=studio&transfer_id=transfer",
      "/ownership?organization_id=studio&transfer_id=transfer",
      "ownership",
    ],
    [
      "/sign-in?error=missing_email&return_to=%2Fkeys",
      "/sign-in?error=missing_email&return_to=%2Fkeys",
      "sign-in",
    ],
  ])(
    "normalizes %s without losing flow parameters",
    (source, canonical, page) => {
      const route = resolveRoute(url(source));
      expect(route?.page).toBe(page);
      expect(route!.url.pathname + route!.url.search).toBe(canonical);
    },
  );
  it.each([
    "/",
    "/?organization=&page=keys",
    "/organizations",
    "/connect-client",
    "/invitations/accept?token=secret",
    "/instance",
    "/instance/usage",
    "/providers?page=keys",
    "/keys?org=studio",
    "/keys?organization_id=studio",
    "/device?page=keys",
    "/keys?page=",
    "/keys?org=",
    "/keys?organization_id=",
  ])("rejects the retired route %s", (path) => {
    expect(resolveRoute(url(path))).toBeUndefined();
  });
  it("encodes organization scope in absolute navigation links", () => {
    expect(pageHref("keys", "studio & other")).toBe(
      "/organizations/studio%20%26%20other/keys",
    );
    expect(pageHref("account")).toBe("/account");
    expect(pageHref("keys", "")).toBe("/keys");
  });
  it.each(organizationPages)("resolves scoped %s deep links", (page) => {
    const href = pageHref(page, "01a17e38-0f0f-78f5-b8c5-f8cdb4960299");
    const route = resolveRoute(url(href));
    expect(route?.page).toBe(page);
    expect(route?.organization).toBe("01a17e38-0f0f-78f5-b8c5-f8cdb4960299");
    expect(route?.url.pathname).toBe(href);
  });
  it("migrates organization bookmarks while preserving other URL state", () => {
    const route = resolveRoute(
      url("/keys?organization=studio&filter=active#main"),
    );
    expect(route?.organization).toBe("studio");
    expect(route!.url.pathname + route!.url.search + route!.url.hash).toBe(
      "/organizations/studio/keys?filter=active#main",
    );
  });
  it.each([
    "/organizations//keys",
    "/organizations/studio/missing",
    "/organizations/studio/instance/health",
    "/organizations/studio/device",
    "/organizations/studio/onboarding",
    "/organizations/studio/keys/extra",
    "/organizations/studio/keys?organization=other",
    "/organizations/studio/keys?org=other",
    "/organizations/studio/keys?organization_id=other",
    "/organizations/bad%2Fid/keys",
    "/organizations/bad%5Cid/keys",
    "/organizations/%00/keys",
    "/organizations/%ZZ/keys",
  ])("rejects invalid scoped paths %s", (path) => {
    expect(resolveRoute(url(path))).toBeUndefined();
  });
  it("rejects unknown paths instead of showing overview", () => {
    expect(resolveRoute(url("/missing?page=keys"))).toBeUndefined();
    window.history.replaceState(null, "", "/missing");
    render(<App />);
    expect(
      screen.getByRole("heading", { name: "Page not found" }),
    ).toBeTruthy();
  });
  it("renders the page-not-found recovery surface without echoing the unknown URL", () => {
    const unknown =
      "/missing?organization=studio&org=other&organization_id=third&page=keys&token=fictional&user_code=ABCD&transfer_id=t1&error=e&return_to=https%3A%2F%2Fexample.test#fictional";
    window.history.replaceState(null, "", unknown);
    const replaceState = rs.spyOn(window.history, "replaceState");
    const { container } = render(<App />);
    const main = screen.getByRole("main", { name: "Page not found" });
    expect(screen.getAllByRole("heading")).toHaveLength(1);
    expect(
      screen.getByRole("heading", { level: 1, name: "Page not found" }),
    ).toBeTruthy();
    expect(within(main).getByText("PAGE UNAVAILABLE").tagName).toBe("P");
    expect(
      within(main).getByText(
        "We couldn’t find this page. Check the address or return to your organizations.",
      ),
    ).toBeTruthy();
    const links = within(main).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0].textContent).toBe("Back to organizations");
    expect(links[0].getAttribute("href")).toBe(pageHref("overview"));
    expect(links[0].getAttribute("href")).toBe("/overview");
    for (const secret of [
      "missing",
      "studio",
      "other",
      "third",
      "fictional",
      "ABCD",
      "example.test",
    ])
      expect(container.innerHTML).not.toContain(secret);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    expect(replaceState).not.toHaveBeenCalled();
    expect(window.location.pathname + window.location.search).toBe(
      unknown.split("#")[0],
    );
    expect(window.location.hash).toBe("#fictional");
    replaceState.mockRestore();
  });
  it.each([
    "/missing?page=keys",
    "/instance/not-a-section?organization=studio",
  ])("keeps %s on the fallback without canonicalizing the address", (path) => {
    expect(resolveRoute(url(path))).toBeUndefined();
    window.history.replaceState(null, "", path);
    const replaceState = rs.spyOn(window.history, "replaceState");
    render(<App />);
    expect(screen.getByRole("main", { name: "Page not found" })).toBeTruthy();
    expect(replaceState).not.toHaveBeenCalled();
    expect(window.location.pathname + window.location.search).toBe(path);
    replaceState.mockRestore();
  });
  it("focuses the heading first, then reaches the single link by keyboard", async () => {
    window.history.replaceState(null, "", "/missing");
    render(<App />);
    const heading = screen.getByRole("heading", { name: "Page not found" });
    expect(heading.getAttribute("tabindex")).toBe("-1");
    expect(document.activeElement).toBe(heading);
    const link = screen.getByRole("link", { name: "Back to organizations" });
    expect(document.activeElement).not.toBe(link);
    const user = userEvent.setup();
    await user.tab();
    expect(document.activeElement).toBe(link);
    await user.tab();
    expect(document.activeElement).not.toBe(link);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(link);
  });
  it("scopes the fallback styles without changing the shared standalone rule", () => {
    const css = readFileSync("src/styles.css", "utf8");
    expect(css).toContain(
      ".standalone {\n  max-width: 720px;\n  margin: 60px auto;\n  padding: 24px;\n}",
    );
    expect(css).toContain(
      ".not-found-action:focus-visible {\n  outline: 2px solid #1d4ed8;\n  outline-offset: 3px;\n}",
    );
    expect(css).toContain("min-height: 44px;");
    expect(css).toContain("max-width: 768px");
    expect(css).toContain("--not-found-padding: 24px;");
  });
  it("matches every server allowlisted route and serves every generated route", () => {
    const source = readFileSync("../../servers/aiprouter/console.go", "utf8");
    const paths = [...source.matchAll(/"(\/[^\"]*)": true/g)].map(
      (match) => match[1],
    );
    expect(paths.length).toBeGreaterThan(0);
    for (const path of paths)
      expect(resolveRoute(url(path)), path).toBeDefined();
    for (const path of [
      ...Object.values(pagePaths),
      ...Object.values(operatorPaths),
    ])
      expect(paths, path).toContain(path);
  });
});
function mount(path: string) {
  window.history.replaceState(null, "", path);
  const organization = { id: "studio", name: "Studio", role: Role.OWNER };
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ userId: "self", isInstanceOperator: true }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => ({
        settings: {
          revision: "v1",
          requestLimits: { requestsPerMinute: 120, concurrentRequests: 10 },
        },
      }),
    });
    router.service(OrganizationService, {
      listOrganizations: () => ({
        organizations: [
          organization,
          { ...organization, id: "other", name: "Other" },
        ],
      }),
      getOrganization: () => ({ organization }),
    });
  });
  render(
    <ApiProvider
      rpcTransport={transport}
      queryClient={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <App />
    </ApiProvider>,
  );
}
describe("page entry and navigation", () => {
  it("keeps the sidebar across internal links and restores organization scope on history", async () => {
    mount("/connect?organization=studio");
    await screen.findByRole("heading", { name: "Connect a client" });
    const sidebar = document.querySelector(".sidebar");
    const navigation = within(
      screen.getByRole("navigation", { name: "Main navigation" }),
    );
    const push = rs.spyOn(window.history, "pushState");
    await userEvent.click(navigation.getByRole("link", { name: "API keys" }));
    await screen.findByRole("heading", { name: "API keys" });
    expect(window.location.pathname + window.location.search).toBe(
      "/organizations/studio/keys",
    );
    expect(document.querySelector(".sidebar")).toBe(sidebar);
    expect(push).toHaveBeenCalledTimes(1);
    act(() => {
      window.history.replaceState(null, "", "/organizations/other/connect");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("heading", { name: "Connect a client" });
    expect(document.querySelector(".sidebar")).toBe(sidebar);
    expect(
      document.querySelector(".sidebar .org-switch")?.textContent,
    ).toContain("Other");
    expect(
      navigation.getByRole("link", { name: "API keys" }).getAttribute("href"),
    ).toBe("/organizations/other/keys");
  });
  it("switches organizations through path history without replacing the shell", async () => {
    mount("/organizations/studio/connect?filter=active#main");
    await screen.findByRole("heading", { name: "Connect a client" });
    const sidebar = document.querySelector(".sidebar");
    const push = rs.spyOn(window.history, "pushState");
    await userEvent.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0],
    );
    await userEvent.click(screen.getByRole("button", { name: "Other Owner" }));
    expect(
      window.location.pathname + window.location.search + window.location.hash,
    ).toBe("/organizations/other/connect?filter=active#main");
    expect(push).toHaveBeenCalledTimes(1);
    expect(document.querySelector(".sidebar")).toBe(sidebar);
    act(() => {
      window.history.replaceState(
        null,
        "",
        "/organizations/studio/connect?filter=active#main",
      );
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("heading", { name: "Connect a client" });
    expect(
      document.querySelector(".sidebar .org-switch")?.textContent,
    ).toContain("Studio");
  });
  it("replaces a legacy organization bookmark without adding history", async () => {
    window.history.replaceState(null, "", "/connect?organization=studio");
    const historyLength = window.history.length;
    mount("/connect?organization=studio");
    await screen.findByRole("heading", { name: "Connect a client" });
    expect(window.location.pathname + window.location.search).toBe(
      "/organizations/studio/connect",
    );
    expect(window.history.length).toBe(historyLength);
  });
  it("protects unsaved operator edits on internal links and allows confirmed navigation", async () => {
    mount("/instance/settings");
    const input = await screen.findByRole("spinbutton", {
      name: "Requests per minute",
    });
    await userEvent.clear(input);
    await userEvent.type(input, "200");
    const confirm = rs.spyOn(window, "confirm").mockReturnValue(false);
    const back = screen.getByRole("link", { name: "Back to organizations" });
    await userEvent.click(back);
    expect(window.location.pathname).toBe("/instance/settings");
    expect((input as HTMLInputElement).value).toBe("200");
    confirm.mockReturnValue(true);
    await userEvent.click(back);
    await screen.findByRole("heading", { name: "Your workspace" });
    expect(window.location.pathname).toBe("/overview");
    expect(confirm).toHaveBeenCalledTimes(2);
  });
  it("removes an empty workspace selector without adding history entries", () => {
    window.history.replaceState(null, "", "/keys?organization=");
    const historyLength = window.history.length;
    mount("/keys?organization=");
    expect(window.location.pathname + window.location.search).toBe("/keys");
    expect(window.history.length).toBe(historyLength);
  });
  it("opens invitation acceptance through its canonical route", async () => {
    mount("/invitation");
    expect(
      await screen.findByRole("heading", { name: "Join an organization" }),
    ).toBeTruthy();
    expect(window.location.pathname).toBe("/invitation");
  });
  it("opens organization usage and emits scoped canonical menu links", async () => {
    mount("/organization-usage?organization=studio");
    expect(
      await screen.findByRole("heading", { name: "Organization usage" }),
    ).toBeTruthy();
    expect(window.location.pathname).toBe(
      "/organizations/studio/organization-usage",
    );
    const navigation = within(
      screen.getByRole("navigation", { name: "Main navigation" }),
    );
    expect(
      navigation.getByRole("link", { name: "API keys" }).getAttribute("href"),
    ).toBe("/organizations/studio/keys");
    expect(
      navigation
        .getByRole("link", { name: "Organization usage" })
        .getAttribute("aria-current"),
    ).toBe("page");
  });
  it("opens instance settings and restores the section on browser navigation", async () => {
    mount("/instance/settings");
    const limits = await screen.findByRole("button", {
      name: "Request limits",
    });
    expect(limits.getAttribute("aria-current")).toBe("page");
    await userEvent.click(screen.getByRole("button", { name: "Retention" }));
    expect(window.location.pathname).toBe("/instance/retention");
    act(() => {
      window.history.replaceState(null, "", "/instance/settings");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(limits.getAttribute("aria-current")).toBe("page");
  });
  it("keeps an unsaved operator editor when browser navigation is canceled", async () => {
    mount("/instance/settings");
    const input = await screen.findByRole("spinbutton", {
      name: "Requests per minute",
    });
    await userEvent.clear(input);
    await userEvent.type(input, "200");
    const confirm = rs.spyOn(window, "confirm").mockReturnValue(false);
    try {
      act(() => {
        window.history.replaceState(null, "", "/instance/health");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      expect(confirm).toHaveBeenCalled();
      expect(window.location.pathname).toBe("/instance/settings");
      expect((input as HTMLInputElement).value).toBe("200");
    } finally {
      confirm.mockRestore();
    }
  });
});
