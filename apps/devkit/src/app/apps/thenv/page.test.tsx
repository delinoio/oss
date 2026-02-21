import { render, screen } from "@testing-library/react";
import { beforeEach, vi } from "vitest";

import ThenvPage from "./page";

const fetchMock = vi.fn(async () =>
  new Response(JSON.stringify({ versions: [], bindings: [], policyRevision: 0, events: [] }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  }),
);

describe("ThenvPage", () => {
  beforeEach(() => {
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });

  it("renders metadata console sections and secret safety statement", () => {
    render(<ThenvPage />);

    expect(
      screen.getByRole("heading", { name: "Thenv Metadata Console" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Version Inventory" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Policy Bindings" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Audit Events" })).toBeInTheDocument();
    expect(
      screen.getByText("Plaintext secret payloads are never shown in this UI."),
    ).toBeInTheDocument();
  });
});
