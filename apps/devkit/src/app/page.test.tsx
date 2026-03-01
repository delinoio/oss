import { render, screen } from "@testing-library/react";

import HomePage from "./page";

describe("HomePage", () => {
  it("renders live mini-app platform status copy and excludes stale bootstrap copy", () => {
    render(<HomePage />);

    expect(
      screen.getByRole("heading", { name: "Live mini-app platform is active" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Devkit now serves live mini apps at \/apps\/<id> with enum-based registration\. Commit Tracker, Remote File Picker, and Thenv are active routes\./,
      ),
    ).toBeInTheDocument();

    expect(
      screen.queryByRole("heading", { name: "Shell-only bootstrap is active" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(
        "Devkit routes are now reserved with enum-based registration and static pages for each canonical mini app.",
      ),
    ).not.toBeInTheDocument();
  });
});
