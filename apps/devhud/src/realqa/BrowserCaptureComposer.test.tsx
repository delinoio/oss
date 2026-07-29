import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { takeBrowserCapture } from "./browserCapture";
import { BrowserCaptureComposer } from "./BrowserCaptureComposer";

vi.mock("./browserCapture", () => ({
  takeBrowserCapture: vi.fn(),
}));

const takeBrowserCaptureMock = vi.mocked(takeBrowserCapture);

describe("BrowserCaptureComposer", () => {
  beforeEach(() => {
    takeBrowserCaptureMock.mockReset();
  });

  it("drains and displays the in-memory browser capture", async () => {
    takeBrowserCaptureMock.mockResolvedValue({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b1",
      captureMode: "visible-viewport",
      page: {
        title: "Captured page",
        url: "https://example.com/private",
      },
      image: {
        mediaType: "png",
        base64: "iVBORw==",
        encodedBytes: 4,
      },
    });

    render(<BrowserCaptureComposer />);

    expect(await screen.findByRole("heading", { name: "Captured page" })).toBeVisible();
    expect(screen.getByAltText("Captured browser viewport")).toHaveAttribute(
      "src",
      "data:image/png;base64,iVBORw==",
    );
  });

  it("drains a later capture when the native listener signals availability", async () => {
    takeBrowserCaptureMock.mockResolvedValue(null);

    render(<BrowserCaptureComposer />);
    expect(
      await screen.findByRole("heading", { name: "Waiting for a capture" }),
    ).toBeVisible();

    const initialCalls = takeBrowserCaptureMock.mock.calls.length;
    takeBrowserCaptureMock.mockResolvedValueOnce({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b2",
      captureMode: "os-capture",
    });
    await act(async () => {
      window.dispatchEvent(
        new Event("devhud:realqa-browser-capture-available"),
      );
    });

    expect(
      await screen.findByText(
        "Chrome requested the native OS capture flow for this page.",
      ),
    ).toBeVisible();
    expect(takeBrowserCaptureMock.mock.calls.length).toBeGreaterThan(
      initialCalls,
    );
  });
});
