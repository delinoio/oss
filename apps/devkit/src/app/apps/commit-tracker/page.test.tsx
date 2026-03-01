import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, vi } from "vitest";

import * as logger from "@/lib/logger";
import CommitTrackerPage from "./page";

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === "string" ? input : input.toString();

  if (url.startsWith("/api/commit-tracker/series")) {
    return new Response(
      JSON.stringify({
        points: [
          {
            metricKey: "binary-size",
            displayName: "Binary Size",
            unit: "bytes",
            valueKind: "METRIC_VALUE_KIND_UNIT_NUMBER",
            direction: "METRIC_DIRECTION_DECREASE_IS_BETTER",
            warningThresholdPercent: 5,
            failThresholdPercent: 10,
            commitSha: "abc123",
            runId: "run-1",
            value: 1234,
            measuredAt: "2026-02-24T00:00:00Z",
          },
        ],
      }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }

  if (url.startsWith("/api/commit-tracker/comparison")) {
    return new Response(
      JSON.stringify({
        provider: "GIT_PROVIDER_KIND_GITHUB",
        repository: "acme/repo",
        baseCommitSha: "base-sha",
        headCommitSha: "head-sha",
        environment: "ci",
        aggregateEvaluation: "EVALUATION_LEVEL_FAIL",
        comparisons: [
          {
            metricKey: "binary-size",
            displayName: "Binary Size",
            unit: "bytes",
            valueKind: "METRIC_VALUE_KIND_UNIT_NUMBER",
            direction: "METRIC_DIRECTION_DECREASE_IS_BETTER",
            warningThresholdPercent: 5,
            failThresholdPercent: 10,
            baseValue: 100,
            headValue: 120,
            delta: 20,
            deltaPercent: 20,
            evaluationLevel: "EVALUATION_LEVEL_FAIL",
            hasBaseValue: true,
            hasHeadValue: true,
          },
        ],
      }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }

  if (url === "/api/commit-tracker/report") {
    const method = init?.method ?? "GET";
    if (method !== "POST") {
      return new Response("method not allowed", { status: 405 });
    }

    return new Response(
      JSON.stringify({
        aggregateEvaluation: "EVALUATION_LEVEL_FAIL",
        markdown: "report",
        commentUrl: "https://github.example/comment/1",
        statusUrl: "https://github.example/status/1",
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

describe("CommitTrackerPage", () => {
  beforeEach(() => {
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders live dashboard content instead of placeholder", () => {
    render(<CommitTrackerPage />);

    expect(
      screen.getByRole("heading", { name: "Commit Tracker Dashboard" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Commit Tracker Placeholder")).not.toBeInTheDocument();
  });

  it("loads series through /api/commit-tracker/series when filter form submits", async () => {
    const user = userEvent.setup();
    const logInfoSpy = vi.spyOn(logger, "logInfo").mockImplementation(() => undefined);
    render(<CommitTrackerPage />);

    await user.click(screen.getByRole("button", { name: "Load Metric Series" }));

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/commit-tracker/series"),
      expect.objectContaining({ cache: "no-store" }),
    );
    expect(await screen.findByText("abc123")).toBeInTheDocument();
    expect(logInfoSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerSeriesLoad,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "success",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 0,
          commit: "",
          run_id: "",
          metric_key: "",
          evaluation_level: "",
          delta_percent: 0,
        }),
      }),
    );
  });

  it("loads comparison and renders verdict table", async () => {
    const user = userEvent.setup();
    const logInfoSpy = vi.spyOn(logger, "logInfo").mockImplementation(() => undefined);
    render(<CommitTrackerPage />);

    await user.type(screen.getByLabelText("Base Commit"), "base-sha");
    await user.type(screen.getByLabelText("Head Commit"), "head-sha");
    await user.click(screen.getByRole("button", { name: "Compare Pull Request" }));

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/commit-tracker/comparison"),
      expect.objectContaining({ cache: "no-store" }),
    );
    expect(await screen.findByText("Aggregate Verdict:")).toBeInTheDocument();
    const failLabels = await screen.findAllByText("FAIL");
    expect(failLabels.length).toBeGreaterThan(0);
    expect(logInfoSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerComparisonLoad,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "success",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 0,
          commit: "head-sha",
          run_id: "",
          metric_key: "",
          evaluation_level: "EVALUATION_LEVEL_FAIL",
          delta_percent: 20,
        }),
      }),
    );
  });

  it("applies fail verdict badge styles in comparison results", async () => {
    const user = userEvent.setup();
    render(<CommitTrackerPage />);

    await user.type(screen.getByLabelText("Base Commit"), "base-sha");
    await user.type(screen.getByLabelText("Head Commit"), "head-sha");
    await user.click(screen.getByRole("button", { name: "Compare Pull Request" }));

    const failBadges = await screen.findAllByText("FAIL", {
      selector: ".dk-ct-verdict-badge",
    });
    expect(failBadges.length).toBeGreaterThan(0);
    expect(failBadges.some((badge) => badge.classList.contains("dk-ct-badge-fail"))).toBe(
      true,
    );
  });

  it("publishes report and shows result message", async () => {
    const user = userEvent.setup();
    const logInfoSpy = vi.spyOn(logger, "logInfo").mockImplementation(() => undefined);
    render(<CommitTrackerPage />);

    const pullRequestInput = screen.getByLabelText("Pull Request Number");
    await user.clear(pullRequestInput);
    await user.type(pullRequestInput, "23");
    await user.type(screen.getByLabelText("Head Commit"), "head-sha");

    await user.click(screen.getByRole("button", { name: "Publish Report to GitHub" }));

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/commit-tracker/report",
      expect.objectContaining({ method: "POST" }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Published report.",
    );
    expect(logInfoSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerReportPublish,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "success",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 123,
          commit: "head-sha",
          run_id: "",
          metric_key: "",
          evaluation_level: "EVALUATION_LEVEL_FAIL",
          delta_percent: 0,
        }),
      }),
    );
  });

  it("logs structured context when metric series load fails", async () => {
    const user = userEvent.setup();
    const logErrorSpy = vi.spyOn(logger, "logError").mockImplementation(() => undefined);
    fetchMock.mockImplementationOnce(async () => new Response("series failed", { status: 500 }));
    render(<CommitTrackerPage />);

    await user.click(screen.getByRole("button", { name: "Load Metric Series" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Request failed (500): series failed",
    );
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerSeriesLoad,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "failed",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 0,
          commit: "",
          run_id: "",
          metric_key: "",
          evaluation_level: "",
          delta_percent: 0,
        }),
      }),
    );
  });

  it("logs structured context when comparison load fails", async () => {
    const user = userEvent.setup();
    const logErrorSpy = vi.spyOn(logger, "logError").mockImplementation(() => undefined);
    fetchMock.mockImplementationOnce(
      async () => new Response("comparison failed", { status: 500 }),
    );
    render(<CommitTrackerPage />);

    await user.type(screen.getByLabelText("Base Commit"), "base-sha");
    await user.type(screen.getByLabelText("Head Commit"), "head-sha");
    await user.type(
      screen.getByLabelText("Metric Keys (comma-separated)"),
      "binary-size",
    );
    await user.click(screen.getByRole("button", { name: "Compare Pull Request" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Request failed (500): comparison failed",
    );
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerComparisonLoad,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "failed",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 0,
          commit: "head-sha",
          run_id: "",
          metric_key: "binary-size",
          evaluation_level: "",
          delta_percent: 0,
        }),
      }),
    );
  });

  it("logs publish failure with pull request and commit context", async () => {
    const user = userEvent.setup();
    const logErrorSpy = vi.spyOn(logger, "logError").mockImplementation(() => undefined);
    fetchMock.mockImplementationOnce(async () => new Response("publish failed", { status: 500 }));
    render(<CommitTrackerPage />);

    const pullRequestInput = screen.getByLabelText("Pull Request Number");
    await user.clear(pullRequestInput);
    await user.type(pullRequestInput, "99");
    await user.type(screen.getByLabelText("Head Commit"), "head-failure");
    await user.type(
      screen.getByLabelText("Metric Keys (comma-separated)"),
      "binary-size",
    );

    await user.click(screen.getByRole("button", { name: "Publish Report to GitHub" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Request failed (500): publish failed",
    );
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.CommitTrackerReportPublish,
        provider: "GIT_PROVIDER_KIND_GITHUB",
        outcome: "failed",
        context: expect.objectContaining({
          repository: "acme/repo",
          pull_request: 199,
          commit: "head-failure",
          run_id: "",
          metric_key: "binary-size",
          evaluation_level: "",
          delta_percent: 0,
        }),
      }),
    );
  });
});
