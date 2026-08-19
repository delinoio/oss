import { describe, expect, it } from "vitest";
import { apiBaseUrl } from "./api";

describe("administrator API base URL", () => {
  it("uses the loopback API only for the exact development origin", () => {
    expect(apiBaseUrl("http://localhost:46306")).toBe("http://127.0.0.1:46307");
  });

  it("keeps production deployments on the same origin even on the development port", () => {
    expect(apiBaseUrl("https://admin.example.com:46306")).toBe(
      "https://admin.example.com:46306",
    );
    expect(apiBaseUrl("http://127.0.0.1:46306")).toBe("http://127.0.0.1:46306");
  });
});
