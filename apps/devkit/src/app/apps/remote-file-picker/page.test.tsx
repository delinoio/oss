import { render, screen } from "@testing-library/react";
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";

import RemoteFilePickerPage from "./page";

function renderWithProviders(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
  );
}

describe("RemoteFilePickerPage", () => {
  it("renders the file picker with drop zone", () => {
    renderWithProviders(<RemoteFilePickerPage />);

    expect(
      screen.getByRole("heading", { name: "Remote File Picker" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Drop file here")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Browse Files" })).toBeInTheDocument();
  });
});
