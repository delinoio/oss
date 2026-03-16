import { render, screen } from "@testing-library/react";

import HomePage from "./page";

describe("HomePage", () => {
  it("renders shell-only bootstrap copy with placeholder-only mini apps", () => {
    render(<HomePage />);

    expect(screen.getByRole("heading", { name: "Shell-only bootstrap is active" })).toBeInTheDocument();
    expect(
      screen.getByText(
        /Devkit routes are now reserved with enum-based registration and static pages for each canonical mini app\./,
      ),
    ).toBeInTheDocument();

    expect(screen.getByRole("link", { name: "Commit Tracker (active)" })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Remote File Picker (active)" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Thenv (active)" })).toBeInTheDocument();
  });
});
