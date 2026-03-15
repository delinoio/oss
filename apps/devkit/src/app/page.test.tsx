import { render, screen } from "@testing-library/react";

import HomePage from "./page";

describe("HomePage", () => {
  it("renders scaffold-first status copy for placeholder and live mixed state", () => {
    render(<HomePage />);

    expect(
      screen.getByRole("heading", { name: "Scaffold-first rollout is active" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Devkit keeps canonical routes active at \/apps\/<id> with enum-based registration\. Commit Tracker and Remote File Picker are placeholder routes while Thenv remains live\./,
      ),
    ).toBeInTheDocument();

    expect(screen.getAllByText("Placeholder")).toHaveLength(2);
    expect(screen.getAllByText("Live")).toHaveLength(1);
  });
});
