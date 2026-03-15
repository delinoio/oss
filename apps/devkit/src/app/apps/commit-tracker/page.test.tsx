import { render, screen } from "@testing-library/react";

import CommitTrackerPage from "./page";

describe("CommitTrackerPage", () => {
  it("renders the placeholder title and contract reference", () => {
    render(<CommitTrackerPage />);

    expect(
      screen.getByRole("heading", { name: "Commit Tracker Placeholder" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("docs/project-devkit-commit-tracker.md"),
    ).toBeInTheDocument();
  });
});
