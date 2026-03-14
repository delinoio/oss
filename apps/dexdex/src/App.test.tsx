import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi, beforeEach } from "vitest";
import App from "./App";

// Mock localStorage
const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => { store[key] = value; },
    removeItem: (key: string) => { delete store[key]; },
    clear: () => { store = {}; },
  };
})();

Object.defineProperty(window, "localStorage", { value: localStorageMock });

beforeEach(() => {
  localStorageMock.clear();
  document.documentElement.classList.remove("dark");
});

describe("App", () => {
  it("renders the main layout with sidebar and task list", () => {
    render(<App />);

    expect(screen.getByTestId("app-layout")).toBeTruthy();
    expect(screen.getByTestId("sidebar")).toBeTruthy();
    expect(screen.getByTestId("tab-bar")).toBeTruthy();
    expect(screen.getByTestId("task-list")).toBeTruthy();
  });

  it("shows task list heading", () => {
    render(<App />);

    expect(screen.getByRole("heading", { name: "Tasks" })).toBeTruthy();
  });

  it("displays mock tasks in the task list", () => {
    render(<App />);

    expect(screen.getByText("Add user authentication flow")).toBeTruthy();
    expect(screen.getByText("Fix database migration rollback")).toBeTruthy();
    expect(screen.getByText("Refactor API response serialization")).toBeTruthy();
  });

  it("navigates to inbox via sidebar", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-inbox"));
    expect(screen.getByTestId("inbox-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Inbox" })).toBeTruthy();
  });

  it("navigates to settings via sidebar", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-settings"));
    expect(screen.getByTestId("settings-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Settings" })).toBeTruthy();
  });

  it("navigates to task detail when clicking a task row", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("task-row-task-001"));
    expect(screen.getByTestId("task-detail")).toBeTruthy();
    // Title appears in both task detail heading and tab bar
    expect(screen.getAllByText("Add user authentication flow").length).toBeGreaterThanOrEqual(1);
  });

  it("shows back button in task detail and returns to list", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("task-row-task-001"));
    expect(screen.getByTestId("task-detail")).toBeTruthy();

    await user.click(screen.getByTestId("back-button"));
    expect(screen.getByTestId("task-list")).toBeTruthy();
  });

  it("shows connection status dot in sidebar", () => {
    render(<App />);

    expect(screen.getByTestId("connection-dot")).toBeTruthy();
  });

  it("shows create task button and opens dialog", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("create-task-button"));
    expect(screen.getByTestId("create-dialog")).toBeTruthy();
  });

  it("creates a new task via dialog", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("create-task-button"));

    await user.type(screen.getByTestId("task-title-input"), "My new task");
    await user.type(screen.getByTestId("task-description-input"), "Some description");
    await user.click(screen.getByTestId("submit-create-task"));

    // Dialog should close and new task should appear in the list
    expect(screen.queryByTestId("create-dialog")).toBeNull();
    expect(screen.getByText("My new task")).toBeTruthy();
  });

  it("opens command palette with keyboard shortcut", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.keyboard("{Meta>}k{/Meta}");
    expect(screen.getByTestId("command-palette")).toBeTruthy();
  });

  it("closes command palette with Escape", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.keyboard("{Meta>}k{/Meta}");
    expect(screen.getByTestId("command-palette")).toBeTruthy();

    await user.keyboard("{Escape}");
    expect(screen.queryByTestId("command-palette")).toBeNull();
  });

  it("searches in command palette", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.keyboard("{Meta>}k{/Meta}");
    const input = screen.getByTestId("command-palette-input");
    await user.type(input, "auth");

    // Should filter to matching items within the command palette
    const palette = screen.getByTestId("command-palette");
    expect(palette.textContent).toContain("Add user authentication flow");
  });

  it("toggles dark mode in settings", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-settings"));
    await user.click(screen.getByTestId("theme-dark"));

    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(localStorageMock.getItem("dexdex-theme")).toBe("dark");
  });

  it("toggles back to light mode", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-settings"));
    await user.click(screen.getByTestId("theme-dark"));
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    await user.click(screen.getByTestId("theme-light"));
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(localStorageMock.getItem("dexdex-theme")).toBe("light");
  });

  it("persists theme preference in localStorage", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-settings"));
    await user.click(screen.getByTestId("theme-dark"));

    expect(localStorageMock.getItem("dexdex-theme")).toBe("dark");
  });

  it("shows session output panel in task detail", async () => {
    const user = userEvent.setup();
    render(<App />);

    // Navigate to task-001 which has session output
    await user.click(screen.getByTestId("task-row-task-001"));
    expect(screen.getByTestId("session-output-panel")).toBeTruthy();
  });

  it("shows plan decision controls for waiting tasks", async () => {
    const user = userEvent.setup();
    render(<App />);

    // Navigate to task-002 which has a subtask waiting for plan approval
    await user.click(screen.getByTestId("task-row-task-002"));
    expect(screen.getByTestId("plan-decisions")).toBeTruthy();
    expect(screen.getByTestId("approve-button")).toBeTruthy();
    expect(screen.getByTestId("reject-button")).toBeTruthy();
  });

  it("shows subtask timeline in task detail", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("task-row-task-001"));
    expect(screen.getByTestId("subtask-timeline")).toBeTruthy();
  });

  it("filters tasks by status", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("filter-COMPLETED"));

    // Only the completed task should be visible in the list
    expect(screen.getByTestId("task-row-task-003")).toBeTruthy();
    expect(screen.queryByTestId("task-row-task-001")).toBeNull();
    expect(screen.queryByTestId("task-row-task-002")).toBeNull();
  });

  it("shows notifications in inbox with unread indicator", async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByTestId("nav-inbox"));

    expect(screen.getByText("Plan approval needed")).toBeTruthy();
    expect(screen.getByText("CI failure on PR #42")).toBeTruthy();
  });
});
