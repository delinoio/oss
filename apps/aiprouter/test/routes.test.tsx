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
  resolveRoute,
} from "../src/routes";

const url = (path: string) => new URL(path, "http://localhost");
describe("canonical console routes", () => {
  it.each(Object.entries(pagePaths))(
    "resolves the %s deep link",
    (page, path) => {
      const route = resolveRoute(url(path + "?organization=studio"));
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
      "/keys?organization=studio+%26+other",
    );
    expect(pageHref("account")).toBe("/account");
    expect(pageHref("keys", "")).toBe("/keys");
  });
  it("rejects unknown paths instead of showing overview", () => {
    expect(resolveRoute(url("/missing?page=keys"))).toBeUndefined();
    window.history.replaceState(null, "", "/missing");
    render(<App />);
    expect(
      screen.getByRole("heading", { name: "Page not found" }),
    ).toBeTruthy();
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
      listOrganizations: () => ({ organizations: [organization] }),
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
    expect(window.location.pathname).toBe("/organization-usage");
    const navigation = within(
      screen.getByRole("navigation", { name: "Main navigation" }),
    );
    expect(
      navigation.getByRole("link", { name: "API keys" }).getAttribute("href"),
    ).toBe("/keys?organization=studio");
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
