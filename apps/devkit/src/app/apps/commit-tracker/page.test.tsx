import { render, screen } from "@testing-library/react";
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";

import CommitTrackerPage from "./page";

function renderWithProviders(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
  );
}

describe("CommitTrackerPage", () => {
  it("renders the commit tracker with repo selector", () => {
    renderWithProviders(<CommitTrackerPage />);

    expect(
      screen.getByRole("heading", { name: "Commit Tracker" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Repository")).toBeInTheDocument();
    expect(screen.getByText("Branch")).toBeInTheDocument();
    expect(screen.getByText("Provider")).toBeInTheDocument();
  });
});
