// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  AppInformation,
  AppInformationLink,
  appInformationPlatform,
  retainedDesktopContext,
} from "./app-information";
import { PersistentConnectionView } from "./connections-page";
import { i18n } from "./localization";
import { SettingsCategory } from "./settings-category";
import { matchSettings, SettingsSearchTarget } from "./settings-search";
import { applicationCommands } from "./command-menu";

it.each([
  ["darwin-amd64", "macOS · Intel"],
  ["darwin-arm64", "macOS · Apple Silicon"],
  ["windows-amd64", "Windows · x64"],
  ["windows-arm64", "Windows · ARM64"],
  ["linux-amd64", "Linux · x64"],
  ["linux-arm64", "Linux · ARM64"],
])(
  "shows native version and %s without server metadata",
  async (target, platform) => {
    const control = vi.fn();
    render(
      <AppInformation
        controls={{
          readContext: async () => ({ current_version: "0.1.7", target }),
          control,
        }}
      />,
    );
    expect(await screen.findByText(platform)).toBeTruthy();
    expect(screen.getByText("0.1.7")).toBeTruthy();
    expect(control).not.toHaveBeenCalled();
  },
);
it("keeps browser absence, native read failure and invalid platform explicit", async () => {
  const view = render(<AppInformation />);
  expect(
    screen.getAllByText("App information is unavailable in this browser."),
  ).toHaveLength(2);
  expect(
    screen.getByRole<HTMLButtonElement>("button", { name: "Official releases" })
      .disabled,
  ).toBe(true);
  view.unmount();
  render(
    <AppInformation
      controls={{
        readContext: async () => ({
          current_version: "0.1.7",
          target: "unknown",
        }),
        control: vi.fn(),
      }}
    />,
  );
  expect((await screen.findByRole("alert")).textContent).toBe(
    "The app information could not be confirmed.",
  );
  expect(screen.queryByText("0.1.7")).toBeNull();
  expect(appInformationPlatform("unknown")).toBeUndefined();
});
it("retains one original context read and DOM while the Settings presentation departs", async () => {
  const read = vi.fn(async () => ({
    current_version: "0.1.7",
    target: "darwin-arm64",
  }));
  const controls = {
    readContext: retainedDesktopContext(read),
    control: vi.fn(),
  };
  const target = document.createElement("div");
  document.body.append(target);
  const tree = (active: boolean) => (
    <PersistentConnectionView target={active ? target : undefined} hidden>
      <AppInformation active={active} controls={controls} />
    </PersistentConnectionView>
  );
  const view = render(tree(true));
  const version = await screen.findByText("0.1.7");
  view.rerender(tree(false));
  view.rerender(tree(true));
  expect(await screen.findByText("0.1.7")).toBe(version);
  await controls.readContext();
  expect(read).toHaveBeenCalledTimes(1);
  view.unmount();
  target.remove();
});
it("retains a failed original context read rather than silently substituting or retrying", async () => {
  const read = vi.fn(async () => {
    throw new Error("invalid-evidence");
  });
  const controls = {
    readContext: retainedDesktopContext(read),
    control: vi.fn(),
  };
  const view = render(<AppInformation controls={controls} />);
  await screen.findByRole("alert");
  view.rerender(<AppInformation active={false} controls={controls} />);
  view.rerender(<AppInformation controls={controls} />);
  await expect(controls.readContext()).rejects.toThrow("invalid-evidence");
  expect(read).toHaveBeenCalledTimes(1);
});
it("opens only a selected fixed native action and reports failure without replay", async () => {
  const openLink = vi.fn(async (_action: AppInformationLink) => {
    throw new Error("sidecar-failed");
  });
  render(<AppInformation openLink={openLink} />);
  expect(openLink).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Official releases" }));
  await screen.findByRole("alert");
  expect(openLink).toHaveBeenCalledExactlyOnceWith(AppInformationLink.Releases);
  fireEvent.click(screen.getByRole("button", { name: "License" }));
  await vi.waitFor(() => expect(openLink).toHaveBeenCalledTimes(2));
  expect(openLink.mock.calls[1]).toEqual([AppInformationLink.License]);
  expect(
    screen.getByText(/not a complete inventory of the installed package/),
  ).toBeTruthy();
});
it("shares all static focus targets with command navigation in English and Korean", async () => {
  const openSettings = vi.fn();
  const commands = applicationCommands({
    navigate: vi.fn(),
    navigateHeader: vi.fn(),
    openSettings,
    newSession: vi.fn(),
    newGeneralChat: vi.fn(),
    newProject: vi.fn(),
    help: vi.fn(),
  });
  const expected = [
    SettingsSearchTarget.AppInformation,
    SettingsSearchTarget.AppUpdates,
    SettingsSearchTarget.AppRecovery,
    SettingsSearchTarget.AppReleases,
    SettingsSearchTarget.AppLicense,
    SettingsSearchTarget.AppNotices,
  ];
  for (const target of expected) {
    const command = commands.find(
      (row) => row.value === `settings:app-information:${target}`,
    )!;
    expect(command).toBeTruthy();
    command.run();
    expect(openSettings.mock.lastCall?.[0]).toMatchObject({
      category: SettingsCategory.AppInformation,
      target,
    });
  }
  expect(
    matchSettings("Official releases", [
      {
        category: SettingsCategory.AppInformation,
        label: "App information",
        help: "Check the current app and its updates.",
      },
    ]).some((row) => row.target === SettingsSearchTarget.AppReleases),
  ).toBe(true);
  await act(() => i18n.changeLanguage("ko"));
  expect(
    matchSettings("앱 정보", [
      {
        category: SettingsCategory.AppInformation,
        label: "앱 정보",
        help: "현재 앱과 업데이트 정보를 확인하세요.",
      },
    ]).some((row) => row.target === SettingsSearchTarget.AppInformation),
  ).toBe(true);
  await act(() => i18n.changeLanguage("en"));
});
it("groups the three related links in one list with decorative icons and unchanged selectors", () => {
  const view = render(<AppInformation openLink={vi.fn(async () => {})} />);
  const group = view.container.querySelector(".app-information-links")!;
  const rows = [...group.querySelectorAll("button")];
  expect(rows.map((row) => row.textContent)).toEqual([
    "Official releases",
    "License",
    "Open-source notices",
  ]);
  expect(
    rows.map((row) => row.getAttribute("data-settings-search-target")),
  ).toEqual([
    SettingsSearchTarget.AppReleases,
    SettingsSearchTarget.AppLicense,
    SettingsSearchTarget.AppNotices,
  ]);
  for (const row of rows) {
    expect(row.getAttribute("type")).toBe("button");
    const icons = row.querySelectorAll("svg");
    expect(icons).toHaveLength(2);
    for (const icon of icons) {
      expect(icon.getAttribute("aria-hidden")).toBe("true");
      expect(icon.getAttribute("focusable")).toBe("false");
    }
    expect(row.querySelector(".app-information-link-icon")).toBeTruthy();
    expect(row.querySelector(".app-information-link-external")).toBeTruthy();
  }
  expect(screen.getAllByRole("button", { name: /./ })).toHaveLength(3);
  const notice = screen.getByText(
    /not a complete inventory of the installed package/,
  );
  expect(notice.classList.contains("app-information-help")).toBe(true);
  expect(group.nextElementSibling).toBe(notice);
  expect(notice.textContent).toBe(
    "These links show current repository source notices. They are not a complete inventory of the installed package.",
  );
});
it("keeps every related row disabled without dispatch when the opener is unavailable or inactive", () => {
  const openLink = vi.fn(async () => {});
  const view = render(<AppInformation />);
  for (const row of view.container.querySelectorAll<HTMLButtonElement>(
    ".app-information-link",
  )) {
    expect(row.disabled).toBe(true);
    fireEvent.click(row);
  }
  view.unmount();
  const inactive = render(
    <AppInformation active={false} openLink={openLink} />,
  );
  for (const row of inactive.container.querySelectorAll<HTMLButtonElement>(
    ".app-information-link",
  )) {
    expect(row.disabled).toBe(true);
    fireEvent.click(row);
  }
  expect(openLink).not.toHaveBeenCalled();
});
it("disables all rows while one opening is in flight and dispatches once", async () => {
  let resolve!: () => void;
  const openLink = vi.fn(
    () =>
      new Promise<void>((done) => {
        resolve = done;
      }),
  );
  render(<AppInformation openLink={openLink} />);
  fireEvent.click(screen.getByRole("button", { name: "License" }));
  expect(await screen.findByText("Opening in your browser…")).toBeTruthy();
  const rows = screen.getAllByRole<HTMLButtonElement>("button");
  expect(rows.every((row) => row.disabled)).toBe(true);
  fireEvent.click(rows[0]);
  expect(openLink).toHaveBeenCalledExactlyOnceWith(AppInformationLink.License);
  await act(async () => resolve());
  await vi.waitFor(() =>
    expect(
      screen.getByRole<HTMLButtonElement>("button", { name: "License" })
        .disabled,
    ).toBe(false),
  );
});
it("declares the grouped 56px row presentation with unclipped focus and enabled-only hover", () => {
  const css = readFileSync("src/app-information.css", "utf8");
  const rule = (selector: string) => {
    const start = css.indexOf(`${selector} {`);
    expect(start).toBeGreaterThanOrEqual(0);
    return css.slice(start, css.indexOf("}", start));
  };
  const group = rule(".app-information-links");
  expect(group).toContain("border: 1px solid var(--border)");
  expect(group).toContain("border-radius: 8px");
  expect(group).toContain("overflow: visible");
  expect(group).toContain("margin: 0 0 16px");
  const row = rule(".app-information-link");
  expect(row).toContain("min-height: 56px");
  expect(row).toContain("padding: 12px 16px");
  expect(row).toContain("gap: 12px");
  expect(row).toContain("font-size: 14px");
  expect(row).toContain("font-weight: 500");
  expect(rule(".app-information-link + .app-information-link")).toContain(
    "border-top: 1px solid var(--border)",
  );
  expect(css).toContain(".app-information-link:enabled:hover {");
  expect(css).not.toContain(".app-information-link:hover");
  expect(rule(".app-information-link-icon")).toContain("width: 20px");
  expect(rule(".app-information-link-external")).toContain("width: 18px");
  expect(css).toMatch(
    /\.app-information-card button:focus-visible \{\s*outline: 2px solid var\(--accent\);\s*outline-offset: 3px;/,
  );
  expect(rule(".app-information-help")).toContain("font-size: 12px");
});
it.each([
  ["browser-unavailable", "The selected browser is not installed"],
  ["browser-launch-failed", "could not be opened in the selected browser"],
  ["storage-unavailable", "Browser settings could not be read"],
])(
  "points a %s failure at Settings > Browser and never retries",
  async (code, expected) => {
    const openLink = vi.fn(async () => {
      throw code;
    });
    render(<AppInformation openLink={openLink} />);
    fireEvent.click(screen.getByRole("button", { name: "License" }));
    expect((await screen.findByRole("alert")).textContent).toContain(expected);
    expect(openLink).toHaveBeenCalledTimes(1);
  },
);
