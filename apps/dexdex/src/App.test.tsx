import { render, screen } from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { queryClient } from "./lib/query-client";
import { App } from "./app";

function renderApp() {
  return render(
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>,
  );
}

describe("App", () => {
  it("renders sidebar with navigation items", () => {
    renderApp();

    // "Tasks" appears in both sidebar nav and page heading
    expect(screen.getAllByText("Tasks").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Inbox")).toBeTruthy();
    expect(screen.getByText("Settings")).toBeTruthy();
  });

  it("renders task list by default", () => {
    renderApp();

    // The task list header should be visible
    expect(screen.getByRole("heading", { name: "Tasks" })).toBeTruthy();
    expect(screen.getByText("New Task")).toBeTruthy();
  });

  it("renders task filter buttons", () => {
    renderApp();

    expect(screen.getByText("All")).toBeTruthy();
    expect(screen.getByText("Queued")).toBeTruthy();
    expect(screen.getByText("In Progress")).toBeTruthy();
    expect(screen.getByText("Completed")).toBeTruthy();
  });

  it("renders mock task items", () => {
    renderApp();

    expect(screen.getByText("Implement user authentication flow")).toBeTruthy();
    expect(screen.getByText("Refactor database connection pooling")).toBeTruthy();
  });
});
