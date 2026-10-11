// SPDX-License-Identifier: Apache-2.0
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
