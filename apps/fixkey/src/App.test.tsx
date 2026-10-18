import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { App, CandidatePicker } from "./App";
import type { DesktopApi, DesktopState } from "./api";

function fixture(patch: Partial<DesktopState> = {}) {
  let state: DesktopState = {
    revision: 1,
    version: "0.0.1",
    platform: "macos-arm64",
    osLanguage: "en",
    status: "active",
    active: true,
    settingsError: null,
    logsHealthy: true,
    unresolvedApps: [],
    settings: {
      schemaVersion: 1,
      paused: false,
      koreanEnabled: true,
      englishEnabled: true,
      loginStartup: false,
      uiLanguage: "system",
      theme: "system",
      shortcut: null,
      excludedApps: [],
    },
    update: { phase: "idle", version: null, progress: null, error: null },
    ...patch,
  };
  const api: DesktopApi = {
    state: vi.fn(async () => state),
    save: vi.fn(async (settings) => {
      state = { ...state, settings, revision: state.revision + 1 };
      return state;
    }),
    action: vi.fn(async () => {}),
    apps: vi.fn(async () => []),
    candidates: vi.fn(async () => () => {}),
    candidateAction: vi.fn(async () => {}),
  };
  return { api, state };
}
function mount(api: DesktopApi) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false, refetchInterval: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <App api={api} />
    </QueryClientProvider>,
  );
}

describe("settings desktop contract", () => {
  it("keeps the previous value when persistence fails and reports a safe error", async () => {
    const { api } = fixture();
    vi.mocked(api.save).mockRejectedValue("settings-save-failed");
    mount(api);
    const checkbox = await screen.findByLabelText("Start at login");
    expect((checkbox as HTMLInputElement).checked).toBe(false);
    fireEvent.click(checkbox);
    await screen.findByText("settings-save-failed");
    expect((checkbox as HTMLInputElement).checked).toBe(false);
  });
  it("persists immediately and switches language and theme from the returned settings", async () => {
    const { api } = fixture();
    mount(api);
    await screen.findByText("Automatic correction");
    fireEvent.change(screen.getByLabelText("Theme"), {
      target: { value: "dark" },
    });
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe("dark"),
    );
    fireEvent.change(screen.getByLabelText("UI language"), {
      target: { value: "ko" },
    });
    await screen.findByText("자동 교정");
    expect(document.documentElement.lang).toBe("ko");
  });
  it("uses Korean on Korean systems and provides all localized help topics", async () => {
    const { api } = fixture({ osLanguage: "ko" });
    mount(api);
    fireEvent.click(
      await screen.findByRole("button", { name: "진단 및 도움말" }),
    );
    for (const topic of [
      "설치",
      "권한",
      "자판과 교정",
      "되돌리기",
      "수동 후보",
      "제외 앱",
      "개인정보",
      "업데이트",
      "문제 해결",
      "로그 삭제",
    ])
      expect(screen.getByText(topic, { selector: "summary" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "로그 삭제" }));
    await waitFor(() => expect(api.action).toHaveBeenCalledWith("delete-logs"));
  });
  it("shows the empty exclusion state and retains unresolved entries", async () => {
    const { api, state } = fixture();
    mount(api);
    fireEvent.click(
      await screen.findByRole("button", { name: "Excluded Apps" }),
    );
    expect(screen.getByText("No excluded apps")).toBeTruthy();
    const app = {
      identity: { platform: "macos" as const, bundle_id: "io.example.editor" },
      displayName: "Editor",
    };
    vi.mocked(api.apps).mockResolvedValue([app]);
    fireEvent.click(screen.getByRole("button", { name: "Add app" }));
    await screen.findByRole("dialog");
    fireEvent.click(screen.getAllByRole("button", { name: "Add app" }).at(-1)!);
    await screen.findByText("Editor", { selector: "strong" });
    expect(api.save).toHaveBeenCalledWith(
      { ...state.settings, excludedApps: [app] },
      1,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove Editor" }));
    await screen.findByText("No excluded apps");
  });
  it("captures a shortcut, cancels with Esc, and displays registration conflicts", async () => {
    const { api } = fixture();
    mount(api);
    fireEvent.click(await screen.findByRole("button", { name: "Shortcuts" }));
    fireEvent.click(screen.getByRole("button", { name: "Set shortcut" }));
    fireEvent.keyDown(screen.getByRole("textbox"), {
      key: "Escape",
      code: "Escape",
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.save).not.toHaveBeenCalled();
    vi.mocked(api.save).mockRejectedValue("shortcut-conflict");
    fireEvent.click(screen.getByRole("button", { name: "Set shortcut" }));
    fireEvent.keyDown(screen.getByRole("textbox"), {
      key: "f",
      code: "KeyF",
      ctrlKey: true,
      altKey: true,
    });
    await screen.findByText("shortcut-conflict");
    expect(screen.getByText("Unassigned")).toBeTruthy();
  });
  it("requires an explicit install action and describes recovery without losing settings", async () => {
    const { api } = fixture({
      update: {
        phase: "available",
        version: "0.0.2",
        progress: null,
        error: null,
      },
    });
    mount(api);
    fireEvent.click(await screen.findByRole("button", { name: "Updates" }));
    expect(api.action).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Download and install" }),
    );
    await waitFor(() =>
      expect(api.action).toHaveBeenCalledWith("install-update"),
    );
    expect(screen.getByText(/Your settings are preserved/)).toBeTruthy();
  });
  it("shows platform permission instructions and dismisses the dialog with Esc", async () => {
    const { api } = fixture({ status: "permission-missing", active: false });
    mount(api);
    const dialog = await screen.findByRole("dialog");
    expect(screen.getByText(/Input Monitoring/)).toBeTruthy();
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("Correction is inactive")).toBeTruthy();
  });
});

it("keeps manual candidates ephemeral and distinguishes unavailable validation", async () => {
  const { api } = fixture();
  vi.mocked(api.candidates).mockImplementation(async (callback) => {
    callback({
      generation: 4,
      selected: 0,
      items: [
        {
          text: "zlqhemrk",
          language: "en-us-qwerty",
          isOriginal: true,
          validation: "not-recognized",
        },
        {
          text: "키보드가",
          language: "ko-dubeolsik",
          isOriginal: false,
          validation: "validator-unavailable",
        },
      ],
    });
    return () => {};
  });
  render(<CandidatePicker api={api} />);
  await screen.findByText("Could not be checked");
  fireEvent.keyDown(screen.getByRole("main"), { key: "ArrowDown" });
  expect(api.candidateAction).toHaveBeenCalledWith(4, "next", undefined);
  fireEvent.keyDown(screen.getByRole("main"), { key: "Escape" });
  await screen.findByText("The source context is no longer available.");
  expect(screen.queryByText("zlqhemrk")).toBeNull();
});
