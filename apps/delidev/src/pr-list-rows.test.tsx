// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  IntegrationService,
  ResourceSchema,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ItemKind, ItemState, QueryOperation } from "./github-query-model";
import { i18n, SupportedLanguage } from "./localization";
import { PRAvatarCache } from "./pr-avatar-cache";
import { AvatarContext, PRWorkspaceRows } from "./pr-workspace";
import { formatUtcInstant } from "./timestamp-format";

afterEach(() => {
  vi.useRealTimers();
});

const now = Date.parse("2026-10-14T12:00:00Z");
const seconds = (value: number) => new Date(now - value * 1000).toISOString();
const selected = create(ResourceSchema, {
  id: newRequestId(),
  kind: EntityKind.REPOSITORY,
  revision: 1n,
  schemaVersion: 1,
  documentJson: encode({
    integration_id: newRequestId(),
    github_owner: "fixture-owner",
    github_name: "repo",
  }),
});
const item = (number: string, updatedAt: string, extra = {}) => ({
  provider: "github.com",
  kind: "pull-request",
  identity_source: "pull-request-api",
  id: String(Number(number) + 1000),
  node_id: `PR_${number}`,
  number,
  title: `Row title ${number}`,
  state: "open",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: updatedAt,
  url: `https://github.com/fixture-owner/repo/pull/${number}`,
  author: { login: "fixture-author", kind: "user" },
  draft: false,
  ...extra,
});
function rows(
  updated: string[],
  options: { active?: boolean; extra?: Record<string, unknown> } = {},
) {
  const rpc = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => {
    router.service(IntegrationService, {
      queryRepositoryIntegration: rpc,
      getPullRequestWorkspace: rpc,
      readPullRequestAvatar: rpc,
    });
    router.service(SystemService, { getStatus: rpc });
  });
  const client = new QueryClient(),
    avatars = new PRAvatarCache();
  const page = {
    query: {
      kind: ItemKind.PullRequest,
      operation: QueryOperation.List,
      state: ItemState.Open,
      page: 1,
      page_size: 50,
    },
    data: {
      observed_at: "2026-10-14T11:59:59Z",
      items: updated.map((value, index) =>
        item(String(index + 1), value, options.extra),
      ),
    },
  };
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <AvatarContext.Provider value={avatars}>
          <div data-testid="scroller">
            <PRWorkspaceRows
              pages={[page]}
              selected={selected}
              enabled={false}
              active={options.active}
              choose={() => {}}
              observations={new Map()}
              observe={() => {}}
              allSeeds={updated.map((_, index) => String(index + 1))}
            />
          </div>
        </AvatarContext.Provider>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { view, rpc };
}

const labels = {
  [SupportedLanguage.English]: [
    "Updated now",
    "Updated 2 minutes ago",
    "Updated 3 hours ago",
    "Updated 2 days ago",
    "Updated 3 weeks ago",
    "Updated 2 months ago",
    "Updated 1 year ago",
  ],
  [SupportedLanguage.Korean]: [
    "방금 업데이트",
    "2분 전 업데이트",
    "3시간 전 업데이트",
    "2일 전 업데이트",
    "3주 전 업데이트",
    "2개월 전 업데이트",
    "1년 전 업데이트",
  ],
};
const ages = [30, 120, 3 * 3600, 2 * 86400, 21 * 86400, 60 * 86400, 400 * 86400];

it.each([SupportedLanguage.English, SupportedLanguage.Korean])(
  "shows relative Updated labels at every age with a frozen clock (%s)",
  async (language) => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(now);
    await act(() => i18n.changeLanguage(language));
    const { view } = rows(ages.map(seconds));
    const shown = [...view.container.querySelectorAll(".pr-row-updated")].map(
      (node) => node.textContent,
    );
    expect(shown).toEqual(labels[language]);
    // The original instant stays machine-readable and no native title competes
    // with the custom tooltip.
    const times = [...view.container.querySelectorAll(".pr-row-updated time")];
    expect(times.map((node) => node.getAttribute("datetime"))).toEqual(
      ages.map(seconds),
    );
    for (const node of times) {
      expect(node.hasAttribute("title")).toBe(false);
      expect(node.hasAttribute("aria-description")).toBe(false);
    }
    await act(() => i18n.changeLanguage(SupportedLanguage.English));
  },
);

it("keeps the title first with a decorative icon and 12px metadata before the update line", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
  await act(() => i18n.changeLanguage(SupportedLanguage.English));
  const { view } = rows([seconds(120)], {
    extra: { draft: true, state: "closed" },
  });
  const button = view.container.querySelector("[data-pr-number='1']")!;
  const order = [...button.children].map((node) => node.className);
  expect(order).toEqual(["pr-row-title", "pr-row-meta", "pr-row-updated"]);
  const title = screen.getByRole("heading", { name: "#1 Row title 1" });
  expect(title.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  const meta = button.querySelector(".pr-row-meta")!;
  expect(meta.textContent).toContain("Closed");
  expect(meta.textContent).toContain("Draft");
  expect(meta.textContent).toContain("fixture-author");
});

it("changes labels through the shared scheduler without any RPC", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  await act(() => i18n.changeLanguage(SupportedLanguage.English));
  const { view, rpc } = rows([seconds(30)]);
  const label = () => view.container.querySelector(".pr-row-updated")?.textContent;
  expect(label()).toBe("Updated now");
  await act(async () => {
    vi.advanceTimersByTime(31_000);
  });
  expect(label()).toBe("Updated 1 minute ago");
  await act(async () => {
    vi.advanceTimersByTime(3 * 3600_000);
  });
  expect(label()).toBe("Updated 3 hours ago");
  expect(rpc).not.toHaveBeenCalled();
});

it("stops the presentation clock while the surface is inactive", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  await act(() => i18n.changeLanguage(SupportedLanguage.English));
  const inactive = rows([seconds(30)], { active: false });
  const idle = vi.getTimerCount();
  inactive.view.unmount();
  rows([seconds(30)], { active: true });
  // Only an active surface adds the shared presentation timer.
  expect(vi.getTimerCount()).toBeGreaterThan(idle);
});

const exact = "2026-10-14 11:59:30.123456789 UTC";
const exactSource = "2026-10-14T11:59:30.123456789Z";
async function hoverFixture() {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
  await act(() => i18n.changeLanguage(SupportedLanguage.English));
  const fixture = rows([exactSource]);
  const time = fixture.view.container.querySelector(".pr-row-updated")!;
  const button = fixture.view.container.querySelector<HTMLButtonElement>(
    "[data-pr-number='1']",
  )!;
  return { ...fixture, time, button };
}

it("formats exact UTC instants with preserved fractions and converted offsets", () => {
  expect(formatUtcInstant("2026-10-14T11:59:30.123456789Z")).toBe(
    "2026-10-14 11:59:30.123456789 UTC",
  );
  expect(formatUtcInstant("2026-10-14T11:59:30Z")).toBe(
    "2026-10-14 11:59:30 UTC",
  );
  expect(formatUtcInstant("2026-10-14T21:00:00.50+09:00")).toBe(
    "2026-10-14 12:00:00.50 UTC",
  );
  expect(formatUtcInstant("2026-02-30T00:00:00Z")).toBeUndefined();
  expect(formatUtcInstant("not a time")).toBeUndefined();
});

it("reveals the exact original UTC instant on hover and dismisses it on pointer departure", async () => {
  const { time, button, rpc } = await hoverFixture();
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(time);
  const tooltip = screen.getByRole("tooltip");
  expect(tooltip.textContent).toBe(exact);
  expect(tooltip.parentElement).toBe(document.body);
  expect(button.getAttribute("aria-describedby")).toBe(tooltip.id);
  fireEvent.pointerLeave(time);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(button.getAttribute("aria-describedby")).toBeNull();
  expect(rpc).not.toHaveBeenCalled();
});

it("exposes the same instant on keyboard focus and dismisses it on blur or Escape", async () => {
  const { button } = await hoverFixture();
  act(() => button.focus());
  expect(screen.getByRole("tooltip").textContent).toBe(exact);
  act(() => button.blur());
  expect(screen.queryByRole("tooltip")).toBeNull();
  act(() => button.focus());
  expect(screen.getByRole("tooltip").textContent).toBe(exact);
  fireEvent.keyDown(button, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("dismisses on Escape after a pointer reveal, scroll, resize and owner disposal", async () => {
  const { time, view } = await hoverFixture();
  fireEvent.pointerEnter(time);
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(time);
  fireEvent.scroll(screen.getByTestId("scroller"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(time);
  fireEvent.resize(window);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(time);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  view.unmount();
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("does not reveal an exact instant for an unverifiable timestamp", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
  const { view } = rows(["2026-02-30T00:00:00Z"]);
  const time = view.container.querySelector(".pr-row-updated")!;
  fireEvent.pointerEnter(time);
  expect(screen.queryByRole("tooltip")).toBeNull();
});
