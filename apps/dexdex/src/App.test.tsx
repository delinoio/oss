import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";

describe("App", () => {
  it("renders scaffold heading", () => {
    render(<App />);

    expect(screen.getByRole("heading", { name: "Welcome to Tauri + React" })).toBeTruthy();
    expect(screen.getByText("Click on the Tauri, Vite, and React logos to learn more.")).toBeTruthy();
  });

  it("shows greeting after form submit", async () => {
    const user = userEvent.setup();

    render(<App />);

    await user.type(screen.getByPlaceholderText("Enter a name..."), "DexDex");
    await user.click(screen.getByRole("button", { name: "Greet" }));

    expect(screen.getByText("Hello, DexDex!")).toBeTruthy();
  });
});
