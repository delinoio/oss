import { render, screen } from "@testing-library/react";

import ThenvPage from "./page";

describe("ThenvPage", () => {
  it("renders the placeholder title and contract reference", () => {
    render(<ThenvPage />);

    expect(
      screen.getByRole("heading", { name: "Thenv Placeholder" }),
    ).toBeInTheDocument();
    expect(screen.getByText("docs/apps-thenv-web-console-foundation.md")).toBeInTheDocument();
  });
});
