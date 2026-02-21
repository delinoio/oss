import { render, screen } from "@testing-library/react";

import RemoteFilePickerPage from "./page";

describe("RemoteFilePickerPage", () => {
  it("renders the placeholder title and contract reference", () => {
    render(<RemoteFilePickerPage />);

    expect(
      screen.getByRole("heading", { name: "Remote File Picker Placeholder" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("docs/project-devkit-remote-file-picker.md"),
    ).toBeInTheDocument();
  });
});
