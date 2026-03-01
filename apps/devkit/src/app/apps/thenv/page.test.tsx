import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, vi } from "vitest";

import ThenvPage from "./page";

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = typeof input === "string" ? input : input.toString();

  if (url.startsWith("/api/thenv/versions")) {
    return new Response(JSON.stringify({ versions: [], nextCursor: "" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.startsWith("/api/thenv/policy")) {
    return new Response(JSON.stringify({ bindings: [], policyRevision: 0 }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.startsWith("/api/thenv/audit")) {
    return new Response(
      JSON.stringify({
        events: [
          {
            eventId: "evt-1",
            eventType: "AUDIT_EVENT_TYPE_PULL",
            actor: "operator@example.com",
            bundleVersionId: "bundle-1",
            targetBundleVersionId: "",
            outcome: "OUTCOME_DENIED",
            requestId: "req-1",
            traceId: "trace-1",
            createdAt: "2026-02-24T00:00:00Z",
          },
        ],
        nextCursor: "",
      }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }

  return new Response(JSON.stringify({ error: `Unhandled URL: ${url}` }), {
    status: 404,
    headers: { "Content-Type": "application/json" },
  });
});

function auditCallUrls(): string[] {
  return fetchMock.mock.calls
    .map(([input]) => (typeof input === "string" ? input : input.toString()))
    .filter((url) => url.startsWith("/api/thenv/audit"));
}

describe("ThenvPage", () => {
  beforeEach(() => {
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });

  it("renders metadata console sections, outcome column, and secret safety statement", async () => {
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
    expect(
      await screen.findByRole("columnheader", { name: "Outcome" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Denied")).toBeInTheDocument();
  });

  it("applies from/to time filters when audit filter form submits", async () => {
    const user = userEvent.setup();
    render(<ThenvPage />);

    const fromTime = "2026-01-01T00:00:00Z";
    const toTime = "2026-01-31T23:59:59Z";

    await user.clear(screen.getByLabelText("From Time (ISO)"));
    await user.type(screen.getByLabelText("From Time (ISO)"), fromTime);
    await user.clear(screen.getByLabelText("To Time (ISO)"));
    await user.type(screen.getByLabelText("To Time (ISO)"), toTime);
    await user.click(screen.getByRole("button", { name: "Apply Audit Filters" }));

    await waitFor(() => {
      expect(
        auditCallUrls().some(
          (url) =>
            url.includes(`fromTime=${encodeURIComponent(fromTime)}`) &&
            url.includes(`toTime=${encodeURIComponent(toTime)}`),
        ),
      ).toBe(true);
    });
  });

  it("keeps unsaved policy draft bindings when applying audit filters", async () => {
    const user = userEvent.setup();
    render(<ThenvPage />);

    await screen.findByText("Denied");

    await user.type(screen.getByLabelText("Subject"), "draft-user");
    await user.click(screen.getByRole("button", { name: "Add Binding" }));
    expect(screen.getByText("draft-user")).toBeInTheDocument();

    await user.clear(screen.getByLabelText("From Time (ISO)"));
    await user.type(screen.getByLabelText("From Time (ISO)"), "2026-01-01T00:00:00Z");
    await user.clear(screen.getByLabelText("To Time (ISO)"));
    await user.type(screen.getByLabelText("To Time (ISO)"), "2026-01-31T23:59:59Z");
    await user.click(screen.getByRole("button", { name: "Apply Audit Filters" }));

    await waitFor(() => {
      expect(screen.getByText("draft-user")).toBeInTheDocument();
    });
  });
});
