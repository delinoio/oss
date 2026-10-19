import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
  it("requests permission once on first launch and closes after a successful recheck", async () => {
    const { api, state } = fixture({
      status: "permission-missing",
      active: false,
    });
    mount(api);
    await screen.findByRole("dialog");
    await waitFor(() => expect(api.action).toHaveBeenCalledTimes(1));
    expect(api.action).toHaveBeenCalledWith("request-permission");
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Allow accessibility access" }),
    );
    vi.mocked(api.state).mockResolvedValue({
      ...state,
      status: "active",
      active: true,
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Recheck" }).at(-1)!);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
  it("shows request failures in the dialog and allows retry", async () => {
    const { api } = fixture({
      status: "permission-missing",
      active: false,
      osLanguage: "ko",
    });
    vi.mocked(api.action).mockRejectedValueOnce(
      "permission-request-unavailable",
    );
    mount(api);
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(dialog.textContent).toContain("permission-request-unavailable"),
    );
    fireEvent.click(screen.getByRole("button", { name: "접근성 허용하기" }));
    await waitFor(() => expect(api.action).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("dialog")).toBeTruthy();
  });
  it.each([
    [
      "en",
      "Allow Input Monitoring",
      "Accessibility access is already allowed.",
    ],
    ["ko", "입력 모니터링 허용하기", "접근성 권한은 이미 허용되어 있어요."],
  ] as const)(
    "identifies missing Input Monitoring without asking for accessibility again (%s)",
    async (osLanguage, requestLabel, granted) => {
      const { api } = fixture({
        status: "input-permission-missing",
        active: false,
        osLanguage,
      });
      mount(api);
      const dialog = await screen.findByRole("dialog");
      await waitFor(() => expect(api.action).toHaveBeenCalledTimes(1));
      expect(screen.getByText(granted)).toBeTruthy();
      expect(screen.getByRole("button", { name: requestLabel })).toBeTruthy();
      expect(dialog.textContent).not.toContain(
        osLanguage === "ko"
          ? "손쉬운 사용에서 FixKey를 켜"
          : "Enable FixKey in Accessibility",
      );
    },
  );
  it("updates the open dialog when accessibility is granted and closes after both permissions are granted", async () => {
    const { api, state } = fixture({
      status: "permission-missing",
      active: false,
    });
    mount(api);
    await screen.findByRole("dialog");
    await waitFor(() => expect(api.action).toHaveBeenCalledTimes(1));
    vi.mocked(api.state).mockResolvedValue({
      ...state,
      status: "input-permission-missing",
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Recheck" }).at(-1)!);
    await screen.findByRole("dialog", { name: "Allow Input Monitoring" });
    expect(
      screen.queryByRole("button", { name: "Allow accessibility access" }),
    ).toBeNull();
    expect(api.action).toHaveBeenCalledTimes(2);
    vi.mocked(api.state).mockResolvedValue({
      ...state,
      status: "active",
      active: true,
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Recheck" }).at(-1)!);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
  it.each([
    ["en", "Set up Input Monitoring", "Open system settings", "Recheck"],
    ["ko", "입력 모니터링 설정", "시스템 설정 열기", "다시 확인"],
  ] as const)(
    "keeps manual list setup available with effective access (%s)",
    async (osLanguage, setup, open, recheck) => {
      const { api } = fixture({ osLanguage });
      mount(api);
      fireEvent.click(await screen.findByRole("button", { name: setup }));
      const dialog = await screen.findByRole("dialog", { name: setup });
      expect(dialog.textContent).toContain("+");
      expect(dialog.textContent).toContain("FixKey.app");
      expect(api.action).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: open }));
      await waitFor(() =>
        expect(api.action).toHaveBeenCalledWith("permission-settings"),
      );
      await waitFor(() =>
        expect(
          (
            within(dialog).getByRole("button", {
              name: recheck,
            }) as HTMLButtonElement
          ).disabled,
        ).toBe(false),
      );
      fireEvent.click(within(dialog).getByRole("button", { name: recheck }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(api.action).toHaveBeenCalledWith("recheck");
      expect(screen.getByRole("button", { name: recheck })).toBeTruthy();
      expect(api.save).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: setup }));
      const reopened = await screen.findByRole("dialog", { name: setup });
      fireEvent(
        reopened,
        new Event("cancel", { bubbles: true, cancelable: true }),
      );
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    },
  );
  it("does not ask for permission on an authorized launch", async () => {
    const { api } = fixture();
    mount(api);
    await screen.findByText("Correction is active");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.action).not.toHaveBeenCalled();
  });
  it.each([
    ["en", "Waiting for an editable text field", "Diagnostics and Help"],
    ["ko", "교정할 입력란을 기다리고 있어요", "진단 및 도움말"],
  ] as const)(
    "explains an unidentified field as waiting and keeps its diagnostic code (%s)",
    async (osLanguage, title, diagnostics) => {
      const { api } = fixture({
        status: "unknown-field",
        active: false,
        osLanguage,
      });
      mount(api);
      await screen.findByText(title);
      expect(screen.queryByText("unknown-field")).toBeNull();
      expect(screen.queryByRole("dialog")).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: diagnostics }));
      expect(screen.getByText("unknown-field")).toBeTruthy();
    },
  );
  it("does not present a pause as waiting for a field", async () => {
    const { api } = fixture({ status: "paused", active: false });
    mount(api);
    await screen.findByText("Correction is inactive");
    expect(screen.queryByText("Waiting for an editable text field")).toBeNull();
  });
  it("shows platform permission instructions and dismisses the dialog with Esc", async () => {
    const { api } = fixture({ status: "permission-missing", active: false });
    mount(api);
    const dialog = await screen.findByRole("dialog");
    expect(
      screen.getByText(/Also enable FixKey in Input Monitoring/),
    ).toBeTruthy();
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
  await screen.findByText(/Could not be checked/);
  fireEvent.keyDown(screen.getByRole("main"), { key: "ArrowDown" });
  expect(api.candidateAction).toHaveBeenCalledWith(4, "next", undefined);
  fireEvent.keyDown(screen.getByRole("main"), { key: "Escape" });
  await screen.findByText("The source context is no longer available.");
  expect(screen.queryByText("zlqhemrk")).toBeNull();
});

it("retains the latest candidate generation and applies the current presentation", async () => {
  const { api } = fixture();
  vi.mocked(api.candidates).mockImplementation(async (callback) => {
    callback({
      generation: 5,
      revision: 2,
      selected: 0,
      language: "ko",
      theme: "dark",
      items: [
        {
          text: "키보드가",
          language: "ko-dubeolsik",
          isOriginal: false,
          validation: "recognized",
        },
      ],
    });
    callback({
      generation: 5,
      revision: 1,
      selected: 0,
      items: [
        {
          text: "late-initial-snapshot",
          language: "en-us-qwerty",
          isOriginal: true,
          validation: "recognized",
        },
      ],
    });
    callback({
      generation: 4,
      selected: 0,
      items: [
        {
          text: "stale",
          language: "en-us-qwerty",
          isOriginal: true,
          validation: "recognized",
        },
      ],
    });
    return () => {};
  });
  render(<CandidatePicker api={api} />);
  await screen.findByText("키보드가");
  expect(screen.queryByText("stale")).toBeNull();
  expect(screen.queryByText("late-initial-snapshot")).toBeNull();
  expect(document.documentElement.lang).toBe("ko");
  expect(document.documentElement.dataset.theme).toBe("dark");
  fireEvent.keyDown(screen.getByRole("main"), { key: "Enter" });
  expect(api.candidateAction).toHaveBeenCalledWith(5, "apply", undefined);
});
